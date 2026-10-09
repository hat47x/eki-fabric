package leasestate

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/placementverify"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// ErrReservationNotCurrent means a verified Placement Approval no longer
// describes the current Task or Attempt, so no new Lease may be reserved.
var ErrReservationNotCurrent = errors.New("lease reservation is not current")

// ApprovalCurrentness is the part of a verified Placement Approval that is
// checked against current state before a Lease is reserved.
// placementverify.Verified implements it.
type ApprovalCurrentness interface {
	Revalidate(placementverify.Current) error
}

var _ ApprovalCurrentness = placementverify.Verified{}

// ReservationExpectation is the Task and Attempt currentness that LSE-1 must
// present with a new Lease reservation. The State Store confirms each field
// inside its reservation transaction.
type ReservationExpectation struct {
	TaskID                  ids.TaskID
	TaskIRDigest            contract.Digest
	ExpectedTaskRevision    uint64
	AttemptID               ids.AttemptID
	ExpectedAttemptRevision uint64
	WorkerIdentity          string
}

// ExpectReservation checks a verified Placement Approval against snapshots of
// the current Task and Attempt. It returns the revisions that a new Lease
// reservation must present to the State Store.
//
// The snapshots come from the caller. leasestate cannot load them itself,
// because the State Store SPI imports this package. The check therefore runs
// before the reservation as an early rejection. Only the State Store's check
// inside the reservation transaction is authoritative, and it repeats the same
// Task and Attempt conditions.
//
// This applies to new reservations only. An exact replay of an existing Lease
// converges inside the store and does not need an expectation.
func ExpectReservation(approval ApprovalCurrentness, task taskstate.TaskRecord, attempt attemptstate.AttemptRecord, policyRevision string, now time.Time) (ReservationExpectation, error) {
	if approval == nil {
		return ReservationExpectation{}, fmt.Errorf("%w: no verified Placement Approval", ErrReservationNotCurrent)
	}
	if err := attempt.Validate(); err != nil {
		return ReservationExpectation{}, fmt.Errorf("%w: %w", ErrReservationNotCurrent, err)
	}
	if attempt.Phase != attemptstate.AttemptPhasePlacing {
		return ReservationExpectation{}, fmt.Errorf(
			"%w: attempt %s is in phase %q, want %q",
			ErrReservationNotCurrent,
			attempt.ID,
			attempt.Phase,
			attemptstate.AttemptPhasePlacing,
		)
	}
	if attempt.TaskID != task.ID || attempt.TaskIRDigest != task.TaskIRDigest {
		return ReservationExpectation{}, fmt.Errorf(
			"%w: attempt %s is not bound to Task %s and its TaskIR digest",
			ErrReservationNotCurrent,
			attempt.ID,
			task.ID,
		)
	}
	if err := approval.Revalidate(placementverify.Current{
		Task:           task,
		AttemptID:      attempt.ID,
		WorkerIdentity: attempt.WorkerIdentity,
		PolicyRevision: policyRevision,
		Now:            now,
	}); err != nil {
		return ReservationExpectation{}, fmt.Errorf("%w: %w", ErrReservationNotCurrent, err)
	}
	return ReservationExpectation{
		TaskID:                  task.ID,
		TaskIRDigest:            task.TaskIRDigest,
		ExpectedTaskRevision:    task.Revision,
		AttemptID:               attempt.ID,
		ExpectedAttemptRevision: attempt.Revision,
		WorkerIdentity:          attempt.WorkerIdentity,
	}, nil
}
