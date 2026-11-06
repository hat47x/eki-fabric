package fs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func put(t *testing.T, store Store, body string) contract.Digest {
	t.Helper()
	digest := contract.SHA256([]byte(body))
	if _, err := store.Put(context.Background(), digest, strings.NewReader(body)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	return digest
}

// The body is stored under its digest and read back unchanged.
func TestABodyRoundTripsThroughTheStore(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	digest := put(t, store, "artifact-bytes")

	reader, size, err := store.Open(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "artifact-bytes" || size != int64(len("artifact-bytes")) {
		t.Fatalf("Open() = %q, %d", got, size)
	}
}

// Bytes that do not hash to the name are refused and nothing is left behind: the
// name would otherwise describe other content.
func TestABodyThatDoesNotMatchItsDigestIsRefused(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	digest := contract.SHA256([]byte("expected"))

	if _, err := store.Put(context.Background(), digest, strings.NewReader("something else")); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Put() error = %v, want %v", err, ErrDigestMismatch)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory holds %d entries, want none after a refused write", len(entries))
	}
}

// A digest with no body is reported as missing, so the route can answer 404.
func TestAMissingBodyIsReported(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	if _, _, err := store.Open(context.Background(), contract.SHA256([]byte("absent"))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open() error = %v, want %v", err, ErrNotFound)
	}
}

// Removing a body that is already gone is not an error: the sweep asks for removal,
// not for proof that the body was there.
func TestRemovingAMissingBodySucceeds(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	digest := contract.SHA256([]byte("absent"))
	if err := store.Remove(context.Background(), digest); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
}

// A store without a directory fails instead of writing somewhere unexpected.
func TestAStoreWithoutADirectoryFails(t *testing.T) {
	if _, err := (Store{}).Put(context.Background(), contract.SHA256([]byte("a")), strings.NewReader("a")); err == nil {
		t.Fatal("Put() ran without a directory")
	}
	if _, _, err := (Store{}).Open(context.Background(), contract.SHA256([]byte("a"))); err == nil {
		t.Fatal("Open() ran without a directory")
	}
}

// The file is written with 0600, so other users of the host cannot read it.
func TestTheStoredBodyIsPrivate(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	digest := put(t, store, "secret")
	info, err := os.Stat(filepath.Join(dir, digest.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}
