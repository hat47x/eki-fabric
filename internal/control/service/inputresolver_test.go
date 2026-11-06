package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// A reference the store can answer for is accepted, and the digest it names is
// what admission records (#763).
func TestAnInputThatIsStoredResolves(t *testing.T) {
	dir := t.TempDir()
	store, err := artifactfile.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("input bytes")
	digest := contract.SHA256(body)
	if _, _, err := store.Put(bytes.NewReader(body), digest.Hex(), 0); err != nil {
		t.Fatal(err)
	}
	resolver := artifactResolver{store: store}
	got, err := resolver.ResolveArtifact(context.Background(), digest.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if got.Hex() != digest.Hex() {
		t.Fatalf("digest = %s, want %s", got.Hex(), digest.Hex())
	}
}

// A reference whose body was never kept is refused: a Worker must not be told to
// fetch something no one stored.
func TestAnInputThatIsNotStoredIsRefused(t *testing.T) {
	dir := t.TempDir()
	store, err := artifactfile.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	resolver := artifactResolver{store: store}
	_, err = resolver.ResolveArtifact(context.Background(), contract.SHA256([]byte("never written")).Hex())
	if !errors.Is(err, admission.ErrUnresolvableArtifact) {
		t.Fatalf("error = %v, want ErrUnresolvableArtifact", err)
	}
}

// A reference that is not a digest is refused before any IO: the name is the
// digest and nothing else (#763, ADR-0029 §5.4).
func TestANameThatIsNotADigestIsRefused(t *testing.T) {
	dir := t.TempDir()
	store, err := artifactfile.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	resolver := artifactResolver{store: store}
	for _, id := range []string{"art_01", "", "../../etc/passwd"} {
		if _, err := resolver.ResolveArtifact(context.Background(), id); !errors.Is(err, admission.ErrUnresolvableArtifact) {
			t.Errorf("ResolveArtifact(%q) error = %v, want ErrUnresolvableArtifact", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "etc", "passwd")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("stat: %v", err)
	}
}
