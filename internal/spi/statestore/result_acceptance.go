package statestore

import (
	"context"
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/acceptedresult"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// ResultAcceptanceCommit is the one atomic step that turns a verified
// ResultClaim into an AcceptedResult: it records the accepted result, closes the
// Attempt as accepted, and completes the Task.
//
// The three changes are one commit because a half-applied acceptance is a wrong
// answer to a Client. An accepted result with an open Attempt would let the
// Attempt be closed another way; a completed Task with no accepted result would
// say "done" with nothing behind it.
type ResultAcceptanceCommit struct {
	Result                  acceptedresult.Record
	ExpectedAttemptRevision uint64
	// NextAttempt must be the stored Attempt moved from A-VERIFYING to A-CLOSED
	// with the close reason result_accepted.
	NextAttempt          attemptstate.AttemptRecord
	ExpectedTaskRevision uint64
	// NextTask must be the stored Task moved to its terminal phase with the
	// outcome completed, keeping its TaskIR digest.
	NextTask taskstate.TaskRecord
}

// ResultAcceptanceStore is the durable owner of accepted results.
//
// CommitAcceptedResult must be atomic. It fails with ErrConflict when any of
// these is false at commit time:
//   - the Attempt is in A-VERIFYING at ExpectedAttemptRevision and belongs to
//     the Task;
//   - the ResultClaim recorded for the Attempt is a completed claim whose claim
//     digest and output digest equal the ones in Result, so a result can only be
//     accepted for what the Worker actually claimed;
//   - the Task is at ExpectedTaskRevision, has not reached a terminal phase, and
//     has no cancellation requested.
//
// A Task has at most one accepted result. Committing the same result again
// fails with ErrAlreadyExists, and a different one fails with ErrConflict.
type ResultAcceptanceStore interface {
	LoadAcceptedResult(ctx context.Context, id ids.TaskID) (acceptedresult.Record, bool, error)
	CommitAcceptedResult(ctx context.Context, commit ResultAcceptanceCommit) error
}

// ErrInvalidAcceptance means the commit is malformed on its own, before any
// stored state is read.
var ErrInvalidAcceptance = errors.New("invalid result acceptance commit")

// Validate checks the commit without reading any stored state. Adapters call it
// first and then check the stored state inside their transaction.
func (c ResultAcceptanceCommit) Validate() error {
	switch {
	case !c.Result.Valid():
		return fmt.Errorf("%w: the accepted result is not constructed", ErrInvalidAcceptance)
	case c.NextAttempt.ID != c.Result.AttemptID() || c.NextAttempt.TaskID != c.Result.TaskID():
		return fmt.Errorf("%w: the Attempt does not belong to the accepted result", ErrInvalidAcceptance)
	case c.NextAttempt.Phase != attemptstate.AttemptPhaseClosed ||
		c.NextAttempt.CloseReason != attemptstate.CloseReasonResultAccepted:
		return fmt.Errorf("%w: the Attempt must close as %s", ErrInvalidAcceptance, attemptstate.CloseReasonResultAccepted)
	case c.NextAttempt.Revision != c.ExpectedAttemptRevision+1:
		return fmt.Errorf("%w: Attempt revision %d does not follow %d", ErrInvalidAcceptance, c.NextAttempt.Revision, c.ExpectedAttemptRevision)
	case c.NextTask.ID != c.Result.TaskID():
		return fmt.Errorf("%w: the Task does not belong to the accepted result", ErrInvalidAcceptance)
	case c.NextTask.Phase != taskstate.TaskPhaseTerminal || c.NextTask.TerminalOutcome != taskstate.TerminalOutcomeCompleted:
		return fmt.Errorf("%w: the Task must complete", ErrInvalidAcceptance)
	case c.NextTask.Revision != c.ExpectedTaskRevision+1:
		return fmt.Errorf("%w: Task revision %d does not follow %d", ErrInvalidAcceptance, c.NextTask.Revision, c.ExpectedTaskRevision)
	}
	if err := c.NextAttempt.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAcceptance, err)
	}
	if err := c.NextTask.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAcceptance, err)
	}
	return nil
}
