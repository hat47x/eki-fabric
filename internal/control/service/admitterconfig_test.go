package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

func TestAPlainHexKeyIsAccepted(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sponsorKey(hex.EncodeToString(public))
	if err != nil {
		t.Fatalf("sponsorKey() error = %v", err)
	}
	if !got.Equal(public) {
		t.Fatal("sponsorKey() returned another key")
	}
}

func TestAPrefixedKeyIsAccepted(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sponsorKey("sha256:" + hex.EncodeToString(public)); err != nil {
		t.Fatalf("sponsorKey() error = %v", err)
	}
}

func TestAMalformedKeyIsRefused(t *testing.T) {
	for _, value := range []string{"", "zz", "abcd"} {
		if _, err := sponsorKey(value); !errors.Is(err, ErrTrustBundleKey) {
			t.Fatalf("sponsorKey(%q) error = %v, want %v", value, err, ErrTrustBundleKey)
		}
	}
}

func TestWithoutEveryPieceTheRouteStaysHidden(t *testing.T) {
	if admitter, err := admitterFor(nil, trustbundle.NewApprovalBook(), []string{"abc"}, nil); err != nil || admitter != nil {
		t.Fatalf("admitterFor() = %v, %v; want nil, nil", admitter, err)
	}
	store, err := trustbundle.NewStore(mustSignForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if admitter, err := admitterFor(store, trustbundle.NewApprovalBook(), nil, nil); err != nil || admitter != nil {
		t.Fatalf("admitterFor() = %v, %v; want nil, nil", admitter, err)
	}
}

func mustSignForTest(t *testing.T) trustbundle.Signed {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bundle := trustbundle.Bundle{
		Version:  1,
		Issuers:  []trustbundle.Issuer{{Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}},
		NotAfter: now.Add(24 * time.Hour),
	}
	signed, err := trustbundle.Sign(bundle, "k1", private)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
