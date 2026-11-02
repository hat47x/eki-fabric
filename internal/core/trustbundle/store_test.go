package trustbundle

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func signedVersion(t *testing.T, b Bundle, keyID string, priv ed25519.PrivateKey) Signed {
	t.Helper()
	signed, err := Sign(b, keyID, priv)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestAStoreStartsAtTheOutOfBandVersion(t *testing.T) {
	_, priv := testKey(t)
	store, err := NewStore(signedVersion(t, validBundle(), "control-key-1", priv))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if current.Bundle.Version != 1 {
		t.Fatalf("version = %d, want 1", current.Bundle.Version)
	}
}

func TestAdmittingTheNextApprovedVersionMovesCurrent(t *testing.T) {
	pub, priv := testKey(t)
	store, err := NewStore(signedVersion(t, validBundle(), "control-key-1", priv))
	if err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = next.NotAfter.Add(24 * time.Hour)
	next.Issuers = append(next.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	if err := store.Admit(signedVersion(t, next, "control-key-1", priv), pub, []Approval{approvalForBundle(2, OperationAdd, 9)}, base.Add(time.Hour)); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.Bundle.Version != 2 {
		t.Fatalf("version = %d, want 2", current.Bundle.Version)
	}
	if len(store.Approvals()) != 1 {
		t.Fatalf("approvals = %d, want 1", len(store.Approvals()))
	}
	if len(store.Chain()) != 2 {
		t.Fatalf("chain = %d, want 2", len(store.Chain()))
	}
}

func TestAnUnapprovedVersionIsNotAdmitted(t *testing.T) {
	pub, priv := testKey(t)
	store, err := NewStore(signedVersion(t, validBundle(), "control-key-1", priv))
	if err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = next.NotAfter.Add(24 * time.Hour)
	next.Issuers = append(next.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	err = store.Admit(signedVersion(t, next, "control-key-1", priv), pub, nil, base.Add(time.Hour))
	if !errors.Is(err, ErrUnapproved) {
		t.Fatalf("error = %v, want %v", err, ErrUnapproved)
	}
	if len(store.Chain()) != 1 {
		t.Fatal("the store changed on a refused version")
	}
}

func TestAVersionSignedByAnotherKeyIsNotAdmitted(t *testing.T) {
	_, priv := testKey(t)
	otherPub, _ := testKey(t)
	store, err := NewStore(signedVersion(t, validBundle(), "control-key-1", priv))
	if err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = next.NotAfter.Add(24 * time.Hour)
	next.Issuers = append(next.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	err = store.Admit(signedVersion(t, next, "control-key-1", priv), otherPub, []Approval{approvalForBundle(2, OperationAdd, 9)}, base.Add(time.Hour))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("error = %v, want %v", err, ErrBadSignature)
	}
}

func TestAnEmptyStoreHasNoCurrentVersion(t *testing.T) {
	var store *Store
	if _, err := store.Current(); !errors.Is(err, ErrNotInitialised) {
		t.Fatalf("error = %v, want %v", err, ErrNotInitialised)
	}
}
