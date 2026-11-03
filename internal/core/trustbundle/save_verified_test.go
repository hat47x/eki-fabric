package trustbundle

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// The saved record must say which key verified the signature, not only what the
// signer called it (ADR-0038 §8).
func TestSaveRecordsTheVerifiedKey(t *testing.T) {
	store := memory.NewTrustBundles()
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{
		Version:  1,
		Issuers:  []Issuer{{Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}},
		NotAfter: now.Add(24 * time.Hour),
	}
	signed, err := Sign(bundle, "control-key-1", private)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(context.Background(), store, signed, nil, "abcd1234", now); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	record, err := store.LatestTrustBundle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record.VerifiedKey != "abcd1234" {
		t.Fatalf("VerifiedKey = %q, want the fingerprint", record.VerifiedKey)
	}
	if record.KeyID != signed.KeyID {
		t.Fatalf("KeyID = %q, want the signer's claim %q", record.KeyID, signed.KeyID)
	}
}
