package trustanchor

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

func signedVersion(t *testing.T, version uint64) trustbundle.Signed {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	bundle := trustbundle.Bundle{
		Version: version,
		Issuers: []trustbundle.Issuer{{
			Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour),
		}},
		NotAfter: now.Add(90*24*time.Hour + time.Duration(version)*24*time.Hour),
	}
	signed, err := trustbundle.Sign(bundle, "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestTheFirstVersionMustBeOne(t *testing.T) {
	if _, err := (File{}).WithVersion(signedVersion(t, 2)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want %v", err, ErrInvalid)
	}
}

func TestAcceptingAVersionAppendsItAndBumpsTheRevision(t *testing.T) {
	doc, err := File{}.WithVersion(signedVersion(t, 1))
	if err != nil {
		t.Fatalf("WithVersion(1) error = %v", err)
	}
	if doc.BundleRevision != 1 || len(doc.TrustBundleVersions) != 1 {
		t.Fatalf("doc = %+v", doc)
	}
	doc, err = doc.WithVersion(signedVersion(t, 2))
	if err != nil {
		t.Fatalf("WithVersion(2) error = %v", err)
	}
	if doc.BundleRevision != 2 || len(doc.TrustBundleVersions) != 2 {
		t.Fatalf("doc = %+v", doc)
	}
	if err := checkVersions(doc); err != nil {
		t.Fatalf("checkVersions() error = %v", err)
	}
}

func TestAcceptingTheSameVersionTwiceIsIdempotent(t *testing.T) {
	first := signedVersion(t, 1)
	doc, err := File{}.WithVersion(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := doc.WithVersion(first)
	if err != nil {
		t.Fatalf("WithVersion(same) error = %v", err)
	}
	if len(again.TrustBundleVersions) != 1 {
		t.Fatalf("versions = %d, want 1", len(again.TrustBundleVersions))
	}
}

func TestAcceptingAVersionThatDoesNotFollowIsRefused(t *testing.T) {
	doc, err := File{}.WithVersion(signedVersion(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.WithVersion(signedVersion(t, 3)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want %v", err, ErrInvalid)
	}
}

func TestWriteFileReplacesTheDocumentAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anchor.json")
	doc, err := File{}.WithVersion(signedVersion(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, doc); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back File
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("the written file does not parse: %v", err)
	}
	if back.BundleRevision != doc.BundleRevision || len(back.TrustBundleVersions) != 1 {
		t.Fatalf("back = %+v", back)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "anchor.json" {
			t.Fatalf("a temporary file was left behind: %s", entry.Name())
		}
	}
}
