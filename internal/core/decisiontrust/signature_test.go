package decisiontrust

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

func TestReferenceSoftwareSignerEdDSARoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-sign-ed", 1, now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	signer, err := NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatalf("NewReferenceSoftwareSigner() error = %v", err)
	}
	if err := signer.RegisterPrivateKey(key.ID(), testEd25519PrivateKey(1)); err != nil {
		t.Fatalf("RegisterPrivateKey() error = %v", err)
	}

	sigStructure := []byte("cose-signature1-ed25519")
	result, err := signer.SignCOSESigStructure(COSESigningRequest{
		Key:            signingRequestFor(key, authority.DecisionWorkOrder),
		PolicyRevision: key.PolicyRevision(),
		SigStructure:   sigStructure,
	}, now)
	if err != nil {
		t.Fatalf("SignCOSESigStructure() error = %v", err)
	}
	if result.KeyID != key.ID() || result.Algorithm != AlgorithmEdDSA {
		t.Fatalf("unexpected signature result: key=%q algorithm=%q", result.KeyID, result.Algorithm)
	}
	if len(result.Signature) != ed25519.SignatureSize {
		t.Fatalf("signature length = %d, want %d", len(result.Signature), ed25519.SignatureSize)
	}

	verifier := mustVerifierBoundaryForAlgorithms(t, registry, []Algorithm{AlgorithmEdDSA})
	got, err := verifier.VerifyCryptographicSignature(CryptographicVerificationRequest{
		PreCrypto:    validPreCryptoRequest(key, now),
		SigStructure: sigStructure,
		Signature:    result.Signature,
	})
	if err != nil {
		t.Fatalf("VerifyCryptographicSignature() error = %v", err)
	}
	if got.ID() != key.ID() {
		t.Fatalf("verified key = %q, want %q", got.ID(), key.ID())
	}
}

func TestReferenceSoftwareSignerES256RoundTripUsesRawFixedWidthSignature(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, es256Config("kid-sign-es256", now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	signer, err := NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatalf("NewReferenceSoftwareSigner() error = %v", err)
	}
	if err := signer.RegisterPrivateKey(key.ID(), testES256PrivateKey()); err != nil {
		t.Fatalf("RegisterPrivateKey() error = %v", err)
	}

	sigStructure := []byte("cose-signature1-es256")
	result, err := signer.SignCOSESigStructure(COSESigningRequest{
		Key:            signingRequestFor(key, authority.DecisionWorkOrder),
		PolicyRevision: key.PolicyRevision(),
		SigStructure:   sigStructure,
	}, now)
	if err != nil {
		t.Fatalf("SignCOSESigStructure() error = %v", err)
	}
	if result.Algorithm != AlgorithmES256 {
		t.Fatalf("algorithm = %q, want %q", result.Algorithm, AlgorithmES256)
	}
	if len(result.Signature) != 64 {
		t.Fatalf("COSE ES256 signature length = %d, want 64", len(result.Signature))
	}

	verifier := mustVerifierBoundaryForAlgorithms(t, registry, []Algorithm{AlgorithmES256})
	pre := validPreCryptoRequest(key, now)
	pre.Key.ProtectedAlgorithm = AlgorithmES256
	got, err := verifier.VerifyCryptographicSignature(CryptographicVerificationRequest{
		PreCrypto:    pre,
		SigStructure: sigStructure,
		Signature:    result.Signature,
	})
	if err != nil {
		t.Fatalf("VerifyCryptographicSignature() error = %v", err)
	}
	if got.ID() != key.ID() {
		t.Fatalf("verified key = %q, want %q", got.ID(), key.ID())
	}
}

func TestES256VerifierRejectsASN1DEREncoding(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, es256Config("kid-es256-der", now), now)
	registerActivate(t, registry, key, now)

	privateKey := testES256PrivateKey()
	sigStructure := []byte("cose-signature1-es256-der")
	digest := sha256.Sum256(sigStructure)
	der, err := ecdsa.SignASN1(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatalf("ecdsa.SignASN1() error = %v", err)
	}
	if len(der) == 64 {
		t.Fatal("unexpected DER signature length 64; test would not distinguish encoding")
	}

	verifier := mustVerifierBoundaryForAlgorithms(t, registry, []Algorithm{AlgorithmES256})
	pre := validPreCryptoRequest(key, now)
	pre.Key.ProtectedAlgorithm = AlgorithmES256
	_, err = verifier.VerifyCryptographicSignature(CryptographicVerificationRequest{
		PreCrypto:    pre,
		SigStructure: sigStructure,
		Signature:    der,
	})
	assertVerificationReason(t, err, ReasonSignatureInvalid)
}

func TestCryptographicVerifierRejectsMutatedSignature(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-mutated-signature", 1, now), now)
	registerActivate(t, registry, key, now)

	privateKey := testEd25519PrivateKey(1)
	sigStructure := []byte("cose-signature1-mutated")
	signature := ed25519.Sign(privateKey, sigStructure)
	signature[0] ^= 0xff

	verifier := mustVerifierBoundaryForAlgorithms(t, registry, []Algorithm{AlgorithmEdDSA})
	_, err := verifier.VerifyCryptographicSignature(CryptographicVerificationRequest{
		PreCrypto:    validPreCryptoRequest(key, now),
		SigStructure: sigStructure,
		Signature:    signature,
	})
	assertVerificationReason(t, err, ReasonSignatureInvalid)
}

func TestReferenceSoftwareSignerRejectsWrongPrivateKey(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-wrong-private", 1, now), now)
	if err := registry.Register(key, now); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	signer, err := NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatalf("NewReferenceSoftwareSigner() error = %v", err)
	}
	if err := signer.RegisterPrivateKey(key.ID(), testEd25519PrivateKey(2)); !errors.Is(err, ErrPrivateKeyMismatch) {
		t.Fatalf("RegisterPrivateKey(wrong) error = %v, want ErrPrivateKeyMismatch", err)
	}
}

func TestReferenceSoftwareSignerDefensivelyCopiesPrivateKey(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-private-copy", 1, now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	privateKey := testEd25519PrivateKey(1)
	signer, err := NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatalf("NewReferenceSoftwareSigner() error = %v", err)
	}
	if err := signer.RegisterPrivateKey(key.ID(), privateKey); err != nil {
		t.Fatalf("RegisterPrivateKey() error = %v", err)
	}
	for i := range privateKey {
		privateKey[i] = 0
	}

	sigStructure := []byte("cose-signature1-private-copy")
	result, err := signer.SignCOSESigStructure(COSESigningRequest{
		Key:            signingRequestFor(key, authority.DecisionWorkOrder),
		PolicyRevision: key.PolicyRevision(),
		SigStructure:   sigStructure,
	}, now)
	if err != nil {
		t.Fatalf("SignCOSESigStructure() error = %v", err)
	}

	public := key.PublicKey().(ed25519.PublicKey)
	if !ed25519.Verify(public, sigStructure, result.Signature) {
		t.Fatal("signature failed after caller mutated original private key")
	}
}

func TestReferenceSoftwareSignerRequiresMatchingPolicyRevision(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-policy-sign", 1, now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	signer, err := NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatalf("NewReferenceSoftwareSigner() error = %v", err)
	}
	if err := signer.RegisterPrivateKey(key.ID(), testEd25519PrivateKey(1)); err != nil {
		t.Fatalf("RegisterPrivateKey() error = %v", err)
	}

	_, err = signer.SignCOSESigStructure(COSESigningRequest{
		Key:            signingRequestFor(key, authority.DecisionWorkOrder),
		PolicyRevision: "policy-other",
		SigStructure:   []byte("cose-signature1-policy"),
	}, now)
	if !errors.Is(err, ErrSigningPolicyMismatch) {
		t.Fatalf("SignCOSESigStructure() error = %v, want ErrSigningPolicyMismatch", err)
	}
}

func TestReferenceSoftwareSignerDoesNotReturnSignatureAfterConcurrentRevocation(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, es256Config("kid-concurrent-revoke", now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	blocker := newBlockingRandomReader()
	signer, err := newReferenceSoftwareSigner(registry, blocker)
	if err != nil {
		t.Fatalf("newReferenceSoftwareSigner() error = %v", err)
	}
	if err := signer.RegisterPrivateKey(key.ID(), testES256PrivateKey()); err != nil {
		t.Fatalf("RegisterPrivateKey() error = %v", err)
	}

	type signingOutcome struct {
		result SignatureResult
		err    error
	}
	done := make(chan signingOutcome, 1)
	go func() {
		result, err := signer.SignCOSESigStructure(COSESigningRequest{
			Key:            signingRequestFor(key, authority.DecisionWorkOrder),
			PolicyRevision: key.PolicyRevision(),
			SigStructure:   []byte("cose-signature1-concurrent-revoke"),
		}, now)
		done <- signingOutcome{result: result, err: err}
	}()

	<-blocker.entered
	if err := registry.Revoke(key.ID(), now.Add(time.Nanosecond)); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	close(blocker.release)

	outcome := <-done
	if !errors.Is(outcome.err, ErrSigningAuthorizationChanged) {
		t.Fatalf("SignCOSESigStructure() error = %v, want ErrSigningAuthorizationChanged", outcome.err)
	}
	if len(outcome.result.Signature) != 0 {
		t.Fatalf("signature leaked after revocation: %d bytes", len(outcome.result.Signature))
	}
}

func TestCOSESigStructureSizeBoundIsEnforcedForSigningAndVerification(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-size-bound", 1, now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	signer, err := NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatalf("NewReferenceSoftwareSigner() error = %v", err)
	}
	if err := signer.RegisterPrivateKey(key.ID(), testEd25519PrivateKey(1)); err != nil {
		t.Fatalf("RegisterPrivateKey() error = %v", err)
	}

	oversized := make([]byte, MaxCOSESigStructureBytes+1)
	_, err = signer.SignCOSESigStructure(COSESigningRequest{
		Key:            signingRequestFor(key, authority.DecisionWorkOrder),
		PolicyRevision: key.PolicyRevision(),
		SigStructure:   oversized,
	}, now)
	if !errors.Is(err, ErrInvalidCOSESigStructure) {
		t.Fatalf("sign oversized error = %v, want ErrInvalidCOSESigStructure", err)
	}

	verifier := mustVerifierBoundaryForAlgorithms(t, registry, []Algorithm{AlgorithmEdDSA})
	_, err = verifier.VerifyCryptographicSignature(CryptographicVerificationRequest{
		PreCrypto:    validPreCryptoRequest(key, now),
		SigStructure: oversized,
		Signature:    make([]byte, ed25519.SignatureSize),
	})
	assertVerificationReason(t, err, ReasonMalformedCOSE)
}

func TestRegisterPrivateKeyRejectsDuplicateRegistration(t *testing.T) {
	now := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-private-duplicate", 1, now), now)
	if err := registry.Register(key, now); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	signer, err := NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatalf("NewReferenceSoftwareSigner() error = %v", err)
	}
	privateKey := testEd25519PrivateKey(1)
	if err := signer.RegisterPrivateKey(key.ID(), privateKey); err != nil {
		t.Fatalf("RegisterPrivateKey(first) error = %v", err)
	}
	if err := signer.RegisterPrivateKey(key.ID(), privateKey); !errors.Is(err, ErrPrivateKeyAlreadyRegistered) {
		t.Fatalf("RegisterPrivateKey(duplicate) error = %v, want ErrPrivateKeyAlreadyRegistered", err)
	}
}

func mustVerifierBoundaryForAlgorithms(t testing.TB, registry *Registry, algorithms []Algorithm) *VerifierBoundary {
	t.Helper()
	policy, err := NewVerificationPolicy(VerificationPolicyConfig{
		MaxClockSkew:            30 * time.Second,
		MaxTrustBundleStaleness: 30 * time.Minute,
		AllowedAlgorithms:       algorithms,
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionWorkOrder: 10 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("NewVerificationPolicy() error = %v", err)
	}
	verifier, err := NewVerifierBoundary(registry, policy)
	if err != nil {
		t.Fatalf("NewVerifierBoundary() error = %v", err)
	}
	return verifier
}

func testEd25519PrivateKey(seedByte byte) ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = seedByte
	return ed25519.NewKeyFromSeed(seed)
}

func testES256PrivateKey() *ecdsa.PrivateKey {
	p256 := elliptic.P256()
	d := big.NewInt(1)
	x, y := p256.ScalarBaseMult(d.Bytes())
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: p256,
			X:     x,
			Y:     y,
		},
		D: d,
	}
}

type blockingRandomReader struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func newBlockingRandomReader() *blockingRandomReader {
	return &blockingRandomReader{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingRandomReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	return rand.Reader.Read(p)
}

func assertVerificationReason(t testing.TB, err error, want VerificationReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected verification failure %q", want)
	}
	got, ok := FailureReason(err)
	if !ok {
		t.Fatalf("error %v does not carry VerificationFailure", err)
	}
	if got != want {
		t.Fatalf("verification reason = %q, want %q (err=%v)", got, want, err)
	}
}
