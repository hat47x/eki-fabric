package resourceprovider

import (
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// Disposition is what EKI does with one provider event. It is not a Task state.
// The Lease and Attempt authorities make the state transitions.
type Disposition string

const (
	// DispositionRunning means the provider reports that the slot has started work.
	DispositionRunning Disposition = "running"
	// DispositionClaimed means the provider reports completion. That report is a
	// claim, not an accepted result (rule c).
	DispositionClaimed Disposition = "claimed"
	// DispositionAttemptFailed means the provider reports a failure, and no
	// automatic retry follows.
	DispositionAttemptFailed Disposition = "attempt-failed"
	// DispositionReclaim means the provider took the slot back, for example because
	// a higher-priority job preempted it. The Lease authority ends the lease. The
	// event is not a failure (rule b).
	DispositionReclaim Disposition = "reclaim"
	// DispositionInternalRetry means the provider retried the work on its own. The
	// retry sits inside one Attempt, so EKI records it as a possible extra
	// execution (research section 5.1).
	DispositionInternalRetry Disposition = "internal-retry"
)

// Classify maps a provider event to its disposition.
//
// Rule (b): a preemption maps to DispositionReclaim, never to
// DispositionAttemptFailed. A completion maps to DispositionClaimed, which rule (c)
// then bounds through ClaimFromEvent.
func Classify(kind EventKind) (Disposition, error) {
	switch kind {
	case EventStarted:
		return DispositionRunning, nil
	case EventCompleted:
		return DispositionClaimed, nil
	case EventFailed:
		return DispositionAttemptFailed, nil
	case EventPreempted:
		return DispositionReclaim, nil
	case EventRequeued:
		return DispositionInternalRetry, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownEvent, kind)
}

// CountsAsAttemptFailure reports whether the disposition adds to the count of
// failed Attempts. Only a failure that the provider reports does. A reclaim never
// does (rule b).
func (d Disposition) CountsAsAttemptFailure() bool {
	return d == DispositionAttemptFailed
}

// ClaimOutcome is the outcome that a provider claims for one lease.
type ClaimOutcome string

const (
	// ClaimSucceeded is the provider's claim that the work succeeded.
	ClaimSucceeded ClaimOutcome = "succeeded"
	// ClaimFailed is the provider's claim that the work failed.
	ClaimFailed ClaimOutcome = "failed"
)

// ProviderClaim is what a provider reports about the outcome of one lease. It is a
// statement by the provider, and nothing in it has been verified. It has no field
// or method that marks a result as accepted. Acceptance is decided on the Result
// verification path (RVR-1 and RAC-1 in architecture/AUTHORITY_BOUNDARIES.md),
// which takes a claim as its input.
type ProviderClaim struct {
	LeaseID  ids.LeaseID
	Sequence uint64
	Outcome  ClaimOutcome
}

// ClaimFromEvent returns the claim that a terminal event makes.
//
// Rule (c): a provider's report of success is a claim, not an accepted result. ok
// is false for events that do not state an outcome.
func ClaimFromEvent(ev Event) (ProviderClaim, bool) {
	var outcome ClaimOutcome
	switch ev.Kind {
	case EventCompleted:
		outcome = ClaimSucceeded
	case EventFailed:
		outcome = ClaimFailed
	default:
		return ProviderClaim{}, false
	}
	return ProviderClaim{LeaseID: ev.LeaseID, Sequence: ev.Sequence, Outcome: outcome}, true
}
