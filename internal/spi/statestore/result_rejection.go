package statestore

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/rejectedresult"
)

// ResultRejectionCommit is the one atomic step that records why RAC-1
// rejected a ResultClaim and closes the Attempt as result_rejected.
//
// The two changes are one commit for the same reason as an acceptance: an
// Attempt closed as rejected with no record cannot be explained later (#57
// evidence chain), and a record with an open Attempt would let the Attempt be
// closed another way. The Task is not changed: whether the work is retried is
// a recovery decision.
type ResultRejectionCommit struct {
	Result                  rejectedresult.Record
	ExpectedAttemptRevision uint64
	// NextAttempt must be the stored Attempt moved from A-VERIFYING to A-CLOSED
	// with the close reason result_rejected.
	NextAttempt attemptstate.AttemptRecord
}

// Validate checks the commit without reading any stored state.
func (c ResultRejectionCommit) Validate() error {
	switch {
	case !c.Result.Valid():
		return fmt.Errorf("%w: the rejected result is not constructed", ErrInvalidAcceptance)
	case c.NextAttempt.ID != c.Result.AttemptID() || c.NextAttempt.TaskID != c.Result.TaskID():
		return fmt.Errorf("%w: the Attempt does not belong to the rejected result", ErrInvalidAcceptance)
	case c.NextAttempt.Phase != attemptstate.AttemptPhaseClosed ||
		c.NextAttempt.CloseReason != attemptstate.CloseReasonResultRejected:
		return fmt.Errorf("%w: the Attempt must close as %s", ErrInvalidAcceptance, attemptstate.CloseReasonResultRejected)
	case c.NextAttempt.Revision != c.ExpectedAttemptRevision+1:
		return fmt.Errorf("%w: Attempt revision %d does not follow %d", ErrInvalidAcceptance, c.NextAttempt.Revision, c.ExpectedAttemptRevision)
	}
	if err := c.NextAttempt.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAcceptance, err)
	}
	return nil
}

// ResultRejectionStore is the durable owner of rejected results.
//
// CommitRejectedResult must be atomic. It fails with ErrConflict when any of
// these is false at commit time:
//   - the Attempt is in A-VERIFYING at ExpectedAttemptRevision and belongs to
//     the Task;
//   - the ResultClaim recorded for the Attempt is a completed claim whose claim
//     digest and output digest equal the ones in Result, so the record always
//     names the claim that was actually rejected.
//
// An Attempt has at most one rejected result. Committing the same record again
// fails with ErrAlreadyExists, and a different one fails with ErrConflict.
// Records are immutable. Malformed commits fail with ErrInvalidAcceptance.
type ResultRejectionStore interface {
	LoadRejectedResult(ctx context.Context, id ids.AttemptID) (rejectedresult.Record, bool, error)
	CommitRejectedResult(ctx context.Context, commit ResultRejectionCommit) error
}
