package decisiontrust

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

func TestNewEd25519KeyRecord(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	record, err := NewKeyRecord(ed25519Config("kid-ed-1", 1, now), now)
	if err != nil {
		t.Fatalf("NewKeyRecord() error = %v", err)
	}
	if record.State() != KeyStatePending {
		t.Fatalf("State() = %q, want %q", record.State(), KeyStatePending)
	}
	if record.Algorithm() != AlgorithmEdDSA || record.KeyType() != KeyTypeOKP || record.Curve() != CurveEd25519 {
		t.Fatalf("unexpected key profile: %q/%q/%q", record.Algorithm(), record.KeyType(), record.Curve())
	}
	if record.PublicKeyDigest().IsZero() {
		t.Fatal("PublicKeyDigest() is zero")
	}
	if record.Revision() != 1 {
		t.Fatalf("Revision() = %d, want 1", record.Revision())
	}
}

func TestNewES256KeyRecord(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	record, err := NewKeyRecord(es256Config("kid-es-1", now), now)
	if err != nil {
		t.Fatalf("NewKeyRecord() error = %v", err)
	}
	if record.Algorithm() != AlgorithmES256 || record.KeyType() != KeyTypeEC2 || record.Curve() != CurveP256 {
		t.Fatalf("unexpected key profile: %q/%q/%q", record.Algorithm(), record.KeyType(), record.Curve())
	}
}

func TestNewKeyRecordRejectsAlgorithmConfusion(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	ed := ed25519Config("kid-confused-ed", 1, now)
	ed.Algorithm = AlgorithmES256
	ed.KeyType = KeyTypeEC2
	ed.Curve = CurveP256
	if _, err := NewKeyRecord(ed, now); !errors.Is(err, ErrInvalidKeyRecord) {
		t.Fatalf("Ed25519 key as ES256 error = %v, want ErrInvalidKeyRecord", err)
	}

	es := es256Config("kid-confused-es", now)
	es.Algorithm = AlgorithmEdDSA
	es.KeyType = KeyTypeOKP
	es.Curve = CurveEd25519
	if _, err := NewKeyRecord(es, now); !errors.Is(err, ErrInvalidKeyRecord) {
		t.Fatalf("P-256 key as Ed25519 error = %v, want ErrInvalidKeyRecord", err)
	}
}

func TestKeyRecordReturnsDefensivePublicKeyCopy(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	record, err := NewKeyRecord(ed25519Config("kid-copy", 1, now), now)
	if err != nil {
		t.Fatalf("NewKeyRecord() error = %v", err)
	}

	got := record.PublicKey().(ed25519.PublicKey)
	got[0] ^= 0xff

	again := record.PublicKey().(ed25519.PublicKey)
	if got[0] == again[0] {
		t.Fatal("mutating returned public key changed record")
	}
}

func TestNewES256KeyRecordRejectsWrongCurveMetadata(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	cfg := es256Config("kid-wrong-curve", now)
	cfg.PublicKey.(*ecdsa.PublicKey).Curve = elliptic.P384()

	if _, err := NewKeyRecord(cfg, now); !errors.Is(err, ErrInvalidKeyRecord) {
		t.Fatalf("wrong curve metadata error = %v, want ErrInvalidKeyRecord", err)
	}
}

func TestNewKeyRecordRejectsInvalidWindowAndRole(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	cfg := ed25519Config("kid-window", 1, now)
	cfg.SigningNotAfter = cfg.SigningNotBefore
	if _, err := NewKeyRecord(cfg, now); !errors.Is(err, ErrInvalidKeyRecord) {
		t.Fatalf("invalid window error = %v, want ErrInvalidKeyRecord", err)
	}

	cfg = ed25519Config("kid-role", 1, now)
	cfg.AuthorityRole = authority.Role("UNKNOWN")
	if _, err := NewKeyRecord(cfg, now); !errors.Is(err, ErrInvalidKeyRecord) {
		t.Fatalf("unknown role error = %v, want ErrInvalidKeyRecord", err)
	}
}

func ed25519Config(id string, seedByte byte, now time.Time) KeyConfig {
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = seedByte
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)
	return KeyConfig{
		ID:                 KeyID(id),
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: "authority:workorder:local",
		TrustDomain:        "local.example",
		Algorithm:          AlgorithmEdDSA,
		KeyType:            KeyTypeOKP,
		Curve:              CurveEd25519,
		PublicKey:          public,
		SigningNotBefore:   now.Add(-time.Hour),
		SigningNotAfter:    now.Add(24 * time.Hour),
		PolicyRevision:     "policy-1",
	}
}

func es256Config(id string, now time.Time) KeyConfig {
	x, y := elliptic.P256().ScalarBaseMult([]byte{1})
	return KeyConfig{
		ID:                 KeyID(id),
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: "authority:workorder:local",
		TrustDomain:        "local.example",
		Algorithm:          AlgorithmES256,
		KeyType:            KeyTypeEC2,
		Curve:              CurveP256,
		PublicKey: &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     x,
			Y:     y,
		},
		SigningNotBefore: now.Add(-time.Hour),
		SigningNotAfter:  now.Add(24 * time.Hour),
		PolicyRevision:   "policy-1",
	}
}
