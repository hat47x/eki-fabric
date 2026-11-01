package statestore

import (
	"context"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/workorderemission"
)

// MaxAssignmentsPerList bounds one ListEmittedAssignments result.
const MaxAssignmentsPerList = 64

// EmittedAssignment is the pair of EMITTED records the Control Plane may hand
// to the Worker that holds an Attempt: the Lease and the WorkOrder of one Lease
// ID. The signed bytes are the exact stored bytes; nothing here re-signs.
//
// LeaseState is the delivery-level currency of the Lease at listing time
// (ADR-0030 §2.2): current while the validity window is open, revoked once the
// emission was aborted, expired once the window closed. The listing drops
// expired entries entirely; revoked ones are still listed so the Worker learns
// the revocation on its next poll instead of hearing silence.
type EmittedAssignment struct {
	LeaseID    ids.LeaseID
	AttemptID  ids.AttemptID
	Lease      leaseemission.Record
	WorkOrder  workorderemission.Record
	LeaseState LeaseDeliveryState
}

// LeaseDeliveryState says how current a listed Lease is. It is a delivery
// fact of this listing, not a state of the Attempt.
type LeaseDeliveryState string

const (
	// LeaseDeliveryCurrent: the Lease's validity window is open.
	LeaseDeliveryCurrent LeaseDeliveryState = "current"
	// LeaseDeliveryRevoked: the Lease emission was aborted. The Worker must
	// stop the Attempt and may not start it.
	LeaseDeliveryRevoked LeaseDeliveryState = "revoked"
	// LeaseDeliveryExpired: the validity window has closed. Expired entries are
	// dropped from the listing, so this value never appears in a response; it
	// names the third case for completeness of the enumeration.
	LeaseDeliveryExpired LeaseDeliveryState = "expired"
)

// AssignmentStore is the read side the Worker delivery route needs.
//
// ListEmittedAssignments returns, oldest first and at most limit entries (0 or
// above MaxAssignmentsPerList means MaxAssignmentsPerList), the assignments of
// Attempts that are bound to the Worker and have not yet produced a claim: the
// Attempt is ASSIGNED or DISPATCHED, and both the Lease and the WorkOrder are
// EMITTED for that Worker. A Lease whose validity window has closed at the
// time of the call is not listed: the Worker refuses expired material on its
// own clock anyway, so listing it would only spend the delivery window on
// assignments that can no longer start and can crowd out live ones. An ABORTED
// Lease emission is listed with LeaseState revoked (ADR-0030 §2.2): silence
// would leave a Worker that already started the Attempt running without a
// current Lease.
//
// It marks nothing as delivered, so listing again returns the same bytes
// (idempotent re-delivery). Whether the Worker still may act is decided by the
// Worker's own verification, not by this list.
type AssignmentStore interface {
	ListEmittedAssignments(ctx context.Context, workerIdentity string, limit int) ([]EmittedAssignment, error)
}

// AssignmentRevokeStore is the Control Plane's write side of the same listing:
// it reclaims one delivered Assignment (ADR-0030 §2.2). The revocation is one
// durable fact, recorded once; replaying it with the same reason changes
// nothing, and replaying it with another reason is refused.
type AssignmentRevokeStore interface {
	// RevokeAssignment records the revocation of every delivered assignment of
	// one Lease. It fails closed when the Lease has no EMITTED delivery to
	// revoke: nothing was delivered, so there is nothing for the Worker to
	// un-learn.
	RevokeAssignment(ctx context.Context, revoke AssignmentRevocation) error
}

// Heartbeat is one recorded liveness fact (ADR-0030 §2.2): the Worker named by
// WorkerIdentity said, at RecordedAt, that it is running the Attempt of
// LeaseID. The Control Plane wrote the fact; the Worker never writes state
// through it, and the Attempt of the Lease keeps whatever phase it had.
type Heartbeat struct {
	LeaseID        ids.LeaseID
	AttemptID      ids.AttemptID
	WorkerIdentity string
	RecordedAt     time.Time
	// Revision counts the accepted reports of this Lease from zero. It moves
	// forward only; a report older than the last accepted one is dropped as a
	// replay and does not bump it.
	Revision uint64
}

// AssignmentHeartbeatStore records the running-Lease reports a polling Worker
// sends with its assignments request (ADR-0030 §2.2). The report is a
// liveness fact: the recorder checks each named Lease against its own store
// and refuses Leases that do not belong to the Worker, instead of trusting
// the request.
type AssignmentHeartbeatStore interface {
	// RecordHeartbeats records one poll's running-Lease report. Every named
	// Lease must be bound to workerIdentity and EMITTED for it; anything else
	// fails closed. Reporting the same Lease again at the same or an older
	// recorded time changes nothing.
	RecordHeartbeats(ctx context.Context, workerIdentity string, leaseIDs []string, at time.Time) error
}

// AssignmentRevocation names one revocation and why the Control Plane decided
// it. CloseReason is the reason the backing Attempt is closed with; the valid
// reasons are the pre-emission abort reasons.
type AssignmentRevocation struct {
	LeaseID     ids.LeaseID
	CloseReason attemptstate.CloseReason
	Reason      string
	CommittedAt time.Time
}

func (r AssignmentRevocation) Validate() error {
	if _, err := ids.ParseLeaseID(r.LeaseID.String()); err != nil {
		return fmt.Errorf("Assignment revocation: Lease ID: %w", err)
	}
	switch r.CloseReason {
	case attemptstate.CloseReasonReclaimed, attemptstate.CloseReasonCancelled,
		attemptstate.CloseReasonSuperseded, attemptstate.CloseReasonPolicyRevoked:
	default:
		return fmt.Errorf("Assignment revocation: close reason %q does not revoke a delivery", r.CloseReason)
	}
	if _, err := leaseemission.NewAborted(leaseemission.AbortedConfig{
		LeaseID:     r.LeaseID,
		CloseReason: string(r.CloseReason),
		Reason:      r.Reason,
		CommittedAt: r.CommittedAt,
	}); err != nil {
		return err
	}
	return nil
}
