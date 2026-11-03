package artifactstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Meta is what the Control Plane knows about a stored body besides its bytes.
// It is kept beside the body so a retrieval can decide whether to serve it
// without searching the state store (#494, #514).
type Meta struct {
	// Owner is the Principal that submitted the Task the body came from. It is
	// the only identity allowed to read the body.
	Owner string `json:"owner"`
	// MediaType is what the Worker declared. It is not a promise about the
	// content; the store never inspects the bytes.
	MediaType string `json:"media_type,omitempty"`
	// Size is the number of bytes the body had when it was accepted.
	Size int64 `json:"size"`
	// DataClass is what the Worker declared about the data (ADR-0034 §9). Empty
	// means the Worker said nothing, and then the field is not served at all: an
	// "unclassified" value would look like a classification.
	DataClass string `json:"data_class,omitempty"`
	// TaskID is the Task the body came from. It is what lets a retrieval ask
	// whether the Task was withdrawn (ADR-0037 §9, #592). Empty means the
	// record predates this field, and then no one is told the body was withdrawn.
	TaskID string `json:"task_id,omitempty"`
	// ExpiresAt is when the body may be removed.
	ExpiresAt time.Time `json:"expires_at"`
}

// ErrNoMeta means the body exists but nothing was recorded about it. A caller
// cannot decide access from that, so it is reported rather than guessed.
var ErrNoMeta = errors.New("artifactstore: no metadata recorded for the body")

// PutMeta records the metadata for a stored body. It is written after the body
// itself: a metadata record without a body would describe nothing.
func (s *Store) PutMeta(digestHex string, meta Meta) error {
	if _, err := parseDigestHex(digestHex); err != nil {
		return err
	}
	if meta.Owner == "" {
		return fmt.Errorf("artifactstore: metadata without an owner")
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("artifactstore: encode metadata: %w", err)
	}
	temp, err := os.CreateTemp(s.root, ".meta-*")
	if err != nil {
		return fmt.Errorf("artifactstore: write metadata: %w", err)
	}
	name := temp.Name()
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		os.Remove(name)
		return fmt.Errorf("artifactstore: write metadata: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("artifactstore: write metadata: %w", err)
	}
	if err := os.Rename(name, s.metaPath(digestHex)); err != nil {
		os.Remove(name)
		return fmt.Errorf("artifactstore: keep metadata: %w", err)
	}
	return nil
}

// LoadMeta reads the metadata recorded for a body.
func (s *Store) LoadMeta(digestHex string) (Meta, error) {
	if _, err := parseDigestHex(digestHex); err != nil {
		return Meta{}, err
	}
	raw, err := os.ReadFile(s.metaPath(digestHex))
	if errors.Is(err, os.ErrNotExist) {
		return Meta{}, ErrNoMeta
	}
	if err != nil {
		return Meta{}, fmt.Errorf("artifactstore: read metadata: %w", err)
	}
	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Meta{}, fmt.Errorf("artifactstore: decode metadata: %w", err)
	}
	if meta.Owner == "" {
		return Meta{}, ErrNoMeta
	}
	return meta, nil
}

func (s *Store) metaPath(digestHex string) string {
	return filepath.Join(s.root, strings.ToLower(digestHex)+".meta.json")
}
