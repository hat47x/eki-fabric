package statestore

import (
	"context"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

// ResultClaimAcceptance is the atomic result of accepting one claim.
//
// Record and ApplyTransition may be combined in one durable commit: a claim
// must not be stored without the Attempt transition it justifies, and an
// Attempt must not advance to Verification without the claim that caused it.
type ResultClaimAcceptance struct {
	Claim resultclaim.Claim
	// Record is true only for a new claim. An exact replay uses false.
	Record bool
	// ApplyTransition is true when NextAttempt must replace the stored Attempt.
	ApplyTransition bool
	// ExpectedAttemptRevision is the revision the caller read.
	ExpectedAttemptRevision uint64
	NextAttempt             attemptstate.AttemptRecord
}

// ResultClaimStore records ResultClaims and applies the matching Attempt
// transition. ApplyResultClaim must be atomic: when it returns an error, neither
// the claim nor the Attempt change is visible. A new claim for an Attempt that
// already has one fails with ErrAlreadyExists; a differing claim is a
// ErrConflict. A stale ExpectedAttemptRevision fails with ErrConflict.
type ResultClaimStore interface {
	LoadResultClaim(ctx context.Context, id ids.AttemptID) (resultclaim.Claim, bool, error)
	ApplyResultClaim(ctx context.Context, acceptance ResultClaimAcceptance) error
}
