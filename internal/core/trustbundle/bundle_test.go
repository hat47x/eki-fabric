package trustbundle

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

var base = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

func issuer(n byte, from, to time.Time) Issuer {
	return Issuer{Fingerprint: contract.SHA256([]byte{n}), NotBefore: from, NotAfter: to}
}

func validBundle() Bundle {
	return Bundle{
		Version:  1,
		Issuers:  []Issuer{issuer(1, base, base.Add(365*24*time.Hour))},
		NotAfter: base.Add(90 * 24 * time.Hour),
	}
}

func TestAWellFormedBundleIsCurrentInsideItsWindow(t *testing.T) {
	b := validBundle()
	if err := b.Current(base.Add(time.Hour)); err != nil {
		t.Fatalf("Current() error = %v", err)
	}
}

func TestABundleWithoutVersionOrIssuerIsRejected(t *testing.T) {
	b := validBundle()
	b.Version = 0
	if err := b.Validate(); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("version 0 error = %v, want %v", err, ErrInvalidBundle)
	}
	b = validBundle()
	b.Issuers = nil
	if err := b.Validate(); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("no issuer error = %v, want %v", err, ErrInvalidBundle)
	}
}

func TestAnExpiredBundleIsNotCurrentEvenWhenWellFormed(t *testing.T) {
	b := validBundle()
	b.NotAfter = base.Add(time.Hour)
	if err := b.Current(base.Add(2 * time.Hour)); !errors.Is(err, ErrNotCurrent) {
		t.Fatalf("expired error = %v, want %v", err, ErrNotCurrent)
	}
}

func TestTheDigestChangesWithTheVersionAndTheIssuers(t *testing.T) {
	first := validBundle().Digest()
	if first.IsZero() {
		t.Fatal("digest is zero")
	}
	next := validBundle()
	next.Version = 2
	if next.Digest() == first {
		t.Fatal("the digest did not change with the version")
	}
	other := validBundle()
	other.Issuers = []Issuer{issuer(2, base, base.Add(365*24*time.Hour))}
	if other.Digest() == first {
		t.Fatal("the digest did not change with the issuer")
	}
}

func TestASuccessorMustBeExactlyTheNextVersion(t *testing.T) {
	first := validBundle()
	next := validBundle()
	next.Version = 2
	next.NotAfter = first.NotAfter.Add(24 * time.Hour)
	if err := next.Follows(first); err != nil {
		t.Fatalf("Follows() error = %v", err)
	}
	skip := validBundle()
	skip.Version = 3
	skip.NotAfter = first.NotAfter.Add(24 * time.Hour)
	if err := skip.Follows(first); !errors.Is(err, ErrNotSuccessor) {
		t.Fatalf("skipping error = %v, want %v", err, ErrNotSuccessor)
	}
	back := validBundle()
	back.Version = 1
	back.NotAfter = first.NotAfter.Add(24 * time.Hour)
	if err := back.Follows(first); !errors.Is(err, ErrNotSuccessor) {
		t.Fatalf("same version error = %v, want %v", err, ErrNotSuccessor)
	}
}

func TestTheNextVersionMustNotExpireEarlier(t *testing.T) {
	first := validBundle()
	next := validBundle()
	next.Version = 2
	next.NotAfter = first.NotAfter
	if err := next.Follows(first); !errors.Is(err, ErrNotSuccessor) {
		t.Fatalf("earlier expiry error = %v, want %v", err, ErrNotSuccessor)
	}
}
