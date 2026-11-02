package enroll

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

func bundleFor(t *testing.T, version uint64, key ed25519.PrivateKey) enrollwire.Bundle {
	t.Helper()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	bundle := trustbundle.Bundle{
		Version: version,
		Issuers: []trustbundle.Issuer{{
			Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour),
		}},
		NotAfter: now.Add(90*24*time.Hour + time.Duration(version)*24*time.Hour),
	}
	signed, err := trustbundle.Sign(bundle, "control-key-1", key)
	if err != nil {
		t.Fatal(err)
	}
	return enrollwire.EncodeBundle(signed)
}

func agentStore(t *testing.T, key ed25519.PrivateKey) *trustbundle.Store {
	t.Helper()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	bundle := trustbundle.Bundle{
		Version: 1,
		Issuers: []trustbundle.Issuer{{
			Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour),
		}},
		NotAfter: now.Add(90 * 24 * time.Hour),
	}
	signed, err := trustbundle.Sign(bundle, "control-key-1", key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trustbundle.NewStore(signed)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTheAgentAcceptsTheVersionTheResponseCarries(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := agentStore(t, priv)
	out := enrollwire.Response{TrustBundle: ptr(bundleFor(t, 2, priv))}
	if err := AcceptBundle(out, pub, store, time.Date(2026, 11, 2, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("AcceptBundle() error = %v", err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.Bundle.Version != 2 {
		t.Fatalf("version = %d, want 2", current.Bundle.Version)
	}
}

func TestTheAgentRefusesAVersionItCannotVerify(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := agentStore(t, priv)
	out := enrollwire.Response{TrustBundle: ptr(bundleFor(t, 2, priv))}
	if err := AcceptBundle(out, otherPub, store, time.Date(2026, 11, 2, 13, 0, 0, 0, time.UTC)); !errors.Is(err, trustbundle.ErrBadSignature) {
		t.Fatalf("error = %v, want %v", err, trustbundle.ErrBadSignature)
	}
}

func TestAResponseWithoutAVersionIsNotAnErrorForEnrollment(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := agentStore(t, priv)
	if err := AcceptBundle(enrollwire.Response{}, pub, store, time.Now()); !errors.Is(err, ErrNoBundle) {
		t.Fatalf("error = %v, want %v", err, ErrNoBundle)
	}
}

func ptr[T any](v T) *T { return &v }
