package memory

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.AssignmentHeartbeatStore = (*Store)(nil)

// heartbeatRow is the latest liveness report of one Lease: when the Worker
// last said it is running it, and the Attempt the Worker was running then.
// A report never changes the Attempt, the Lease or any emission; it is an
// observation the Control Plane reads.
type heartbeatRow struct {
	At       time.Time
	Attempt  ids.AttemptID
	Worker   string
	Revision uint64
}

// RecordHeartbeats implements statestore.AssignmentHeartbeatStore (ADR-0030
// §2.2). Every named Lease must exist, be bound to the calling Worker and be
// EMITTED for that Worker; a report about anything else fails closed instead
// of being recorded, because a liveness fact about a Lease the Worker does not
// hold is exactly the smuggling the principal is there to prevent. A report
// only moves forward: a beat older than the last recorded one for the same
// Lease is dropped as a replay, so late arrivals and retries cannot make the
// liveness record go backwards. Each reported Lease is one fact and one
// revision bump; a report naming several Leases records them in order.
func (s *Store) RecordHeartbeats(ctx context.Context, workerIdentity string, leaseIDs []string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if workerIdentity == "" {
		return fmt.Errorf("%w: heartbeat: Worker identity is required", statestore.ErrNotFound)
	}
	if len(leaseIDs) > statestore.MaxAssignmentsPerList {
		return fmt.Errorf("%w: heartbeat names %d Leases, at most %d are accepted",
			statestore.ErrConflict, len(leaseIDs), statestore.MaxAssignmentsPerList)
	}
	if at.IsZero() {
		return fmt.Errorf("%w: heartbeat: recorded time is required", statestore.ErrConflict)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, raw := range leaseIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		leaseID, err := ids.ParseLeaseID(raw)
		if err != nil {
			return fmt.Errorf("%w: heartbeat: Lease ID %q: %v", statestore.ErrConflict, raw, err)
		}
		lease, ok := s.leases[leaseID]
		if !ok || lease.WorkerIdentity != workerIdentity {
			return fmt.Errorf("%w: heartbeat: Lease %s is not bound to Worker %s",
				statestore.ErrNotFound, leaseID, workerIdentity)
		}
		le, ok := s.leaseEmissions[leaseID]
		if !ok || le.Status() != leaseemission.StatusEmitted {
			return fmt.Errorf("%w: heartbeat: Lease %s has no EMITTED emission",
				statestore.ErrConflict, leaseID)
		}
		attempt, ok := s.attempts[lease.AttemptID]
		if !ok {
			return fmt.Errorf("%w: heartbeat: Lease %s holds no Attempt %s",
				statestore.ErrConflict, leaseID, lease.AttemptID)
		}
		row, exists := s.heartbeats[leaseID]
		if exists && !row.At.Before(at) {
			continue
		}
		if row.Revision == math.MaxUint64 {
			return fmt.Errorf("%w: heartbeat: Lease %s liveness revision exhausted",
				statestore.ErrConflict, leaseID)
		}
		s.heartbeats[leaseID] = heartbeatRow{
			At: at, Attempt: attempt.ID, Worker: workerIdentity, Revision: row.Revision + 1,
		}
	}
	return nil
}

// LatestHeartbeat returns the newest liveness report of one Lease. It is a
// read the Control Plane's tests and future liveness decisions use; an absent
// report is statestore.ErrNotFound.
func (s *Store) LatestHeartbeat(ctx context.Context, leaseID ids.LeaseID) (statestore.Heartbeat, error) {
	if err := ctx.Err(); err != nil {
		return statestore.Heartbeat{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.heartbeats[leaseID]
	if !ok {
		return statestore.Heartbeat{}, fmt.Errorf("%w: heartbeat of Lease %s", statestore.ErrNotFound, leaseID)
	}
	return statestore.Heartbeat{LeaseID: leaseID, AttemptID: row.Attempt, WorkerIdentity: row.Worker, RecordedAt: row.At, Revision: row.Revision}, nil
}
