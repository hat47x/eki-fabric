package statestore

import (
	"context"

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
