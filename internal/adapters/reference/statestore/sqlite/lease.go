package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
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

	policy, err := scanPolicy(tx.QueryRowContext(ctx, `
SELECT h.epoch, h.revision, h.status, h.activated_at_ns, h.revoked_at_ns, h.revoke_reason
FROM policy_current c
JOIN policy_history h ON h.epoch = c.epoch
WHERE c.singleton = 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.LeaseReservationResult{}, statestore.ErrPolicyUnavailable
	}
	if err != nil {
		return statestore.LeaseReservationResult{}, err
	}
	if policy.Epoch != reservation.ExpectedPolicyEpoch {
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: current epoch=%d expected=%d",
			statestore.ErrPolicyChanged,
			policy.Epoch,
			reservation.ExpectedPolicyEpoch,
		)
	}
	if policy.Status == policystate.StatusRevoked && policy.Revision == reservation.PolicyRevision {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: %s", statestore.ErrPolicyRevoked, reservation.PolicyRevision)
	}
	if !policy.ActiveRevision(reservation.PolicyRevision) {
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: current revision=%s requested=%s",
			statestore.ErrPolicyChanged,
			policy.Revision,
			reservation.PolicyRevision,
		)
	}

	existing, err := scanLease(tx.QueryRowContext(ctx, `
SELECT lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
       placement_trust_domain, placement_authority_principal, placement_decision_id, placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
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
		attempt, attemptErr := scanAttempt(tx.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, existing.AttemptID.String()))
		if errors.Is(attemptErr, sql.ErrNoRows) {
			return statestore.LeaseReservationResult{}, fmt.Errorf(
				"%w: replayed lease %s has no current Attempt %s",
				statestore.ErrConflict,
				existing.ID,
				existing.AttemptID,
			)
		}
		if attemptErr != nil {
			return statestore.LeaseReservationResult{}, attemptErr
		}
		if !attemptCanReplayLease(attempt, existing) {
			return statestore.LeaseReservationResult{}, fmt.Errorf(
				"%w: attempt %s no longer owns replayed lease %s",
				statestore.ErrConflict,
				existing.AttemptID,
				existing.ID,
			)
		}
		return statestore.LeaseReservationResult{Lease: existing, Replayed: true}, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return statestore.LeaseReservationResult{}, err
	}

	attempt, err := scanAttempt(tx.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, reservation.AttemptID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, reservation.AttemptID)
	}
	if err != nil {
		return statestore.LeaseReservationResult{}, err
	}
	if attempt.TaskID != reservation.TaskID ||
		attempt.TaskIRDigest != reservation.TaskIRDigest ||
		attempt.WorkerIdentity != reservation.WorkerIdentity ||
		attempt.Phase != attemptstate.AttemptPhasePlacing ||
		attempt.LeaseID != "" {
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: attempt %s changed before new lease reservation",
			statestore.ErrConflict,
			reservation.AttemptID,
		)
	}
	if attempt.Revision != reservation.ExpectedAttemptRevision {
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: attempt %s is at revision %d, expected %d",
			statestore.ErrConflict,
			reservation.AttemptID,
			attempt.Revision,
			reservation.ExpectedAttemptRevision,
		)
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
SELECT l.lease_id
FROM leases l
LEFT JOIN lease_emissions e ON e.lease_id = l.lease_id
WHERE l.task_id = ?
  AND l.expires_at_ns > ?
  AND (e.status IS NULL OR e.status <> 'aborted')
ORDER BY l.fence_epoch DESC
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
		ID:                          reservation.LeaseID,
		TaskID:                      reservation.TaskID,
		TaskIRDigest:                reservation.TaskIRDigest,
		AttemptID:                   reservation.AttemptID,
		WorkerIdentity:              reservation.WorkerIdentity,
		PlacementTrustDomain:        reservation.PlacementTrustDomain,
		PlacementAuthorityPrincipal: reservation.PlacementAuthorityPrincipal,
		PlacementDecisionID:         reservation.PlacementDecisionID,
		PlacementPayloadDigest:      reservation.PlacementPayloadDigest,
		PlacementExpiresAt:          reservation.PlacementExpiresAt,
		PolicyRevision:              reservation.PolicyRevision,
		PolicyEpoch:                 reservation.ExpectedPolicyEpoch,
		FenceEpoch:                  fence,
		RenewalSequence:             0,
		IssuedAt:                    reservation.IssuedAt,
		ExpiresAt:                   reservation.ExpiresAt,
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
	placement_trust_domain, placement_authority_principal, placement_decision_id, placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
	fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		int64(lease.PolicyEpoch),
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

func attemptCanReplayLease(attempt attemptstate.AttemptRecord, lease leasestate.LeaseRecord) bool {
	if attempt.TaskID != lease.TaskID ||
		attempt.TaskIRDigest != lease.TaskIRDigest ||
		attempt.WorkerIdentity != lease.WorkerIdentity ||
		attempt.LeaseID != lease.ID {
		return false
	}
	switch attempt.Phase {
	case attemptstate.AttemptPhaseAssigned,
		attemptstate.AttemptPhaseDispatched,
		attemptstate.AttemptPhaseClaimed,
		attemptstate.AttemptPhaseVerifying:
		return true
	default:
		return false
	}
}

func (s *Store) LoadLease(ctx context.Context, id ids.LeaseID) (leasestate.LeaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return leasestate.LeaseRecord{}, err
	}
	lease, err := scanLease(s.db.QueryRowContext(ctx, `
SELECT lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
       placement_trust_domain, placement_authority_principal, placement_decision_id, placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
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
		&c.policyEpoch,
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
	id                          string
	taskID                      string
	taskIRDigest                string
	attemptID                   string
	workerIdentity              string
	placementTrustDomain        string
	placementAuthorityPrincipal string
	placementDecisionID         string
	placementPayloadDigest      string
	placementExpiresNS          int64
	policyRevision              string
	policyEpoch                 int64
	fenceEpoch                  int64
	renewalSequence             int64
	issuedNS                    int64
	expiresNS                   int64
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
	if c.policyEpoch < 0 || c.fenceEpoch < 1 || c.renewalSequence < 0 {
		return leasestate.LeaseRecord{}, corruptLease(c.id, errors.New("negative or zero lease sequence"))
	}
	lease, err := leasestate.New(leasestate.Config{
		ID:                          leaseID,
		TaskID:                      taskID,
		TaskIRDigest:                taskIR,
		AttemptID:                   attemptID,
		WorkerIdentity:              c.workerIdentity,
		PlacementTrustDomain:        c.placementTrustDomain,
		PlacementAuthorityPrincipal: c.placementAuthorityPrincipal,
		PlacementDecisionID:         c.placementDecisionID,
		PlacementPayloadDigest:      placementDigest,
		PlacementExpiresAt:          time.Unix(0, c.placementExpiresNS).UTC(),
		PolicyRevision:              c.policyRevision,
		PolicyEpoch:                 uint64(c.policyEpoch),
		FenceEpoch:                  uint64(c.fenceEpoch),
		RenewalSequence:             uint64(c.renewalSequence),
		IssuedAt:                    time.Unix(0, c.issuedNS).UTC(),
		ExpiresAt:                   time.Unix(0, c.expiresNS).UTC(),
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
