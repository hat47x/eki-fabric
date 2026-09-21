package decisiontrust

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"errors"
	"fmt"
	"math/big"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

var (
	ErrInvalidKeyRecord = errors.New("invalid signed-decision key record")
)

type KeyID string

type Algorithm string

const (
	AlgorithmEdDSA Algorithm = "EdDSA"
	AlgorithmES256 Algorithm = "ES256"
)

func (a Algorithm) COSEIdentifier() (int64, bool) {
	switch a {
	case AlgorithmEdDSA:
		return -8, true
	case AlgorithmES256:
		return -7, true
	default:
		return 0, false
	}
}

type KeyType string

const (
	KeyTypeOKP KeyType = "OKP"
	KeyTypeEC2 KeyType = "EC2"
)

type Curve string

const (
	CurveEd25519 Curve = "Ed25519"
	CurveP256    Curve = "P-256"
)

type KeyState string

const (
	KeyStatePending     KeyState = "PENDING"
	KeyStateActive      KeyState = "ACTIVE"
	KeyStateVerifyOnly  KeyState = "VERIFY_ONLY"
	KeyStateRevoked     KeyState = "REVOKED"
	KeyStateCompromised KeyState = "COMPROMISED"
)

type KeyConfig struct {
	ID                 KeyID
	AuthorityRole      authority.Role
	AuthorityPrincipal string
	TrustDomain        string
	Algorithm          Algorithm
	KeyType            KeyType
	Curve              Curve
	PublicKey          crypto.PublicKey
	SigningNotBefore   time.Time
	SigningNotAfter    time.Time
	PolicyRevision     string
}

type KeyRecord struct {
	id                 KeyID
	authorityRole      authority.Role
	authorityPrincipal string
	trustDomain        string
	algorithm          Algorithm
	keyType            KeyType
	curve              Curve
	publicKey          crypto.PublicKey
	publicKeyDigest    contract.Digest
	state              KeyState
	activatedAt        time.Time
	signingNotBefore   time.Time
	signingNotAfter    time.Time
	policyRevision     string
	revision           uint64
	stateChangedAt     time.Time
}

func NewKeyRecord(cfg KeyConfig, now time.Time) (KeyRecord, error) {
	if err := validateText("key ID", string(cfg.ID), 128); err != nil {
		return KeyRecord{}, err
	}
	if !cfg.AuthorityRole.Valid() {
		return KeyRecord{}, fmt.Errorf("%w: invalid authority role %q", ErrInvalidKeyRecord, cfg.AuthorityRole)
	}
	if err := validateText("authority principal", cfg.AuthorityPrincipal, 256); err != nil {
		return KeyRecord{}, err
	}
	if err := validateText("trust domain", cfg.TrustDomain, 256); err != nil {
		return KeyRecord{}, err
	}
	if err := validateText("policy revision", cfg.PolicyRevision, 128); err != nil {
		return KeyRecord{}, err
	}
	if cfg.SigningNotBefore.IsZero() || cfg.SigningNotAfter.IsZero() || !cfg.SigningNotBefore.Before(cfg.SigningNotAfter) {
		return KeyRecord{}, fmt.Errorf("%w: invalid signing validity window", ErrInvalidKeyRecord)
	}

	keyBytes, keyCopy, err := validateAndCanonicalizePublicKey(cfg)
	if err != nil {
		return KeyRecord{}, err
	}
	now = now.UTC()
	return KeyRecord{
		id:                 cfg.ID,
		authorityRole:      cfg.AuthorityRole,
		authorityPrincipal: cfg.AuthorityPrincipal,
		trustDomain:        cfg.TrustDomain,
		algorithm:          cfg.Algorithm,
		keyType:            cfg.KeyType,
		curve:              cfg.Curve,
		publicKey:          keyCopy,
		publicKeyDigest:    contract.SHA256(keyBytes),
		state:              KeyStatePending,
		signingNotBefore:   cfg.SigningNotBefore.UTC(),
		signingNotAfter:    cfg.SigningNotAfter.UTC(),
		policyRevision:     cfg.PolicyRevision,
		revision:           1,
		stateChangedAt:     now,
	}, nil
}

func validateAndCanonicalizePublicKey(cfg KeyConfig) ([]byte, crypto.PublicKey, error) {
	switch cfg.Algorithm {
	case AlgorithmEdDSA:
		if cfg.KeyType != KeyTypeOKP || cfg.Curve != CurveEd25519 {
			return nil, nil, fmt.Errorf("%w: Ed25519 requires OKP / Ed25519", ErrInvalidKeyRecord)
		}
		key, ok := cfg.PublicKey.(ed25519.PublicKey)
		if !ok || len(key) != ed25519.PublicKeySize {
			return nil, nil, fmt.Errorf("%w: invalid Ed25519 public key", ErrInvalidKeyRecord)
		}
		copyKey := append(ed25519.PublicKey(nil), key...)
		return append([]byte(nil), copyKey...), copyKey, nil

	case AlgorithmES256:
		if cfg.KeyType != KeyTypeEC2 || cfg.Curve != CurveP256 {
			return nil, nil, fmt.Errorf("%w: ES256 requires EC2 / P-256", ErrInvalidKeyRecord)
		}
		key, ok := cfg.PublicKey.(*ecdsa.PublicKey)
		if !ok || key == nil || key.X == nil || key.Y == nil || key.Curve == nil {
			return nil, nil, fmt.Errorf("%w: invalid ES256 public key", ErrInvalidKeyRecord)
		}
		p256 := elliptic.P256()
		if key.Curve != p256 {
			return nil, nil, fmt.Errorf("%w: ES256 public key curve metadata is not P-256", ErrInvalidKeyRecord)
		}
		if !p256.IsOnCurve(key.X, key.Y) {
			return nil, nil, fmt.Errorf("%w: ES256 public key is not on P-256", ErrInvalidKeyRecord)
		}
		copyKey := &ecdsa.PublicKey{
			Curve: p256,
			X:     new(big.Int).Set(key.X),
			Y:     new(big.Int).Set(key.Y),
		}
		return elliptic.Marshal(p256, copyKey.X, copyKey.Y), copyKey, nil

	default:
		return nil, nil, fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidKeyRecord, cfg.Algorithm)
	}
}

func validateText(name, value string, maxBytes int) error {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return fmt.Errorf("%w: invalid %s", ErrInvalidKeyRecord, name)
	}
	return nil
}

func (r KeyRecord) sameRegistration(other KeyRecord) bool {
	return r.id == other.id &&
		r.authorityRole == other.authorityRole &&
		r.authorityPrincipal == other.authorityPrincipal &&
		r.trustDomain == other.trustDomain &&
		r.algorithm == other.algorithm &&
		r.keyType == other.keyType &&
		r.curve == other.curve &&
		r.publicKeyDigest == other.publicKeyDigest &&
		r.signingNotBefore.Equal(other.signingNotBefore) &&
		r.signingNotAfter.Equal(other.signingNotAfter) &&
		r.policyRevision == other.policyRevision
}

func (r KeyRecord) ID() KeyID                       { return r.id }
func (r KeyRecord) AuthorityRole() authority.Role   { return r.authorityRole }
func (r KeyRecord) AuthorityPrincipal() string      { return r.authorityPrincipal }
func (r KeyRecord) TrustDomain() string             { return r.trustDomain }
func (r KeyRecord) Algorithm() Algorithm            { return r.algorithm }
func (r KeyRecord) KeyType() KeyType                { return r.keyType }
func (r KeyRecord) Curve() Curve                    { return r.curve }
func (r KeyRecord) PublicKeyDigest() contract.Digest { return r.publicKeyDigest }
func (r KeyRecord) State() KeyState                 { return r.state }
func (r KeyRecord) ActivatedAt() time.Time           { return r.activatedAt }
func (r KeyRecord) SigningNotBefore() time.Time     { return r.signingNotBefore }
func (r KeyRecord) SigningNotAfter() time.Time      { return r.signingNotAfter }
func (r KeyRecord) PolicyRevision() string          { return r.policyRevision }
func (r KeyRecord) Revision() uint64                { return r.revision }
func (r KeyRecord) StateChangedAt() time.Time       { return r.stateChangedAt }

func (r KeyRecord) PublicKey() crypto.PublicKey {
	return clonePublicKey(r.publicKey)
}

func (r KeyRecord) scope() signingScope {
	return signingScope{
		TrustDomain:        r.trustDomain,
		AuthorityRole:      r.authorityRole,
		AuthorityPrincipal: r.authorityPrincipal,
		Algorithm:          r.algorithm,
	}
}

func clonePublicKey(key crypto.PublicKey) crypto.PublicKey {
	switch v := key.(type) {
	case ed25519.PublicKey:
		return append(ed25519.PublicKey(nil), v...)
	case *ecdsa.PublicKey:
		if v == nil {
			return (*ecdsa.PublicKey)(nil)
		}
		return &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).Set(v.X),
			Y:     new(big.Int).Set(v.Y),
		}
	default:
		return nil
	}
}
