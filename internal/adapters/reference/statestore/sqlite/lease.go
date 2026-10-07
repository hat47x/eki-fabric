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
	if task.TaskIRDigest != reservation.TaskIRDigest ||
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
       placement_trust_domain, placement_authority_principal, placement_decision_id, placement_payload_digest, placement_expires_at_ns, policy_revision,
       fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
FROM leases WHERE placement_trust_domain = ? AND placement_authority_principal = ? AND placement_decision_id = ?`,
		reservation.PlacementTrustDomain,
		reservation.PlacementAuthorityPrincipal,
		reservation.PlacementDecisionID,
	))
	switch {
	case err == nil:
		if !reservation.MatchesReservedPlacement(existing) {
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

	if task.Revision != reservation.ExpectedTaskRevision {
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: task %s changed before new lease reservation",
			statestore.ErrConflict,
			reservation.TaskID,
		)
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

	var lastFence int64
	err = tx.QueryRowContext(ctx, `
SELECT last_fence_epoch FROM lease_fences WHERE task_id = ?`,
		reservation.TaskID.String(),
	).Scan(&lastFence)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		lastFence = 0
	default:
		return statestore.LeaseReservationResult{}, fmt.Errorf("read fence epoch for task %s: %w", reservation.TaskID, err)
	}
	if lastFence < 0 || lastFence == math.MaxInt64 {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: fence epoch exhausted for task %s", statestore.ErrConflict, reservation.TaskID)
	}
	fence := uint64(lastFence + 1)

	lease, err := leasestate.New(leasestate.Config{
		ID:                     reservation.LeaseID,
		TaskID:                 reservation.TaskID,
		TaskIRDigest:           reservation.TaskIRDigest,
		AttemptID:              reservation.AttemptID,
		WorkerIdentity:         reservation.WorkerIdentity,
		PlacementTrustDomain:        reservation.PlacementTrustDomain,
		PlacementAuthorityPrincipal: reservation.PlacementAuthorityPrincipal,
		PlacementDecisionID:           reservation.PlacementDecisionID,
		PlacementPayloadDigest:        reservation.PlacementPayloadDigest,
		PlacementExpiresAt:            reservation.PlacementExpiresAt,
		PolicyRevision:                reservation.PolicyRevision,
		FenceEpoch:             fence,
		RenewalSequence:        0,
		IssuedAt:               reservation.IssuedAt,
		ExpiresAt:              reservation.ExpiresAt,
	})
	if err != nil {
		return statestore.LeaseReservationResult{}, err
	}

	fenceResult, err := tx.ExecContext(ctx, `
INSERT INTO lease_fences (task_id, last_fence_epoch)
VALUES (?, ?)
ON CONFLICT (task_id) DO UPDATE SET last_fence_epoch = excluded.last_fence_epoch
WHERE lease_fences.last_fence_epoch = ?`,
		lease.TaskID.String(),
		int64(lease.FenceEpoch),
		lastFence,
	)
	if err != nil {
		return statestore.LeaseReservationResult{}, fmt.Errorf("advance fence epoch for task %s: %w", lease.TaskID, err)
	}
	advanced, err := fenceResult.RowsAffected()
	if err != nil {
		return statestore.LeaseReservationResult{}, fmt.Errorf("read fence update result for task %s: %w", lease.TaskID, err)
	}
	if advanced != 1 {
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: task %s fence changed concurrently",
			statestore.ErrConflict,
			lease.TaskID,
		)
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO leases (
	lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
	placement_trust_domain, placement_authority_principal, placement_decision_id, placement_payload_digest, placement_expires_at_ns, policy_revision,
	fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		lease.ID.String(),
		lease.TaskID.String(),
		lease.TaskIRDigest.Hex(),
		lease.AttemptID.String(),
		lease.WorkerIdentity,
		lease.PlacementTrustDomain,
		lease.PlacementAuthorityPrincipal,
		lease.PlacementDecisionID,
		lease.PlacementPayloadDigest.Hex(),
		lease.PlacementExpiresAt.UnixNano(),
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
       placement_trust_domain, placement_authority_principal, placement_decision_id, placement_payload_digest, placement_expires_at_ns, policy_revision,
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
		&c.placementTrustDomain,
		&c.placementAuthorityPrincipal,
		&c.placementDecisionID,
		&c.placementPayloadDigest,
		&c.placementExpiresNS,
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
	workerIdentity              string
	placementTrustDomain        string
	placementAuthorityPrincipal string
	placementDecisionID         string
	placementPayloadDigest string
	placementExpiresNS     int64
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
		PlacementTrustDomain:        c.placementTrustDomain,
		PlacementAuthorityPrincipal: c.placementAuthorityPrincipal,
		PlacementDecisionID:           c.placementDecisionID,
		PlacementPayloadDigest:        placementDigest,
		PlacementExpiresAt:            time.Unix(0, c.placementExpiresNS).UTC(),
		PolicyRevision:                c.policyRevision,
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

var _ statestore.LeaseStore = (*Store)(nil)
