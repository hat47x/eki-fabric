package trustbundle

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
)

func TestARecordRoundTripsThroughStorage(t *testing.T) {
	_, priv := testKey(t)
	signed, err := Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromRecord(ToRecord(signed, base))
	if err != nil {
		t.Fatalf("FromRecord() error = %v", err)
	}
	if back.Bundle.Digest() != signed.Bundle.Digest() {
		t.Fatal("the digest changed through storage")
	}
	if back.KeyID != signed.KeyID || len(back.Signature) != len(signed.Signature) {
		t.Fatal("the signature did not round-trip")
	}
}

func TestAnAlteredRecordIsRefused(t *testing.T) {
	_, priv := testKey(t)
	signed, err := Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	record := ToRecord(signed, base)
	record.Issuers[0].NotAfter = record.Issuers[0].NotAfter.Add(24 * time.Hour)
	if _, err := FromRecord(record); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("error = %v, want %v", err, ErrBadSignature)
	}
}

func TestSaveThenLoadRebuildsTheChainAndApprovals(t *testing.T) {
	pub, priv := testKey(t)
	ctx := context.Background()
	first, err := Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.NewTrustBundles()
	if err := Save(ctx, store, first, nil, base); err != nil {
		t.Fatalf("Save(first) error = %v", err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = next.NotAfter.Add(24 * time.Hour)
	next.Issuers = append(next.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	second, err := Sign(next, "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(ctx, store, second, []Approval{approvalForBundle(2, OperationAdd, 9)}, base.Add(time.Hour)); err != nil {
		t.Fatalf("Save(second) error = %v", err)
	}
	loaded, err := Load(ctx, store)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	current, err := loaded.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.Bundle.Version != 2 {
		t.Fatalf("current = %d, want 2", current.Bundle.Version)
	}
	if len(loaded.Chain()) != 2 {
		t.Fatalf("chain = %d, want 2", len(loaded.Chain()))
	}
	if len(loaded.Approvals()) != 1 {
		t.Fatalf("approvals = %d, want 1", len(loaded.Approvals()))
	}
	keys := keyringOf(map[string]ed25519.PublicKey{"control-key-1": pub})
	if err := VerifyChain(loaded.Chain(), keys, base.Add(time.Hour)); err != nil {
		t.Fatalf("VerifyChain(loaded) error = %v", err)
	}
}

func TestLoadingAnEmptyStoreSaysItIsUninitialised(t *testing.T) {
	if _, err := Load(context.Background(), memory.NewTrustBundles()); !errors.Is(err, ErrNotInitialised) {
		t.Fatalf("error = %v, want %v", err, ErrNotInitialised)
	}
}
