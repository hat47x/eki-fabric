package trustbundle

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// The first version cannot be approved: an Operator certificate is issued by a CA
// the version itself lists. The configured key is what makes it trusted, and every
// later version needs approvals (ADR-0036 §9.2).
func TestAnEmptyStoreTakesTheFirstVersionWithoutApprovals(t *testing.T) {
	pub, priv := testKey(t)
	store := &Store{}
	first := signedVersion(t, validBundle(), "control-key-1", priv)
	if err := store.Admit(first, pub, nil, base); err != nil {
		t.Fatalf("Admit(first) error = %v, want it to be taken without approvals", err)
	}
	if current, err := store.Current(); err != nil || current.Bundle.Version != first.Bundle.Version {
		t.Fatalf("Current() = %d, %v", current.Bundle.Version, err)
	}
}

// The allowance is for the first version only: the next one needs approvals, or a
// version could be swapped without anyone agreeing to it.
func TestTheSecondVersionStillNeedsApprovals(t *testing.T) {
	pub, priv := testKey(t)
	store := &Store{}
	first := signedVersion(t, validBundle(), "control-key-1", priv)
	if err := store.Admit(first, pub, nil, base); err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = next.NotAfter.Add(time.Hour)
	next.Issuers = append(next.Issuers, Issuer{Fingerprint: contract.SHA256([]byte("ca-2")), NotBefore: base, NotAfter: base.Add(time.Hour)})
	if err := store.Admit(signedVersion(t, next, "control-key-1", priv), pub, nil, base); !errors.Is(err, ErrUnapproved) {
		t.Fatalf("Admit(second) error = %v, want %v", err, ErrUnapproved)
	}
}

// The first version is unapproved, not unchecked: a signature that does not verify
// is still refused.
func TestTheFirstVersionStillNeedsAValidSignature(t *testing.T) {
	pub, _ := testKey(t)
	_, otherPriv := testKey(t)
	store := &Store{}
	first := signedVersion(t, validBundle(), "control-key-1", otherPriv)
	if err := store.Admit(first, pub, nil, base); err == nil {
		t.Fatal("Admit() took a first version signed by another key")
	}
}
