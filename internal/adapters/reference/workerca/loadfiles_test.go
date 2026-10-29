package workerca_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
)

// writeCAFiles writes a fresh CA certificate and key, and sets the key's mode.
func writeCAFiles(t *testing.T, keyMode os.FileMode) (certPath, keyPath string) {
	t.Helper()
	_, certPEM, keyPEM := newCA(t, "example.org", 0)
	dir := t.TempDir()
	certPath = filepath.Join(dir, "ca.pem")
	keyPath = filepath.Join(dir, "ca.key")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	// WriteFile applies the umask, so set the mode explicitly.
	if err := os.Chmod(keyPath, keyMode); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestLoadFilesAcceptsAnOwnerOnlyKey(t *testing.T) {
	certPath, keyPath := writeCAFiles(t, 0o600)
	if _, err := workerca.LoadFiles(certPath, keyPath, "example.org", 0); err != nil {
		t.Fatalf("a 0600 key must load: %v", err)
	}
}

func TestLoadFilesRefusesAKeyOthersCanAccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits carry no meaning on Windows")
	}
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660, 0o666} {
		certPath, keyPath := writeCAFiles(t, mode)
		_, err := workerca.LoadFiles(certPath, keyPath, "example.org", 0)
		if !errors.Is(err, workerca.ErrInvalidCA) {
			t.Fatalf("mode %04o: want ErrInvalidCA, got %v", mode, err)
		}
		if !strings.Contains(err.Error(), "chmod 600") || !strings.Contains(err.Error(), "mode ") {
			t.Fatalf("mode %04o: the error must show the mode and the fix: %v", mode, err)
		}
	}
}

func TestLoadFilesReportsAMissingKey(t *testing.T) {
	certPath, keyPath := writeCAFiles(t, 0o600)
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	_, err := workerca.LoadFiles(certPath, keyPath, "example.org", 0)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want a not-exist error, got %v", err)
	}
}
