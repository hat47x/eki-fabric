package clientapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// MaxIngestBytes is the largest body this deployment accepts in one ingestion. A
// Client that declares more is refused at the declaration, before it sends anything
// (ADR-0029 §5.12).
const MaxIngestBytes = 64 << 20

// ingestIntent is the declaration a Client makes before sending a body: what it is
// about to send, and how big it is.
type ingestIntent struct {
	Sha256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	DataClass string `json:"data_class,omitempty"`
}

// ingestAccepted is the answer to a declaration. It names the digest that will be
// used as the body's name; the Client sends the bytes against it.
type ingestAccepted struct {
	Schema   string `json:"schema"`
	Sha256   string `json:"sha256"`
	MaxBytes int64  `json:"max_bytes"`
	// UploadPath is where the body goes. It is built from the digest, so the
	// Client cannot choose a destination.
	UploadPath string `json:"upload_path"`
}

// ErrIngestRefused is returned for a declaration this deployment will not accept.
var ErrIngestRefused = errors.New("clientapi: ingest intent refused")

// validateIngest checks a declaration without touching any storage: the shape, the
// digest, the size and the declared class. It returns the parsed digest.
func validateIngest(raw []byte) (ingestIntent, contract.Digest, error) {
	var intent ingestIntent
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil {
		return ingestIntent{}, contract.Digest{}, fmt.Errorf("%w: %v", ErrIngestRefused, err)
	}
	digest, err := contract.ParseSHA256(intent.Sha256)
	if err != nil {
		return ingestIntent{}, contract.Digest{}, fmt.Errorf("%w: sha256: %v", ErrIngestRefused, err)
	}
	switch {
	case intent.SizeBytes <= 0:
		return ingestIntent{}, contract.Digest{}, fmt.Errorf("%w: size_bytes must be positive", ErrIngestRefused)
	case intent.SizeBytes > MaxIngestBytes:
		return ingestIntent{}, contract.Digest{}, fmt.Errorf("%w: size_bytes exceeds %d", ErrIngestRefused, MaxIngestBytes)
	case !admission.ValidDataClass(intent.DataClass):
		return ingestIntent{}, contract.Digest{}, fmt.Errorf("%w: unknown data_class", ErrIngestRefused)
	}
	return intent, digest, nil
}
