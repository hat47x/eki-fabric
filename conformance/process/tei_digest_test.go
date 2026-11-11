package process

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func digestOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// digestOf2 is kept separate so the helper file has no unused imports when a
// test is skipped on Windows.
var _ = time.Now
var _ contract.Digest
var _ = testing.Short
