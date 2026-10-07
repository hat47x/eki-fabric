package attemptstate

import (
	"errors"
	"fmt"
)

var ErrInvalidAttemptTransition = errors.New("invalid attempt transition")

// ValidateTransition checks a single durable Attempt state transition.
// Retry, migration, and supersession create a new Attempt; a closed Attempt
// never becomes executable again.
func ValidateTransition(current, next AttemptRecord) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("%w: current: %v", ErrInvalidAttemptTransition, err)
	}
	if err := next.Validate(); err != nil {
		return fmt.Errorf("%w: next: %v", ErrInvalidAttemptTransition, err)
	}
	if current.ID != next.ID ||
		current.TaskID != next.TaskID ||
		current.TaskIRDigest != next.TaskIRDigest ||
		current.Number != next.Number ||
		!current.CreatedAt.Equal(next.CreatedAt) {
		return fmt.Errorf("%w: immutable Attempt identity changed", ErrInvalidAttemptTransition)
	}
	if next.Revision != current.Revision+1 {
		return fmt.Errorf("%w: revision %d does not follow %d", ErrInvalidAttemptTransition, next.Revision, current.Revision)
	}
	if next.UpdatedAt.Before(current.UpdatedAt) {
		return fmt.Errorf("%w: updated time moved backwards", ErrInvalidAttemptTransition)
	}

	switch current.Phase {
	case AttemptPhaseCreated:
		if next.Phase != AttemptPhasePlacing && next.Phase != AttemptPhaseClosed {
			return phaseTransitionError(current, next)
		}
	case AttemptPhasePlacing:
		if next.Phase != AttemptPhasePlacing && next.Phase != AttemptPhaseAssigned && next.Phase != AttemptPhaseClosed {
			return phaseTransitionError(current, next)
		}
	case AttemptPhaseAssigned:
		if next.Phase != AttemptPhaseDispatched && next.Phase != AttemptPhaseClosed {
			return phaseTransitionError(current, next)
		}
	case AttemptPhaseDispatched:
		if next.Phase != AttemptPhaseClaimed && next.Phase != AttemptPhaseClosed {
			return phaseTransitionError(current, next)
		}
	case AttemptPhaseClaimed:
		if next.Phase != AttemptPhaseVerifying && next.Phase != AttemptPhaseClosed {
			return phaseTransitionError(current, next)
		}
	case AttemptPhaseVerifying:
		if next.Phase != AttemptPhaseClosed {
			return phaseTransitionError(current, next)
		}
	case AttemptPhaseClosed:
		return fmt.Errorf("%w: closed Attempt cannot transition", ErrInvalidAttemptTransition)
	default:
		return fmt.Errorf("%w: current phase %q is unknown", ErrInvalidAttemptTransition, current.Phase)
	}

	// The Scheduler may set the first candidate on CREATED -> PLACING and may
	// replace that candidate only while remaining in PLACING. The Worker must
	// already be fixed before PLACING -> ASSIGNED binds a Lease.
	workerChanged := current.WorkerIdentity != next.WorkerIdentity
	workerChangeAllowed := (current.Phase == AttemptPhaseCreated && next.Phase == AttemptPhasePlacing) ||
		(current.Phase == AttemptPhasePlacing && next.Phase == AttemptPhasePlacing)
	if workerChanged && !workerChangeAllowed {
		return fmt.Errorf("%w: Worker binding changed outside placement replanning", ErrInvalidAttemptTransition)
	}
	if current.LeaseID != "" && next.LeaseID != current.LeaseID {
		return fmt.Errorf("%w: Lease binding changed", ErrInvalidAttemptTransition)
	}
	if current.LeaseID == "" && next.LeaseID != "" &&
		!(current.Phase == AttemptPhasePlacing && next.Phase == AttemptPhaseAssigned) {
		return fmt.Errorf("%w: Lease may only bind on placing -> assigned", ErrInvalidAttemptTransition)
	}
	return nil
}

func phaseTransitionError(current, next AttemptRecord) error {
	return fmt.Errorf("%w: phase %q -> %q", ErrInvalidAttemptTransition, current.Phase, next.Phase)
}
