package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) ReserveLease(ctx context.Context, reservation statestore.LeaseReservation) (statestore.LeaseReservationResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.LeaseReservationResult{}, err
	}
	if err := reservation.Validate(); err != nil {
		return statestore.LeaseReservationResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return statestore.LeaseReservationResult{}, fmt.Errorf("reserve lease %s: %w", reservation.LeaseID, err)
	}
	defer func() { _ = tx.Rollback() }()

	task, err := scanTask(tx.QueryRowContext(ctx, `
SELECT task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns
FROM tasks WHERE task_id = ?`, reservation.TaskID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, reservation.TaskID)
	}
	if err != nil {
		return statestore.LeaseReservationResult{}, err
	}
	if task.Revision != reservation.ExpectedTaskRevision ||
		task.TaskIRDigest != reservation.TaskIRDigest ||
		task.CancellationRequested ||
		(task.Phase != taskstate.TaskPhaseReady && task.Phase != taskstate.TaskPhaseActive) {
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: task %s changed before lease reservation",
			statestore.ErrConflict,
			reservation.TaskID,
		)
	}

	existing, err := scanLease(tx.QueryRowContext(ctx, `
SELECT lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
       placement_decision_id, placement_payload_digest, policy_revision,
       fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
FROM leases WHERE placement_decision_id = ?`, reservation.PlacementDecisionID))
	switch {
	case err == nil:
		if existing.PlacementPayloadDigest != reservation.PlacementPayloadDigest {
			return statestore.LeaseReservationResult{}, fmt.Errorf(
				"%w: decision %q",
				statestore.ErrPlacementDecisionConflict,
				reservation.PlacementDecisionID,
			)
		}
		return statestore.LeaseReservationResult{Lease: existing, Replayed: true}, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return statestore.LeaseReservationResult{}, err
	}

	var activeID string
	err = tx.QueryRowContext(ctx, `
SELECT lease_id
FROM leases
WHERE task_id = ? AND expires_at_ns > ?
ORDER BY fence_epoch DESC
LIMIT 1`, reservation.TaskID.String(), reservation.IssuedAt.UTC().UnixNano()).Scan(&activeID)
	switch {
	case err == nil:
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: task %s lease=%s",
			statestore.ErrLeaseActive,
			reservation.TaskID,
			activeID,
		)
	case errors.Is(err, sql.ErrNoRows):
	default:
		return statestore.LeaseReservationResult{}, fmt.Errorf("find active lease for task %s: %w", reservation.TaskID, err)
	}

	var maxFence int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(fence_epoch), 0) FROM leases WHERE task_id = ?`,
		reservation.TaskID.String(),
	).Scan(&maxFence); err != nil {
		return statestore.LeaseReservationResult{}, fmt.Errorf("read fence epoch for task %s: %w", reservation.TaskID, err)
	}
	if maxFence < 0 || maxFence == math.MaxInt64 {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: fence epoch exhausted for task %s", statestore.ErrConflict, reservation.TaskID)
	}
	fence := uint64(maxFence + 1)

	lease, err := leasestate.New(leasestate.Config{
		ID:                     reservation.LeaseID,
		TaskID:                 reservation.TaskID,
		TaskIRDigest:           reservation.TaskIRDigest,
		AttemptID:              reservation.AttemptID,
		WorkerIdentity:         reservation.WorkerIdentity,
		PlacementDecisionID:    reservation.PlacementDecisionID,
		PlacementPayloadDigest: reservation.PlacementPayloadDigest,
		PolicyRevision:         reservation.PolicyRevision,
		FenceEpoch:             fence,
		RenewalSequence:        0,
		IssuedAt:               reservation.IssuedAt,
		ExpiresAt:              reservation.ExpiresAt,
	})
	if err != nil {
		return statestore.LeaseReservationResult{}, err
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO leases (
	lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
	placement_decision_id, placement_payload_digest, policy_revision,
	fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		lease.ID.String(),
		lease.TaskID.String(),
		lease.TaskIRDigest.Hex(),
		lease.AttemptID.String(),
		lease.WorkerIdentity,
		lease.PlacementDecisionID,
		lease.PlacementPayloadDigest.Hex(),
		lease.PolicyRevision,
		int64(lease.FenceEpoch),
		int64(lease.RenewalSequence),
		lease.IssuedAt.UnixNano(),
		lease.ExpiresAt.UnixNano(),
	)
	if err != nil {
		return statestore.LeaseReservationResult{}, fmt.Errorf("insert lease %s: %w", lease.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return statestore.LeaseReservationResult{}, fmt.Errorf("commit lease %s: %w", lease.ID, err)
	}
	return statestore.LeaseReservationResult{Lease: lease}, nil
}

func (s *Store) LoadLease(ctx context.Context, id ids.LeaseID) (leasestate.LeaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return leasestate.LeaseRecord{}, err
	}
	lease, err := scanLease(s.db.QueryRowContext(ctx, `
SELECT lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
       placement_decision_id, placement_payload_digest, policy_revision,
       fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
FROM leases WHERE lease_id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return leasestate.LeaseRecord{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, id)
	}
	return lease, err
}

func scanLease(row rowScanner) (leasestate.LeaseRecord, error) {
	var c leaseColumns
	if err := row.Scan(
		&c.id,
		&c.taskID,
		&c.taskIRDigest,
		&c.attemptID,
		&c.workerIdentity,
		&c.placementDecisionID,
		&c.placementPayloadDigest,
		&c.policyRevision,
		&c.fenceEpoch,
		&c.renewalSequence,
		&c.issuedNS,
		&c.expiresNS,
	); err != nil {
		return leasestate.LeaseRecord{}, err
	}
	return c.record()
}

type leaseColumns struct {
	id                     string
	taskID                 string
	taskIRDigest           string
	attemptID              string
	workerIdentity         string
	placementDecisionID    string
	placementPayloadDigest string
	policyRevision         string
	fenceEpoch             int64
	renewalSequence        int64
	issuedNS               int64
	expiresNS              int64
}

func (c leaseColumns) record() (leasestate.LeaseRecord, error) {
	leaseID, err := ids.ParseLeaseID(c.id)
	if err != nil {
		return leasestate.LeaseRecord{}, corruptLease(c.id, err)
	}
	taskID, err := ids.ParseTaskID(c.taskID)
	if err != nil {
		return leasestate.LeaseRecord{}, corruptLease(c.id, err)
	}
	taskIR, err := contract.ParseSHA256(c.taskIRDigest)
	if err != nil {
		return leasestate.LeaseRecord{}, corruptLease(c.id, err)
	}
	attemptID, err := ids.ParseAttemptID(c.attemptID)
	if err != nil {
		return leasestate.LeaseRecord{}, corruptLease(c.id, err)
	}
	placementDigest, err := contract.ParseSHA256(c.placementPayloadDigest)
	if err != nil {
		return leasestate.LeaseRecord{}, corruptLease(c.id, err)
	}
	if c.fenceEpoch < 1 || c.renewalSequence < 0 {
		return leasestate.LeaseRecord{}, corruptLease(c.id, errors.New("negative or zero lease sequence"))
	}
	lease, err := leasestate.New(leasestate.Config{
		ID:                     leaseID,
		TaskID:                 taskID,
		TaskIRDigest:           taskIR,
		AttemptID:              attemptID,
		WorkerIdentity:         c.workerIdentity,
		PlacementDecisionID:    c.placementDecisionID,
		PlacementPayloadDigest: placementDigest,
		PolicyRevision:         c.policyRevision,
		FenceEpoch:             uint64(c.fenceEpoch),
		RenewalSequence:        uint64(c.renewalSequence),
		IssuedAt:               time.Unix(0, c.issuedNS).UTC(),
		ExpiresAt:              time.Unix(0, c.expiresNS).UTC(),
	})
	if err != nil {
		return leasestate.LeaseRecord{}, corruptLease(c.id, err)
	}
	return lease, nil
}

func corruptLease(id string, err error) error {
	return fmt.Errorf("%w: lease %s: %v", ErrCorruptRecord, id, err)
}
