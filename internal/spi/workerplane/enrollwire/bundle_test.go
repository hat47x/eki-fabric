package enrollwire

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

func signedBundle(t *testing.T) trustbundle.Signed {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
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
	return signed
}

func TestABundleRoundTripsThroughTheWire(t *testing.T) {
	signed := signedBundle(t)
	back, err := DecodeBundle(EncodeBundle(signed))
	if err != nil {
		t.Fatalf("DecodeBundle() error = %v", err)
	}
	if back.Bundle.Digest() != signed.Bundle.Digest() {
		t.Fatal("the digest changed through the wire")
	}
	if back.KeyID != signed.KeyID || len(back.Signature) != len(signed.Signature) {
		t.Fatal("the signature did not round-trip")
	}
}

func TestAPartialBundleIsRefused(t *testing.T) {
	wire := EncodeBundle(signedBundle(t))
	wire.Issuers = nil
	if _, err := DecodeBundle(wire); !errors.Is(err, ErrInvalidBundleWire) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidBundleWire)
	}
	wire = EncodeBundle(signedBundle(t))
	wire.Signature = "not base64"
	if _, err := DecodeBundle(wire); !errors.Is(err, ErrInvalidBundleWire) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidBundleWire)
	}
}

func TestAnEnrollmentResponseWithAnInvalidBundleIsRefused(t *testing.T) {
	wire := EncodeBundle(signedBundle(t))
	wire.Version = 0
	raw := []byte(`{"worker_identity":"w","trust_domain":"example.org","certificate_pem":"x","not_after":"2026-11-03T00:00:00Z","trust_bundle":{"version":0,"not_after":"2026-11-03T00:00:00Z","key_id":"k","signature":"AA==","issuers":[]}}`)
	_ = wire
	if _, err := DecodeResponse(raw); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidResponse)
	}
}
