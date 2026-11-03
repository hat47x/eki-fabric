package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

// The fingerprint is the fact an investigation compares against a configured key;
// the signer's key_id is only a claim (ADR-0038 §8).
func TestTheFingerprintNamesTheKeyItself(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	first := fingerprint(public)
	if len(first) != 64 {
		t.Fatalf("fingerprint = %q, want 64 hex characters", first)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("fingerprint is not hex: %v", err)
	}
	if fingerprint(public) != first {
		t.Fatal("fingerprint is not stable for the same key")
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint(other) == first {
		t.Fatal("two different keys share a fingerprint")
	}
}
