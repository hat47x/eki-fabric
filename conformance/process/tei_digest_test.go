package process

import (
	"crypto/sha256"
	"encoding/hex"
)

// digestOf is the SHA-256 hex of bytes, the same digest EKI names an artifact
// by. The tests use it to re-hash a body they fetched, so the comparison is
// against what the bytes are and not what a header said.
func digestOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
