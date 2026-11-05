package workerregistry

import (
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// MaxHeldArtifacts is how many artifact digests a Worker may claim to hold. A
// Worker that has fetched more than this is not acting as a cache for the Tasks it
// runs, and a longer list would let one Worker occupy the placement hint for
// everybody else's inputs.
const MaxHeldArtifacts = 64

// ErrHeldArtifacts means a claim about held artifacts is not usable. It is a
// refusal of the claim, never of the Worker: the claim is only a hint, so a bad one
// is dropped rather than allowed to matter (ADR-0029 §5).
var ErrHeldArtifacts = errors.New("workerregistry: unusable held-artifacts claim")

// HeldArtifacts is what a Worker says it already has, by digest.
//
// It is a claim: EKI never checks that the bytes are there, and it never uses the
// claim to decide where declared-private data goes. Unknown or malformed claims are
// dropped by Validate rather than kept in a shortened form, because a hint that is
// silently wrong is worse than no hint.
type HeldArtifacts []contract.Digest

// Validate reports whether the claim is usable as a hint.
//
// Empty is valid: a Worker that says nothing is treated like one that holds
// nothing, which is the same outcome for placement.
func (h HeldArtifacts) Validate() error {
	if len(h) > MaxHeldArtifacts {
		return fmt.Errorf("%w: %d digests, at most %d", ErrHeldArtifacts, len(h), MaxHeldArtifacts)
	}
	for i, digest := range h {
		if digest.IsZero() {
			return fmt.Errorf("%w: entry %d is empty", ErrHeldArtifacts, i)
		}
	}
	return nil
}

// Holds reports whether the claim includes the digest. A claim that does not
// validate holds nothing: EKI does not half-trust a broken list.
func (h HeldArtifacts) Holds(digest contract.Digest) bool {
	if h.Validate() != nil {
		return false
	}
	for _, held := range h {
		if held.Hex() == digest.Hex() {
			return true
		}
	}
	return false
}
