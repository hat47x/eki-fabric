package trustbundle

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestASignedBundleVerifiesWithTheControlPlaneKey(t *testing.T) {
	pub, priv := testKey(t)
	signed, err := Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if err := signed.Verify(pub); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := signed.Accept(pub, base.Add(time.Hour)); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
}

func TestAnAlteredBundleDoesNotVerify(t *testing.T) {
	pub, priv := testKey(t)
	signed, err := Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	signed.Bundle.Version = 2
	if err := signed.Verify(pub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("altered bundle error = %v, want %v", err, ErrBadSignature)
	}
}

func TestAnotherKeyDoesNotVerify(t *testing.T) {
	_, priv := testKey(t)
	other, _ := testKey(t)
	signed, err := Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := signed.Verify(other); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("foreign key error = %v, want %v", err, ErrBadSignature)
	}
}

func TestAnUnsignedOrUnnamedSignatureIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	signed, err := Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	signed.Signature = nil
	if err := signed.Verify(pub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("no signature error = %v, want %v", err, ErrBadSignature)
	}
	signed, err = Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	signed.KeyID = ""
	if err := signed.Verify(pub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("no key id error = %v, want %v", err, ErrBadSignature)
	}
}

func TestAnInvalidBundleCannotBeSigned(t *testing.T) {
	_, priv := testKey(t)
	broken := validBundle()
	broken.Version = 0
	if _, err := Sign(broken, "control-key-1", priv); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("invalid bundle error = %v, want %v", err, ErrInvalidBundle)
	}
}

func TestAnExpiredBundleDoesNotAccept(t *testing.T) {
	pub, priv := testKey(t)
	b := validBundle()
	b.NotAfter = base.Add(time.Hour)
	signed, err := Sign(b, "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := signed.Verify(pub); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := signed.Accept(pub, base.Add(2*time.Hour)); !errors.Is(err, ErrNotCurrent) {
		t.Fatalf("expired Accept() error = %v, want %v", err, ErrNotCurrent)
	}
}
