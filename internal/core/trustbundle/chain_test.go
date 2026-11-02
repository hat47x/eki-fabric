package trustbundle

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func keyringOf(entries map[string]ed25519.PublicKey) Keyring {
	return KeyringFunc(func(id string) (ed25519.PublicKey, bool) {
		key, ok := entries[id]
		return key, ok
	})
}

func TestAChainOfSuccessorsVerifies(t *testing.T) {
	pub, priv := testKey(t)
	first, err := Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = first.Bundle.NotAfter.Add(24 * time.Hour)
	second, err := Sign(next, "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	chain := []Signed{first, second}
	if err := VerifyChain(chain, keyringOf(map[string]ed25519.PublicKey{"control-key-1": pub}), base.Add(time.Hour)); err != nil {
		t.Fatalf("VerifyChain() error = %v", err)
	}
}

func TestAChainWithAGapIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	first, err := Sign(validBundle(), "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	skipping := validBundle()
	skipping.Version = 3
	skipping.NotAfter = first.Bundle.NotAfter.Add(48 * time.Hour)
	third, err := Sign(skipping, "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	err = VerifyChain([]Signed{first, third}, keyringOf(map[string]ed25519.PublicKey{"control-key-1": pub}), base)
	if !errors.Is(err, ErrNotSuccessor) {
		t.Fatalf("gap error = %v, want %v", err, ErrNotSuccessor)
	}
}

func TestAChainWithAnUnknownKeyIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	first, err := Sign(validBundle(), "control-key-2", priv)
	if err != nil {
		t.Fatal(err)
	}
	err = VerifyChain([]Signed{first}, keyringOf(map[string]ed25519.PublicKey{"control-key-1": pub}), base)
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key error = %v, want %v", err, ErrUnknownKey)
	}
}

func TestAnEmptyChainIsNotAValidChain(t *testing.T) {
	if err := VerifyChain(nil, keyringOf(nil), base); !errors.Is(err, ErrEmptyChain) {
		t.Fatalf("empty chain error = %v, want %v", err, ErrEmptyChain)
	}
}

func TestAnExpiredLastBundleIsRefusedEvenIfItVerifies(t *testing.T) {
	pub, priv := testKey(t)
	b := validBundle()
	b.NotAfter = base.Add(time.Hour)
	signed, err := Sign(b, "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	err = VerifyChain([]Signed{signed}, keyringOf(map[string]ed25519.PublicKey{"control-key-1": pub}), base.Add(2*time.Hour))
	if !errors.Is(err, ErrNotCurrent) {
		t.Fatalf("expired chain error = %v, want %v", err, ErrNotCurrent)
	}
}
