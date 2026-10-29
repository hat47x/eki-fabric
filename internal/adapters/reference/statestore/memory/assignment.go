package memory

import (
	"context"
	"sort"

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
	for _, attempt := range s.attempts {
		if attempt.WorkerIdentity != workerIdentity || attempt.LeaseID == "" ||
			(attempt.Phase != attemptstate.AttemptPhaseAssigned && attempt.Phase != attemptstate.AttemptPhaseDispatched) {
			continue
		}
		lease, ok := s.leases[attempt.LeaseID]
		if !ok || lease.WorkerIdentity != workerIdentity {
			continue
		}
		le, ok := s.leaseEmissions[attempt.LeaseID]
		if !ok || le.Status() != leaseemission.StatusEmitted {
			continue
		}
		wo, ok := s.workOrders[attempt.LeaseID]
		if !ok || wo.Status() != workorderemission.StatusEmitted || wo.WorkerIdentity() != workerIdentity {
			continue
		}
		out = append(out, statestore.EmittedAssignment{LeaseID: attempt.LeaseID, AttemptID: attempt.ID, Lease: le, WorkOrder: wo})
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
