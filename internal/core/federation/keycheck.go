package federation

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"strings"
)

// smallOrderKeyHex lists the canonical encodings of the eight points of Ed25519
// whose order divides 8. These points form the whole torsion subgroup, so no
// other curve point has small order. The first entry is the identity point.
//
// Why these keys must be refused: Ed25519 verification checks that the signature
// point R equals [S]B - [k]A, where A is the public key. For the identity point,
// [k]A is the identity whatever the message, so the check reduces to R = [S]B.
// A signature with R = identity and S = 0 then verifies for every message, with
// no private key. The other points of small order weaken the check in the same
// way. The Go standard library accepts all of these keys, so this package must
// refuse them.
//
// The test suite checks that the table is complete. It enumerates the torsion
// subgroup independently and compares the result with this table.
var smallOrderKeyHex = [8]string{
	// The identity point, y = 1.
	"01" + strings.Repeat("00", 31),
	// The point of order 2, y = -1.
	"ec" + strings.Repeat("ff", 30) + "7f",
	// The two points of order 4, y = 0, with the sign bit clear and set.
	strings.Repeat("00", 32),
	strings.Repeat("00", 31) + "80",
	// The four points of order 8.
	"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
	"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac03fa",
	"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
	"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc85",
}

// smallOrderKeys holds the decoded table. A malformed entry is a programming
// error in this file, and the package tests catch it.
var smallOrderKeys = decodeSmallOrderKeys()

// fieldPrime is p = 2^255 - 19 in little-endian order, the modulus of the
// Ed25519 field. A y-coordinate at or above p has a non-canonical encoding.
var fieldPrime = [ed25519.PublicKeySize]byte{
	0xed, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f,
}

func decodeSmallOrderKeys() [8][]byte {
	var out [8][]byte
	for i, s := range smallOrderKeyHex {
		b, err := hex.DecodeString(s)
		if err != nil || len(b) != ed25519.PublicKeySize {
			panic("federation: malformed small-order key table entry")
		}
		out[i] = b
	}
	return out
}

// usableSigningKey reports whether key may anchor signatures in this package.
// It refuses keys of the wrong length, keys whose y-coordinate is not canonical
// (RFC 8032 requires a decoder to reject these), and the small-order points
// listed above. Other keys that are not curve points fail verification, so
// they do not need a separate check here.
func usableSigningKey(key []byte) bool {
	if len(key) != ed25519.PublicKeySize {
		return false
	}
	if nonCanonicalY(key) {
		return false
	}
	for _, bad := range smallOrderKeys {
		if bytes.Equal(key, bad) {
			return false
		}
	}
	return true
}

// nonCanonicalY reports whether the y-coordinate in key, with the sign bit
// removed, is at least p. Such a value is an alias of a smaller one, so it
// would decode to a point that a canonical key already names.
func nonCanonicalY(key []byte) bool {
	var y [ed25519.PublicKeySize]byte
	copy(y[:], key)
	y[31] &= 0x7f
	for i := len(y) - 1; i >= 0; i-- {
		if y[i] != fieldPrime[i] {
			return y[i] > fieldPrime[i]
		}
	}
	return true
}
