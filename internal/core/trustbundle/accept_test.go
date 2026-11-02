package trustbundle

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestAWorkerAcceptsTheNextVersionItHolds(t *testing.T) {
	pub, priv := testKey(t)
	store, err := NewStore(mustSign(t, validBundle(), priv))
	if err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = next.NotAfter.Add(24 * time.Hour)
	next.Issuers = append(next.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	if err := store.AcceptSigned(mustSign(t, next, priv), pub, base.Add(time.Hour)); err != nil {
		t.Fatalf("AcceptSigned() error = %v", err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.Bundle.Version != 2 {
		t.Fatalf("version = %d, want 2", current.Bundle.Version)
	}
}

func TestAWorkerIgnoresTheVersionItAlreadyHolds(t *testing.T) {
	pub, priv := testKey(t)
	store, err := NewStore(mustSign(t, validBundle(), priv))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptSigned(mustSign(t, validBundle(), priv), pub, base.Add(time.Hour)); err != nil {
		t.Fatalf("AcceptSigned() error = %v", err)
	}
	if len(store.Chain()) != 1 {
		t.Fatalf("chain = %d, want 1", len(store.Chain()))
	}
}

func TestAWorkerNeverGoesBack(t *testing.T) {
	pub, priv := testKey(t)
	store, err := NewStore(mustSign(t, validBundle(), priv))
	if err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = next.NotAfter.Add(24 * time.Hour)
	next.Issuers = append(next.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	if err := store.AcceptSigned(mustSign(t, next, priv), pub, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptSigned(mustSign(t, validBundle(), priv), pub, base.Add(time.Hour)); !errors.Is(err, ErrNotSuccessor) {
		t.Fatalf("error = %v, want %v", err, ErrNotSuccessor)
	}
}

func TestAWorkerRefusesAVersionItCannotVerify(t *testing.T) {
	pub, priv := testKey(t)
	otherPub, _ := testKey(t)
	store, err := NewStore(mustSign(t, validBundle(), priv))
	if err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = next.NotAfter.Add(24 * time.Hour)
	next.Issuers = append(next.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	if err := store.AcceptSigned(mustSign(t, next, priv), otherPub, base.Add(time.Hour)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("error = %v, want %v", err, ErrBadSignature)
	}
	_ = pub
}

func mustSign(t *testing.T, b Bundle, priv ed25519.PrivateKey) Signed {
	t.Helper()
	signed, err := Sign(b, "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
