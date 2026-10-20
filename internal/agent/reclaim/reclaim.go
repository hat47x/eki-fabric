// Package reclaim turns an observed execution outcome into a recovery
// interruption.
//
// Not every ending is a candidate for a new Attempt. A completed execution has
// nothing to retry. An intentional cancellation must not be turned into an
// automatic retry. A stuck Runner needs the platform to stop it, and retrying
// before that would run two executions at once. This package makes the mapping
// explicit so the Agent does not have to decide it case by case.
package reclaim

import (
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
)

var (
	ErrNotReclaimable    = errors.New("the outcome is not a reclaimable interruption")
	ErrIntentionalStop   = errors.New("the execution was cancelled intentionally")
	ErrNeedsIntervention = errors.New("the Runner must be stopped before any retry")
	ErrInvalidAttempt    = errors.New("invalid attempt for recovery")
)

// Attempt is the part of the previous Attempt that recovery needs. The Contract
// comes from the durable Task state, not from the WorkOrder.
type Attempt struct {
	Contract               recovery.Contract
	AttemptID              ids.AttemptID
	AttemptNumber          uint32
	Priority               recovery.Priority
	EstimatedTransferBytes uint64
	EstimatedComputeMillis uint64
}

// FromOutcome classifies one execution outcome.
//
// reclaimed, failed, and deadline_exceeded become recovery interruptions. The
// recovery boundary still decides whether a new Attempt is allowed, using the
// Task contract, the attempt budget, the deadline, and the time-window budgets.
func FromOutcome(outcome executor.Outcome, attempt Attempt) (recovery.Interruption, error) {
	if err := validateAttempt(attempt); err != nil {
		return recovery.Interruption{}, err
	}
	switch outcome.Status {
	case executor.StatusReclaimed, executor.StatusFailed, executor.StatusDeadlineExceeded:
		return recovery.Interruption{
			AttemptID:              attempt.AttemptID,
			AttemptNumber:          attempt.AttemptNumber,
			Contract:               attempt.Contract,
			Priority:               attempt.Priority,
			EstimatedTransferBytes: attempt.EstimatedTransferBytes,
			EstimatedComputeMillis: attempt.EstimatedComputeMillis,
		}, nil
	case executor.StatusCancelled:
		return recovery.Interruption{}, fmt.Errorf("%w: %s", ErrIntentionalStop, outcome.Status)
	case executor.StatusRunnerStuck:
		return recovery.Interruption{}, fmt.Errorf("%w: %s", ErrNeedsIntervention, outcome.Status)
	case executor.StatusCompleted, executor.StatusOutputExceeded:
		return recovery.Interruption{}, fmt.Errorf("%w: %s", ErrNotReclaimable, outcome.Status)
	default:
		return recovery.Interruption{}, fmt.Errorf("%w: unknown status %q", ErrNotReclaimable, outcome.Status)
	}
}

func validateAttempt(attempt Attempt) error {
	switch {
	case attempt.Contract.TaskID == "":
		return fmt.Errorf("%w: TaskID is required", ErrInvalidAttempt)
	case attempt.Contract.TaskIRDigest.IsZero():
		return fmt.Errorf("%w: TaskIR digest is required", ErrInvalidAttempt)
	case attempt.AttemptID == "":
		return fmt.Errorf("%w: AttemptID is required", ErrInvalidAttempt)
	case attempt.AttemptNumber == 0:
		return fmt.Errorf("%w: attempt number must be positive", ErrInvalidAttempt)
	case attempt.Contract.MaxAttempts == 0:
		return fmt.Errorf("%w: MaxAttempts must be positive", ErrInvalidAttempt)
	}
	return nil
}
