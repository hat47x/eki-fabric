package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

var ErrInvalidDigest = errors.New("invalid digest")

type Digest struct {
	value [sha256.Size]byte
	set   bool
}

func SHA256(data []byte) Digest {
	return FromSHA256(sha256.Sum256(data))
}

// FromSHA256 wraps a digest that is already computed. It exists for values that are
// a SHA-256 by definition, such as a certificate fingerprint: passing those bytes
// through SHA256 would hash the digest again and print a value no other tool agrees
// with (#466).
func FromSHA256(sum [sha256.Size]byte) Digest {
	return Digest{value: sum, set: true}
}

func ParseSHA256(value string) (Digest, error) {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return Digest{}, fmt.Errorf("%w: expected %d hexadecimal bytes", ErrInvalidDigest, sha256.Size)
	}

	var sum [sha256.Size]byte
	copy(sum[:], raw)
	return Digest{value: sum, set: true}, nil
}

func (d Digest) Algorithm() string {
	if !d.set {
		return ""
	}
	return "sha256"
}

func (d Digest) Hex() string {
	if !d.set {
		return ""
	}
	return hex.EncodeToString(d.value[:])
}

func (d Digest) String() string {
	if !d.set {
		return ""
	}
	return "sha256:" + d.Hex()
}

func (d Digest) IsZero() bool {
	return !d.set
}

// MarshalJSON encodes the Digest as a JSON string holding its hex representation.
// A zero Digest is encoded as null.
func (d Digest) MarshalJSON() ([]byte, error) {
	if !d.set {
		return []byte("null"), nil
	}
	return []byte(`"` + d.Hex() + `"`), nil
}

// UnmarshalJSON decodes a JSON null or hex string into the Digest.
func (d *Digest) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		d.set = false
		d.value = [sha256.Size]byte{}
		return nil
	}
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return fmt.Errorf("%w: expected a JSON string or null", ErrInvalidDigest)
	}
	parsed, err := ParseSHA256(string(data[1 : len(data)-1]))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
