package decisiontrust

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

const MaxCOSESigStructureBytes = 272 * 1024

var (
	ErrInvalidSigningRequest       = errors.New("invalid signed-decision signing request")
	ErrInvalidCOSESigStructure     = errors.New("invalid COSE Sig_structure")
	ErrNilSigningRegistry          = errors.New("signed-decision signer requires a registry")
	ErrPrivateKeyNotFound          = errors.New("signed-decision private key not found")
	ErrPrivateKeyAlreadyRegistered = errors.New("signed-decision private key already registered")
	ErrPrivateKeyMismatch          = errors.New("signed-decision private key does not match key record")
	ErrSigningPolicyMismatch       = errors.New("signed-decision signing policy revision mismatch")
	ErrSigningAuthorizationChanged = errors.New("signed-decision signing authorization changed during signing")
	ErrSignatureGeneration         = errors.New("signed-decision signature generation failed")
	ErrVerificationKeyInvalid      = errors.New("signed-decision verification key is invalid")
)

type COSESigningRequest struct {
	Key            SigningRequest
	PolicyRevision string
	SigStructure   []byte
}

func (r COSESigningRequest) Validate() error {
	if err := r.Key.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSigningRequest, err)
	}
	if err := validateText("policy revision", r.PolicyRevision, 128); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSigningRequest, err)
	}
	if err := validateCOSESigStructure(r.SigStructure); err != nil {
		return err
	}
	return nil
}

type SignatureResult struct {
	KeyID     KeyID
	Algorithm Algorithm
	Signature []byte
}

type ReferenceSoftwareSigner struct {
	registry *Registry
	// beforeSign is a test seam called after the key is resolved and before the
	// signature is generated. It is nil outside tests. An injected random
	// source cannot serve this purpose: since Go 1.26 crypto/ecdsa always uses
	// the system CSPRNG and ignores the reader it is given.
	beforeSign func()

	mu          sync.RWMutex
	privateKeys map[KeyID]crypto.PrivateKey
}

func NewReferenceSoftwareSigner(registry *Registry) (*ReferenceSoftwareSigner, error) {
	return newReferenceSoftwareSigner(registry, nil)
}

func newReferenceSoftwareSigner(registry *Registry, beforeSign func()) (*ReferenceSoftwareSigner, error) {
	if registry == nil {
		return nil, ErrNilSigningRegistry
	}
	return &ReferenceSoftwareSigner{
		registry:    registry,
		beforeSign:  beforeSign,
		privateKeys: make(map[KeyID]crypto.PrivateKey),
	}, nil
}

func (s *ReferenceSoftwareSigner) RegisterPrivateKey(id KeyID, privateKey crypto.PrivateKey) error {
	record, err := s.registry.Get(id)
	if err != nil {
		return err
	}
	cloned, err := cloneAndValidatePrivateKey(record, privateKey)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.privateKeys[id]; exists {
		return fmt.Errorf("%w: key %q", ErrPrivateKeyAlreadyRegistered, id)
	}
	s.privateKeys[id] = cloned
	return nil
}

func (s *ReferenceSoftwareSigner) SignCOSESigStructure(req COSESigningRequest, now time.Time) (SignatureResult, error) {
	if now.IsZero() {
		return SignatureResult{}, fmt.Errorf("%w: current time is required", ErrInvalidSigningRequest)
	}
	if err := req.Validate(); err != nil {
		return SignatureResult{}, err
	}

	record, err := s.registry.ResolveSigningKey(req.Key, now)
	if err != nil {
		return SignatureResult{}, err
	}
	if record.PolicyRevision() != req.PolicyRevision {
		return SignatureResult{}, fmt.Errorf(
			"%w: request=%q key=%q",
			ErrSigningPolicyMismatch,
			req.PolicyRevision,
			record.PolicyRevision(),
		)
	}

	privateKey, err := s.privateKey(record.ID())
	if err != nil {
		return SignatureResult{}, err
	}

	if s.beforeSign != nil {
		s.beforeSign()
	}
	signature, err := signCOSESigStructure(record, privateKey, req.SigStructure)
	if err != nil {
		return SignatureResult{}, err
	}

	current, err := s.registry.ResolveSigningKey(req.Key, now)
	if err != nil ||
		current.ID() != record.ID() ||
		current.Revision() != record.Revision() ||
		current.PolicyRevision() != record.PolicyRevision() {
		return SignatureResult{}, ErrSigningAuthorizationChanged
	}

	return SignatureResult{
		KeyID:     record.ID(),
		Algorithm: record.Algorithm(),
		Signature: append([]byte(nil), signature...),
	}, nil
}

func (s *ReferenceSoftwareSigner) privateKey(id KeyID) (crypto.PrivateKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := s.privateKeys[id]
	if !ok {
		return nil, fmt.Errorf("%w: key %q", ErrPrivateKeyNotFound, id)
	}
	return key, nil
}

type CryptographicVerificationRequest struct {
	PreCrypto    PreCryptoVerificationRequest
	SigStructure []byte
	Signature    []byte
}

func (v *VerifierBoundary) VerifyCryptographicSignature(req CryptographicVerificationRequest) (KeyRecord, error) {
	if err := validateCOSESigStructure(req.SigStructure); err != nil {
		return KeyRecord{}, failure(ReasonMalformedCOSE, err)
	}
	record, err := v.ResolvePreCrypto(req.PreCrypto)
	if err != nil {
		return KeyRecord{}, err
	}
	if err := verifyCOSESigStructure(
		record,
		req.PreCrypto.Key.ProtectedAlgorithm,
		req.SigStructure,
		req.Signature,
	); err != nil {
		return KeyRecord{}, err
	}

	// Re-run the pre-crypto gate after signature verification so a key that is
	// revoked or compromised while crypto work is in progress is not accepted
	// solely because it was valid at the first lookup. This narrows the TOCTOU
	// window; execution authorization must still validate current state again.
	current, err := v.ResolvePreCrypto(req.PreCrypto)
	if err != nil {
		return KeyRecord{}, err
	}
	return current, nil
}

func validateCOSESigStructure(value []byte) error {
	if len(value) == 0 {
		return fmt.Errorf("%w: empty Sig_structure", ErrInvalidCOSESigStructure)
	}
	if len(value) > MaxCOSESigStructureBytes {
		return fmt.Errorf(
			"%w: %d bytes exceeds %d-byte limit",
			ErrInvalidCOSESigStructure,
			len(value),
			MaxCOSESigStructureBytes,
		)
	}
	return nil
}

func cloneAndValidatePrivateKey(record KeyRecord, privateKey crypto.PrivateKey) (crypto.PrivateKey, error) {
	switch record.Algorithm() {
	case AlgorithmEdDSA:
		key, ok := privateKey.(ed25519.PrivateKey)
		if !ok || len(key) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("%w: EdDSA requires Ed25519 private key", ErrPrivateKeyMismatch)
		}
		public, ok := key.Public().(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: Ed25519 public key unavailable", ErrPrivateKeyMismatch)
		}
		expected, ok := record.PublicKey().(ed25519.PublicKey)
		if !ok || !bytes.Equal(public, expected) {
			return nil, fmt.Errorf("%w: Ed25519 public key mismatch", ErrPrivateKeyMismatch)
		}
		return append(ed25519.PrivateKey(nil), key...), nil

	case AlgorithmES256:
		key, ok := privateKey.(*ecdsa.PrivateKey)
		if !ok || key == nil || key.D == nil || key.PublicKey.X == nil || key.PublicKey.Y == nil || key.PublicKey.Curve == nil {
			return nil, fmt.Errorf("%w: ES256 requires P-256 ECDSA private key", ErrPrivateKeyMismatch)
		}
		p256 := elliptic.P256()
		if key.PublicKey.Curve != p256 ||
			key.D.Sign() <= 0 ||
			key.D.Cmp(p256.Params().N) >= 0 ||
			!p256.IsOnCurve(key.PublicKey.X, key.PublicKey.Y) {
			return nil, fmt.Errorf("%w: invalid P-256 private key", ErrPrivateKeyMismatch)
		}
		x, y := p256.ScalarBaseMult(key.D.Bytes())
		if x.Cmp(key.PublicKey.X) != 0 || y.Cmp(key.PublicKey.Y) != 0 {
			return nil, fmt.Errorf("%w: ECDSA private/public key mismatch", ErrPrivateKeyMismatch)
		}
		expected, ok := record.PublicKey().(*ecdsa.PublicKey)
		if !ok ||
			expected == nil ||
			expected.Curve != p256 ||
			expected.X.Cmp(key.PublicKey.X) != 0 ||
			expected.Y.Cmp(key.PublicKey.Y) != 0 {
			return nil, fmt.Errorf("%w: ES256 public key mismatch", ErrPrivateKeyMismatch)
		}
		return &ecdsa.PrivateKey{
			PublicKey: ecdsa.PublicKey{
				Curve: p256,
				X:     new(big.Int).Set(key.PublicKey.X),
				Y:     new(big.Int).Set(key.PublicKey.Y),
			},
			D: new(big.Int).Set(key.D),
		}, nil

	default:
		return nil, fmt.Errorf("%w: unsupported algorithm %q", ErrPrivateKeyMismatch, record.Algorithm())
	}
}

func signCOSESigStructure(record KeyRecord, privateKey crypto.PrivateKey, sigStructure []byte) ([]byte, error) {
	switch record.Algorithm() {
	case AlgorithmEdDSA:
		key, ok := privateKey.(ed25519.PrivateKey)
		if !ok || len(key) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("%w: invalid Ed25519 private key", ErrSignatureGeneration)
		}
		return ed25519.Sign(key, sigStructure), nil

	case AlgorithmES256:
		key, ok := privateKey.(*ecdsa.PrivateKey)
		if !ok || key == nil {
			return nil, fmt.Errorf("%w: invalid P-256 private key", ErrSignatureGeneration)
		}
		digest := sha256.Sum256(sigStructure)
		r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSignatureGeneration, err)
		}
		signature := make([]byte, 64)
		r.FillBytes(signature[:32])
		s.FillBytes(signature[32:])
		return signature, nil

	default:
		return nil, fmt.Errorf("%w: unsupported algorithm %q", ErrSignatureGeneration, record.Algorithm())
	}
}

func verifyCOSESigStructure(record KeyRecord, protectedAlgorithm Algorithm, sigStructure, signature []byte) error {
	if record.Algorithm() != protectedAlgorithm {
		return failure(
			ReasonKeyAlgorithmMismatch,
			fmt.Errorf(
				"%w: protected=%q key=%q",
				ErrAlgorithmMismatch,
				protectedAlgorithm,
				record.Algorithm(),
			),
		)
	}

	switch protectedAlgorithm {
	case AlgorithmEdDSA:
		public, ok := record.PublicKey().(ed25519.PublicKey)
		if !ok || len(public) != ed25519.PublicKeySize {
			return failure(ReasonKeyAlgorithmMismatch, ErrVerificationKeyInvalid)
		}
		if len(signature) != ed25519.SignatureSize || !ed25519.Verify(public, sigStructure, signature) {
			return failure(ReasonSignatureInvalid, errors.New("Ed25519 signature verification failed"))
		}
		return nil

	case AlgorithmES256:
		public, ok := record.PublicKey().(*ecdsa.PublicKey)
		if !ok || public == nil || public.Curve != elliptic.P256() {
			return failure(ReasonKeyAlgorithmMismatch, ErrVerificationKeyInvalid)
		}
		if len(signature) != 64 {
			return failure(ReasonSignatureInvalid, fmt.Errorf("ES256 signature must be 64 bytes, got %d", len(signature)))
		}
		r := new(big.Int).SetBytes(signature[:32])
		s := new(big.Int).SetBytes(signature[32:])
		order := elliptic.P256().Params().N
		if r.Sign() <= 0 || s.Sign() <= 0 || r.Cmp(order) >= 0 || s.Cmp(order) >= 0 {
			return failure(ReasonSignatureInvalid, errors.New("ES256 signature scalar out of range"))
		}
		digest := sha256.Sum256(sigStructure)
		if !ecdsa.Verify(public, digest[:], r, s) {
			return failure(ReasonSignatureInvalid, errors.New("ES256 signature verification failed"))
		}
		return nil

	default:
		return failure(
			ReasonAlgorithmNotAllowed,
			fmt.Errorf("unsupported protected algorithm %q", protectedAlgorithm),
		)
	}
}
