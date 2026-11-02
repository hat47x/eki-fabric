package trustbundle

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"
)

// ErrEmptyChain is returned when there is nothing to verify. An absent chain is
// not the same as a chain that verifies.
var ErrEmptyChain = errors.New("trustbundle: chain has no bundle")

// Keyring looks up the public key a signed bundle names. A Worker keeps one for
// the keys it is willing to follow; migration adds keys, revocation removes
// them (ADR-0036 §2.2).
type Keyring interface {
	PublicKey(keyID string) (ed25519.PublicKey, bool)
}

// KeyringFunc adapts a function to Keyring.
type KeyringFunc func(keyID string) (ed25519.PublicKey, bool)

// PublicKey implements Keyring.
func (f KeyringFunc) PublicKey(keyID string) (ed25519.PublicKey, bool) { return f(keyID) }

// VerifyChain checks a run of signed bundles as a Worker would: every bundle
// follows the one before it, every signature verifies with the key it names,
// and the last bundle is current at now. Earlier versions may be expired; only
// the last one has to be usable.
func VerifyChain(chain []Signed, keys Keyring, now time.Time) error {
	if len(chain) == 0 {
		return ErrEmptyChain
	}
	if keys == nil {
		return fmt.Errorf("%w: no keyring", ErrUnknownKey)
	}
	for i, signed := range chain {
		key, ok := keys.PublicKey(signed.KeyID)
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownKey, signed.KeyID)
		}
		if err := signed.Verify(key); err != nil {
			return err
		}
		if i == 0 {
			continue
		}
		if err := signed.Bundle.Follows(chain[i-1].Bundle); err != nil {
			return err
		}
	}
	return chain[len(chain)-1].Bundle.Current(now)
}
