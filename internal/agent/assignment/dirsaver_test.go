package assignment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// The saver writes the input under its digest, so the handler can find it again
// without a lookup table.
func TestDirSaverWritesTheInputUnderItsDigest(t *testing.T) {
	body := []byte("input-bytes")
	digest := contract.SHA256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	dir := t.TempDir()
	saver := DirSaver{Dir: dir}
	fetcher := HTTPFetcher{BaseURL: server.URL, Save: saver.Save}
	if err := fetcher.Fetch(context.Background(), digest); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(dir, digest.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(body) {
		t.Fatalf("stored = %q, want the served bytes", stored)
	}
	if info, err := os.Stat(filepath.Join(dir, digest.Hex())); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// A saver without a directory fails rather than writing somewhere unexpected.
func TestDirSaverWithoutADirectoryFails(t *testing.T) {
	if err := (DirSaver{}).Save(contract.SHA256([]byte("a")), []byte("a")); err == nil {
		t.Fatal("Save() ran without a directory")
	}
}
