package enroll

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/trustanchor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

func signedAt(t *testing.T, version uint64, key ed25519.PrivateKey) trustbundle.Signed {
	t.Helper()
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	bundle := trustbundle.Bundle{
		Version: version,
		Issuers: []trustbundle.Issuer{{
			Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour),
		}},
		NotAfter: now.Add(90*24*time.Hour + time.Duration(version)*24*time.Hour),
	}
	signed, err := trustbundle.Sign(bundle, "control-key-1", key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func anchorFile(t *testing.T, signed trustbundle.Signed) string {
	t.Helper()
	doc, err := trustanchor.File{}.WithVersion(signed)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "anchor.json")
	if err := trustanchor.WriteFile(path, doc); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheEnrollmentVersionIsWrittenIntoTheAnchorFile(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := anchorFile(t, signedAt(t, 1, priv))
	bundle := enrollwire.EncodeBundle(signedAt(t, 2, priv))
	out := enrollwire.Response{TrustBundle: &bundle}
	if err := AcceptBundleToFile(path, out, pub, time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("AcceptBundleToFile() error = %v", err)
	}
	doc, err := trustanchor.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.BundleRevision != 2 || len(doc.TrustBundleVersions) != 2 {
		t.Fatalf("doc = revision %d, versions %d", doc.BundleRevision, len(doc.TrustBundleVersions))
	}
}

func TestAVersionThatDoesNotVerifyLeavesTheFileAlone(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := anchorFile(t, signedAt(t, 1, priv))
	bundle := enrollwire.EncodeBundle(signedAt(t, 2, priv))
	out := enrollwire.Response{TrustBundle: &bundle}
	err = AcceptBundleToFile(path, out, otherPub, time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC))
	if !errors.Is(err, trustbundle.ErrBadSignature) {
		t.Fatalf("error = %v, want %v", err, trustbundle.ErrBadSignature)
	}
	doc, err := trustanchor.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.BundleRevision != 1 || len(doc.TrustBundleVersions) != 1 {
		t.Fatalf("the file changed: revision %d, versions %d", doc.BundleRevision, len(doc.TrustBundleVersions))
	}
}

func TestAResponseWithoutAVersionWritesNothing(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := anchorFile(t, signedAt(t, 1, priv))
	if err := AcceptBundleToFile(path, enrollwire.Response{}, pub, time.Now()); !errors.Is(err, ErrNoBundle) {
		t.Fatalf("error = %v, want %v", err, ErrNoBundle)
	}
}

// TestTheVersionTheResponseCarriesIsUsableAfterTheEnrollment is #488: the version
// an enrollment response carries must not only be written into the anchor file, it
// must be the version the Agent verifies with afterwards. A test that stops at
// "two versions are in the file" would pass even if the store could not read them.
func TestTheVersionTheResponseCarriesIsUsableAfterTheEnrollment(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := anchorFile(t, signedAt(t, 1, priv))
	bundle := enrollwire.EncodeBundle(signedAt(t, 2, priv))
	out := enrollwire.Response{TrustBundle: &bundle}
	if err := AcceptBundleToFile(path, out, pub, time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("AcceptBundleToFile() error = %v", err)
	}
	doc, err := trustanchor.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trustanchor.StoreFromFile(doc)
	if err != nil {
		t.Fatalf("StoreFromFile() error = %v", err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if current.Bundle.Version != 2 {
		t.Fatalf("version = %d, want the version the response carried", current.Bundle.Version)
	}
}
