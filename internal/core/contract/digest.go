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
	sum := sha256.Sum256(data)
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
