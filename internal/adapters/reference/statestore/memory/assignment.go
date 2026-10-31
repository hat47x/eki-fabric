package memory

import (
	"context"
	"sort"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/workorderemission"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.AssignmentStore = (*Store)(nil)

// ListEmittedAssignments implements statestore.AssignmentStore.
func (s *Store) ListEmittedAssignments(ctx context.Context, workerIdentity string, limit int) ([]statestore.EmittedAssignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if workerIdentity == "" {
		return nil, nil
	}
	if limit <= 0 || limit > statestore.MaxAssignmentsPerList {
		limit = statestore.MaxAssignmentsPerList
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []statestore.EmittedAssignment
	now := s.now()
	for _, attempt := range s.attempts {
		if attempt.WorkerIdentity != workerIdentity || attempt.LeaseID == "" {
			continue
		}
		revoked := attempt.Phase == attemptstate.AttemptPhaseClosed &&
			(attempt.CloseReason == attemptstate.CloseReasonReclaimed ||
				attempt.CloseReason == attemptstate.CloseReasonCancelled ||
				attempt.CloseReason == attemptstate.CloseReasonSuperseded ||
				attempt.CloseReason == attemptstate.CloseReasonPolicyRevoked)
		if attempt.Phase != attemptstate.AttemptPhaseAssigned &&
			attempt.Phase != attemptstate.AttemptPhaseDispatched && !revoked {
			continue
		}
		lease, ok := s.leases[attempt.LeaseID]
		if !ok || lease.WorkerIdentity != workerIdentity || !now.Before(lease.ExpiresAt) {
			continue
		}
		le, ok := s.leaseEmissions[attempt.LeaseID]
		if !ok || (le.Status() != leaseemission.StatusEmitted && !revoked) {
			continue
		}
		wo, ok := s.workOrders[attempt.LeaseID]
		if !ok || wo.Status() != workorderemission.StatusEmitted || wo.WorkerIdentity() != workerIdentity {
			continue
		}
		out = append(out, statestore.EmittedAssignment{LeaseID: attempt.LeaseID, AttemptID: attempt.ID, Lease: le, WorkOrder: wo, LeaseState: leaseDeliveryState(le, lease.ExpiresAt, now)})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := s.leases[out[i].LeaseID], s.leases[out[j].LeaseID]
		if !a.IssuedAt.Equal(b.IssuedAt) {
			return a.IssuedAt.Before(b.IssuedAt)
		}
		return out[i].LeaseID < out[j].LeaseID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetListingClock replaces the clock ListEmittedAssignments uses to drop
// Leases whose validity window has closed. It exists so a contract test can
// pin the boundary without waiting on the wall clock; production code never
// calls it, and the default is time.Now.
func (s *Store) SetListingClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}
