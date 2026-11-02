package enroll

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidControlKey is returned for a Control Plane key that cannot be read
// as an Ed25519 public key.
var ErrInvalidControlKey = errors.New("invalid control plane public key")

// ParseControlKey reads the Control Plane's signing key from its out-of-band
// form (ADR-0036 §5). Two spellings are accepted because both are what an
// operator can copy from the Control Plane: 64 hex characters, or the standard
// base64 of the same 32 bytes. A key with any other length is refused rather
// than padded: a wrong key must not look like a usable one.
func ParseControlKey(value string) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidControlKey)
	}
	var (
		raw []byte
		err error
	)
	if len(trimmed) == hex.EncodedLen(ed25519.PublicKeySize) {
		raw, err = hex.DecodeString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("%w: not hex", ErrInvalidControlKey)
		}
	} else {
		raw, err = base64.StdEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("%w: not base64", ErrInvalidControlKey)
		}
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrInvalidControlKey, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
