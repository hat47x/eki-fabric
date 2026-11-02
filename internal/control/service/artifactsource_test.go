package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
)

func newArtifactStore(t *testing.T) *artifactfile.Store {
	t.Helper()
	store, err := artifactfile.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTheSourceReportsWhatWasRecorded(t *testing.T) {
	store := newArtifactStore(t)
	digest := putHello(t, store)
	sink := metaSink{store: store}
	if err := sink.PutMeta(digest, "alice", 5, "text/plain", time.Date(2026, 11, 3, 7, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	source := artifactSource{store: store}
	meta, err := source.LoadMeta(digest)
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if meta.Owner != "alice" || meta.Size != 5 || meta.MediaType != "text/plain" {
		t.Fatalf("meta = %+v", meta)
	}
	reader, size, err := source.Open(digest)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer reader.Close()
	body, _ := io.ReadAll(reader)
	if string(body) != "hello" || size != 5 {
		t.Fatalf("body = %q, size = %d", body, size)
	}
}

func TestABodyWithoutMetadataIsReportedAsMissing(t *testing.T) {
	store := newArtifactStore(t)
	digest := putHello(t, store)
	if _, err := (artifactSource{store: store}).LoadMeta(digest); !errors.Is(err, clientapi.ErrArtifactNotFound) {
		t.Fatalf("error = %v, want %v", err, clientapi.ErrArtifactNotFound)
	}
}

func TestAMissingBodyIsReportedAsMissing(t *testing.T) {
	store := newArtifactStore(t)
	missing := strings.Repeat("a", 64)
	source := artifactSource{store: store}
	if _, err := source.LoadMeta(missing); !errors.Is(err, clientapi.ErrArtifactNotFound) {
		t.Fatalf("LoadMeta error = %v, want %v", err, clientapi.ErrArtifactNotFound)
	}
	if _, _, err := source.Open(missing); !errors.Is(err, clientapi.ErrArtifactNotFound) {
		t.Fatalf("Open error = %v, want %v", err, clientapi.ErrArtifactNotFound)
	}
}

func putHello(t *testing.T, store *artifactfile.Store) string {
	t.Helper()
	sum := sha256.Sum256([]byte("hello"))
	digest := hex.EncodeToString(sum[:])
	if _, _, err := store.Put(strings.NewReader("hello"), digest, 1024); err != nil {
		t.Fatal(err)
	}
	return digest
}
