package artifactstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

func storedBody(t *testing.T, store *Store) string {
	t.Helper()
	sum := sha256.Sum256([]byte("the output"))
	digest := hex.EncodeToString(sum[:])
	if _, _, err := store.Put(strings.NewReader("the output"), digest, 1024); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestTheMetadataOfABodyRoundTrips(t *testing.T) {
	store := newStore(t)
	digest := storedBody(t, store)
	meta := Meta{Owner: "alice", MediaType: "text/plain", Size: 10, ExpiresAt: time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)}
	if err := store.PutMeta(digest, meta); err != nil {
		t.Fatalf("PutMeta() error = %v", err)
	}
	back, err := store.LoadMeta(digest)
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if back.Owner != "alice" || back.MediaType != "text/plain" || back.Size != 10 {
		t.Fatalf("meta = %+v", back)
	}
	if !back.ExpiresAt.Equal(meta.ExpiresAt) {
		t.Fatalf("expires_at = %v, want %v", back.ExpiresAt, meta.ExpiresAt)
	}
}

func TestABodyWithoutMetadataSaysSo(t *testing.T) {
	store := newStore(t)
	digest := storedBody(t, store)
	if _, err := store.LoadMeta(digest); !errors.Is(err, ErrNoMeta) {
		t.Fatalf("error = %v, want %v", err, ErrNoMeta)
	}
}

func TestMetadataWithoutAnOwnerIsRefused(t *testing.T) {
	store := newStore(t)
	digest := storedBody(t, store)
	if err := store.PutMeta(digest, Meta{Size: 1}); err == nil {
		t.Fatal("PutMeta() accepted metadata without an owner")
	}
}

func TestADigestThatIsNotADigestIsRefused(t *testing.T) {
	store := newStore(t)
	if err := store.PutMeta("../../etc/passwd", Meta{Owner: "alice"}); err == nil {
		t.Fatal("PutMeta() accepted a path")
	}
	if _, err := store.LoadMeta("../../etc/passwd"); err == nil {
		t.Fatal("LoadMeta() accepted a path")
	}
}
