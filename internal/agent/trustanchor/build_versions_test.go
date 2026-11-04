package trustanchor

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

// validFile builds the smallest document Build accepts, with the version history
// the Agent keeps (ADR-0036, #478).
func validFile(t *testing.T) File {
	t.Helper()
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	key := func(role, id string) Key {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return Key{
			KeyID: id, Role: role, Principal: "control@example.org", Algorithm: "EdDSA",
			PublicKey:        base64.StdEncoding.EncodeToString(pub),
			SigningNotBefore: now.Add(-time.Hour), SigningNotAfter: now.Add(24 * time.Hour),
			KeyPolicy: "policy-1",
		}
	}
	doc := File{
		TrustDomain: "example.org", PolicyRevision: "policy-1", BundleRevision: 1, ValidatedAt: now,
		MaxClockSkewSeconds: 60, MaxBundleStalenessSeconds: 3600, MaxDecisionLifetimeSeconds: 300,
		Keys: []Key{key("LSE-1", "lse-key"), key("WOR-1", "wor-key")},
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := trustbundle.Bundle{
		Version: 1,
		Issuers: []trustbundle.Issuer{{
			Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour),
		}},
		NotAfter: now.Add(90 * 24 * time.Hour),
	}
	signed, err := trustbundle.Sign(bundle, "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	doc, err = doc.WithVersion(signed)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestBuildCarriesTheNewestStoredVersion(t *testing.T) {
	doc := validFile(t)
	anchor, err := Build(doc)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	version, ok := anchor.BundleVersion()
	if !ok {
		t.Fatal("the anchor does not carry the stored version")
	}
	if version.Bundle.Version != 1 {
		t.Fatalf("version = %d, want 1", version.Bundle.Version)
	}
	if anchor.TrustBundle().Revision != 1 {
		t.Fatalf("bundle revision = %d, want 1", anchor.TrustBundle().Revision)
	}
}

func TestBuildRefusesARevisionThatDisagreesWithTheHistory(t *testing.T) {
	doc := validFile(t)
	doc.BundleRevision = 5
	if _, err := Build(doc); err == nil {
		t.Fatal("Build() accepted a bundle_revision that disagrees with the history")
	}
}
