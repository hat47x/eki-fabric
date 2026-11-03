package enroll

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/trustanchor"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

// A rotation leaves the Agent holding two keys for a while: the version may be
// signed by either, and refusing the old one would stop the Agent from following
// the history it already trusts (#478, ADR-0038 §7).
func TestAVersionSignedByAnyConfiguredKeyIsAccepted(t *testing.T) {
	oldPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newPub, newPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := anchorFile(t, signedAt(t, 1, newPriv))
	bundle := enrollwire.EncodeBundle(signedAt(t, 2, newPriv))
	out := enrollwire.Response{TrustBundle: &bundle}
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)

	if err := AcceptBundleToFileWithKeys(path, out, []ed25519.PublicKey{oldPub, newPub}, now); err != nil {
		t.Fatalf("AcceptBundleToFileWithKeys() error = %v, want the second key to be tried", err)
	}
	doc, err := trustanchor.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.BundleRevision != 2 {
		t.Fatalf("revision = %d, want 2", doc.BundleRevision)
	}
}

// When nothing verifies the file is left exactly as it was: a rotation that goes
// wrong must not leave the Agent with a version nobody signed for.
func TestAVersionNobodySignedForLeavesTheFileAlone(t *testing.T) {
	_, firstPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// The version is signed by a key the Agent does not hold: nothing verifies.
	_, strangerPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	heldPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := anchorFile(t, signedAt(t, 1, firstPriv))
	bundle := enrollwire.EncodeBundle(signedAt(t, 2, strangerPriv))
	out := enrollwire.Response{TrustBundle: &bundle}
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)

	if err := AcceptBundleToFileWithKeys(path, out, []ed25519.PublicKey{heldPub}, now); err == nil {
		t.Fatal("AcceptBundleToFileWithKeys() accepted a version signed by another key")
	}
	doc, err := trustanchor.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.BundleRevision != 1 {
		t.Fatalf("revision = %d, want the file left at 1", doc.BundleRevision)
	}
}

// No keys at all is a configuration mistake, and it is refused as one rather than
// reported as a bad signature.
func TestNoKeysIsRefused(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := anchorFile(t, signedAt(t, 1, priv))
	bundle := enrollwire.EncodeBundle(signedAt(t, 2, priv))
	out := enrollwire.Response{TrustBundle: &bundle}
	if err := AcceptBundleToFileWithKeys(path, out, nil, time.Now()); err == nil {
		t.Fatal("AcceptBundleToFileWithKeys() accepted an empty key set")
	}
}
