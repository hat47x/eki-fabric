package trustbundle

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ErrBadSignature is returned when a signed bundle was altered, was signed by a
// key other than the one offered, or does not hold a signature at all.
var ErrBadSignature = errors.New("trustbundle: signature does not verify")

// ErrUnknownKey identifies a signature chain that cannot be followed: the
// signed bundle names a key the verifier was not given.
var ErrUnknownKey = errors.New("trustbundle: signing key is not trusted for this version")

// Signed is a bundle with the signature that covers its digest.
type Signed struct {
	Bundle Bundle
	// KeyID names the Control Plane key that produced the signature, so the
	// receiver can pick the right public key out of its own version.
	KeyID     string
	Signature []byte
}

// Sign covers the bundle's digest with the Control Plane's key. The bundle must
// be well formed: signing an invalid bundle would launder it into an accepted
// one.
func Sign(b Bundle, keyID string, key ed25519.PrivateKey) (Signed, error) {
	if err := b.Validate(); err != nil {
		return Signed{}, err
	}
	if keyID == "" {
		return Signed{}, fmt.Errorf("%w: signature has no key id", ErrInvalidBundle)
	}
	if len(key) != ed25519.PrivateKeySize {
		return Signed{}, fmt.Errorf("%w: signing key has the wrong size", ErrInvalidBundle)
	}
	digest := b.Digest()
	return Signed{Bundle: b, KeyID: keyID, Signature: ed25519.Sign(key, []byte(digest.String()))}, nil
}

// Verify checks the signature and the bundle. It does not decide whether the
// bundle is current: use Signed.Accept for that.
func (s Signed) Verify(key ed25519.PublicKey) error {
	if s.KeyID == "" || len(s.Signature) == 0 {
		return fmt.Errorf("%w: no key id or signature", ErrBadSignature)
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: verification key has the wrong size", ErrBadSignature)
	}
	if err := s.Bundle.Validate(); err != nil {
		return err
	}
	digest := s.Bundle.Digest()
	if !ed25519.Verify(key, []byte(digest.String()), s.Signature) {
		return fmt.Errorf("%w: bundle %s", ErrBadSignature, digest)
	}
	return nil
}

// Accept verifies the signature and the bundle's own window at now. A Worker
// calls this on every bundle it receives before using it.
func (s Signed) Accept(key ed25519.PublicKey, now time.Time) error {
	if err := s.Verify(key); err != nil {
		return err
	}
	return s.Bundle.Current(now)
}

// KeyDigest is the digest of the public key that must cover this version, so a
// bundle can carry the expectation of its successor's signature.
func KeyDigest(key ed25519.PublicKey) contract.Digest {
	return contract.SHA256(key)
}
