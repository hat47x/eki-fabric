package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.AssignmentHeartbeatStore = (*Store)(nil)

// RecordHeartbeats implements statestore.AssignmentHeartbeatStore (ADR-0030
// §2.2). Every named Lease must exist, be bound to the calling Worker and
// carry an EMITTED Lease emission for that Worker; a report about anything
// else fails closed. A report only moves forward: the newest accepted report
// of a Lease is the row, and an older or equally old report is dropped as a
// replay. Each Lease is one fact; the whole report is one transaction, so a
// poll either records what it saw or nothing.
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
	if len(leaseIDs) == 0 {
		return nil
	}
	if at.IsZero() {
		return fmt.Errorf("%w: heartbeat: recorded time is required", statestore.ErrConflict)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record heartbeats: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, raw := range leaseIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		leaseID, err := ids.ParseLeaseID(raw)
		if err != nil {
			return fmt.Errorf("%w: heartbeat: Lease ID %q: %v", statestore.ErrConflict, raw, err)
		}
		var attemptID string
		var worker string
		err = tx.QueryRowContext(ctx, `SELECT attempt_id, worker_identity FROM leases WHERE lease_id = ?`,
			leaseID.String()).Scan(&attemptID, &worker)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("%w: heartbeat: Lease %s", statestore.ErrNotFound, leaseID)
		case err != nil:
			return fmt.Errorf("record heartbeat %s: %w", leaseID, err)
		case worker != workerIdentity:
			return fmt.Errorf("%w: heartbeat: Lease %s is not bound to Worker %s",
				statestore.ErrNotFound, leaseID, workerIdentity)
		}
		var status string
		err = tx.QueryRowContext(ctx, `SELECT status FROM lease_emissions WHERE lease_id = ?`,
			leaseID.String()).Scan(&status)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("%w: heartbeat: Lease %s has no emission", statestore.ErrConflict, leaseID)
		case err != nil:
			return fmt.Errorf("record heartbeat %s: %w", leaseID, err)
		case status != "emitted":
			return fmt.Errorf("%w: heartbeat: Lease %s emission is %q, not emitted",
				statestore.ErrConflict, leaseID, status)
		}
		// A revoked delivery is no longer a live Lease even though its
		// emission row stays EMITTED (the row is immutable): a Worker cannot
		// claim to still be running a Lease the Control Plane took back.
		var revoked int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM delivery_revocations WHERE lease_id = ?`,
			leaseID.String()).Scan(&revoked); err != nil {
			return fmt.Errorf("record heartbeat %s: %w", leaseID, err)
		}
		if revoked > 0 {
			return fmt.Errorf("%w: heartbeat: Lease %s delivery is revoked",
				statestore.ErrConflict, leaseID)
		}

		// Only the newest report per Lease is kept: a revision that would not
		// move the recorded instant forward is the same fact arriving twice.
		res, err := tx.ExecContext(ctx, `
INSERT INTO assignment_heartbeats (lease_id, attempt_id, worker_identity, recorded_at_ns, revision)
VALUES (?, ?, ?, ?, 1)
ON CONFLICT (lease_id) DO UPDATE SET
	attempt_id = excluded.attempt_id,
	worker_identity = excluded.worker_identity,
	recorded_at_ns = excluded.recorded_at_ns,
	revision = assignment_heartbeats.revision + 1
WHERE excluded.recorded_at_ns > assignment_heartbeats.recorded_at_ns`,
			leaseID.String(), attemptID, workerIdentity, at.UnixNano())
		if err != nil {
			return fmt.Errorf("record heartbeat %s: %w", leaseID, err)
		}
		if res == nil { // unreachable; guards the driver contract
			return fmt.Errorf("record heartbeat %s: no result", leaseID)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit heartbeats: %w", err)
	}
	return nil
}

// LatestHeartbeat returns the newest accepted report of one Lease, or
// statestore.ErrNotFound when no report was ever accepted.
func (s *Store) LatestHeartbeat(ctx context.Context, leaseID ids.LeaseID) (statestore.Heartbeat, error) {
	if err := ctx.Err(); err != nil {
		return statestore.Heartbeat{}, err
	}
	var (
		attemptID string
		worker    string
		atNS      int64
		revision  uint64
	)
	err := s.db.QueryRowContext(ctx, `
SELECT attempt_id, worker_identity, recorded_at_ns, revision
FROM assignment_heartbeats WHERE lease_id = ?`, leaseID.String()).
		Scan(&attemptID, &worker, &atNS, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.Heartbeat{}, fmt.Errorf("%w: heartbeat of Lease %s", statestore.ErrNotFound, leaseID)
	}
	if err != nil {
		return statestore.Heartbeat{}, fmt.Errorf("load heartbeat of Lease %s: %w", leaseID, err)
	}
	attempt, err := ids.ParseAttemptID(attemptID)
	if err != nil {
		return statestore.Heartbeat{}, fmt.Errorf("%w: heartbeat of Lease %s: Attempt ID %q: %v",
			ErrCorruptRecord, leaseID, attemptID, err)
	}
	return statestore.Heartbeat{
		LeaseID: leaseID, AttemptID: attempt, WorkerIdentity: worker,
		RecordedAt: time.Unix(0, atNS).UTC(), Revision: revision,
	}, nil
}
