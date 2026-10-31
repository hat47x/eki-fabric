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
type EmittedAssignment struct {
	LeaseID   ids.LeaseID
	AttemptID ids.AttemptID
	Lease     leaseemission.Record
	WorkOrder workorderemission.Record
}

// AssignmentStore is the read side the Worker delivery route needs.
//
// ListEmittedAssignments returns, oldest first and at most limit entries (0 or
// above MaxAssignmentsPerList means MaxAssignmentsPerList), the assignments of
// Attempts that are bound to the Worker and have not yet produced a claim: the
// Attempt is ASSIGNED or DISPATCHED, and both the Lease and the WorkOrder are
// EMITTED for that Worker. A Lease whose validity window has closed at the
// time of the call is not listed: the Worker refuses expired material on its
// own clock anyway, so listing it would only spend the delivery window on
// assignments that can no longer start and can crowd out live ones. It never
// returns another Worker's records, an ABORTED record, or an Attempt that is
// closed.
//
// It marks nothing as delivered, so listing again returns the same bytes
// (idempotent re-delivery). Whether the Worker still may act is decided by the
// Worker's own verification, not by this list.
type AssignmentStore interface {
	ListEmittedAssignments(ctx context.Context, workerIdentity string, limit int) ([]EmittedAssignment, error)
}
