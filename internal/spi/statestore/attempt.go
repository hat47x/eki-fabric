package statestore

import (
	"context"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// AttemptStore is the durable owner of Attempt lifecycle state.
//
// CreateAttempt accepts only an initial A-CREATED / revision 1 record and
// must reject an absent, cancelled, or non-progressable parent Task, a TaskIR
// mismatch, a duplicate Attempt ID, or a duplicate Attempt number within one
// Task. CompareAndSwapAttempt must use Attempt revision as its stale-writer
// fence and enforce attemptstate.ValidateTransition against the current stored
// record.
type AttemptStore interface {
	CreateAttempt(ctx context.Context, record attemptstate.AttemptRecord) error
	LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error)
	CompareAndSwapAttempt(ctx context.Context, id ids.AttemptID, expectedRevision uint64, next attemptstate.AttemptRecord) error
	// ListAttemptsByTask returns every live Attempt of one Task in Attempt
	// number order, lowest number first. It backs dispatch, which must know
	// the next free Attempt number and the Attempts a re-placement closes.
	// Closed Attempts are included: their numbers are never reused.
	ListAttemptsByTask(ctx context.Context, taskID ids.TaskID) ([]attemptstate.AttemptRecord, error)
}
