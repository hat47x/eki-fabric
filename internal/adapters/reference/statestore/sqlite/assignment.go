package sqlite

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.AssignmentStore = (*Store)(nil)

// ListEmittedAssignments implements statestore.AssignmentStore. It needs no
// schema change: the Attempt, Lease and both emission rows already say whether
// an assignment is deliverable.
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
	rows, err := s.db.QueryContext(ctx, `
SELECT l.lease_id, l.attempt_id
FROM attempts a
JOIN leases l ON l.lease_id = a.lease_id AND l.worker_identity = a.worker_identity
JOIN lease_emissions e ON e.lease_id = l.lease_id AND e.status = 'emitted'
JOIN work_order_emissions w ON w.lease_id = l.lease_id AND w.status = 'emitted' AND w.worker_identity = a.worker_identity
WHERE a.worker_identity = ? AND a.phase IN ('assigned','dispatched')
ORDER BY l.issued_at_ns, l.lease_id
LIMIT ?`, workerIdentity, limit)
	if err != nil {
		return nil, fmt.Errorf("list assignments: %w", err)
	}
	type pair struct{ lease, attempt string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.lease, &p.attempt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list assignments: %w", err)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list assignments: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]statestore.EmittedAssignment, 0, len(pairs))
	for _, p := range pairs {
		leaseID, err := ids.ParseLeaseID(p.lease)
		if err != nil {
			return nil, fmt.Errorf("%w: lease ID %q: %v", ErrCorruptRecord, p.lease, err)
		}
		attemptID, err := ids.ParseAttemptID(p.attempt)
		if err != nil {
			return nil, fmt.Errorf("%w: attempt ID %q: %v", ErrCorruptRecord, p.attempt, err)
		}
		le, err := s.LoadLeaseEmission(ctx, leaseID)
		if err != nil {
			return nil, err
		}
		wo, err := s.LoadWorkOrderEmission(ctx, leaseID)
		if err != nil {
			return nil, err
		}
		out = append(out, statestore.EmittedAssignment{LeaseID: leaseID, AttemptID: attemptID, Lease: le, WorkOrder: wo})
	}
	return out, nil
}
