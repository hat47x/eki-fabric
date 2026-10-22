package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workorderemission"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// CommitWorkOrderEmission linearizes one WOR-1 WorkOrder in one transaction.
// The schema trigger work_order_emissions_emitted_guard repeats the same
// currentness checks for writes that bypass this adapter.
func (s *Store) CommitWorkOrderEmission(ctx context.Context, commit statestore.WorkOrderEmissionCommit) (statestore.WorkOrderEmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	if err := commit.Validate(); err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("commit WorkOrder emission %s: %w", commit.LeaseID, err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := scanWorkOrderEmission(tx.QueryRowContext(ctx, workOrderEmissionSelect+` WHERE lease_id = ?`, commit.LeaseID.String()))
	switch {
	case err == nil:
		if existing.Status() == workorderemission.StatusAborted {
			return statestore.WorkOrderEmissionResult{}, statestore.ErrWorkOrderEmissionAborted
		}
		if !commit.Matches(existing) {
			return statestore.WorkOrderEmissionResult{}, statestore.ErrWorkOrderEmissionConflict
		}
		return statestore.WorkOrderEmissionResult{Record: existing, Replayed: true}, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return statestore.WorkOrderEmissionResult{}, err
	}

	other, err := scanWorkOrderEmission(tx.QueryRowContext(
		ctx,
		workOrderEmissionSelect+` WHERE status = 'emitted' AND trust_domain = ? AND authority_principal = ? AND decision_id = ?`,
		commit.TrustDomain,
		commit.AuthorityPrincipal,
		commit.DecisionID,
	))
	switch {
	case err == nil:
		if other.LeaseID() != commit.LeaseID {
			return statestore.WorkOrderEmissionResult{}, statestore.ErrWorkOrderEmissionConflict
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return statestore.WorkOrderEmissionResult{}, err
	}

	lease, err := scanLease(tx.QueryRowContext(ctx, `
SELECT lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
       placement_trust_domain, placement_authority_principal, placement_decision_id, placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
       fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
FROM leases WHERE lease_id = ?`, commit.LeaseID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, commit.LeaseID)
	}
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	emitted, err := scanLeaseEmission(tx.QueryRowContext(ctx, leaseEmissionSelect+` WHERE lease_id = ?`, lease.ID.String()))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !commit.LeaseEmissionMatches(emitted)) {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: Lease %s is not the EMITTED Lease Decision the WorkOrder references", statestore.ErrConflict, lease.ID)
	}
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	if !lease.PolicyFenced() || lease.PolicyEpoch != commit.ExpectedPolicyEpoch || lease.PolicyRevision != commit.ExpectedPolicyRevision {
		return statestore.WorkOrderEmissionResult{}, statestore.ErrPolicyChanged
	}
	if lease.WorkerIdentity != commit.WorkerIdentity {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: WorkOrder Worker differs from the Lease Worker", statestore.ErrConflict)
	}
	var lastFence int64
	if err := tx.QueryRowContext(ctx, `SELECT last_fence_epoch FROM lease_fences WHERE task_id = ?`, lease.TaskID.String()).Scan(&lastFence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: Task %s has no fence", ErrCorruptRecord, lease.TaskID)
		}
		return statestore.WorkOrderEmissionResult{}, err
	}
	if lease.FenceEpoch != commit.ExpectedFenceEpoch || uint64(lastFence) != lease.FenceEpoch {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: Lease fence is no longer the latest for Task", statestore.ErrConflict)
	}
	if commit.CommittedAt.Before(lease.IssuedAt) || !commit.CommittedAt.Before(lease.ExpiresAt) ||
		commit.ExpiresAt.After(lease.ExpiresAt) {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: WorkOrder window is outside the live Lease", statestore.ErrConflict)
	}

	task, err := scanTask(tx.QueryRowContext(ctx, `
SELECT task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns
FROM tasks WHERE task_id = ?`, lease.TaskID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, lease.TaskID)
	}
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	if task.Revision != commit.ExpectedTaskRevision ||
		task.TaskIRDigest != lease.TaskIRDigest ||
		task.CancellationRequested ||
		(task.Phase != taskstate.TaskPhaseReady && task.Phase != taskstate.TaskPhaseActive) {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: Task changed before WorkOrder emission", statestore.ErrConflict)
	}

	attempt, err := scanAttempt(tx.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, lease.AttemptID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, lease.AttemptID)
	}
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	if attempt.Revision != commit.ExpectedAttemptRevision ||
		attempt.TaskID != lease.TaskID ||
		attempt.TaskIRDigest != lease.TaskIRDigest ||
		attempt.WorkerIdentity != lease.WorkerIdentity ||
		attempt.LeaseID != lease.ID ||
		attempt.Phase != attemptstate.AttemptPhaseAssigned {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: Attempt changed before WorkOrder emission", statestore.ErrConflict)
	}

	policy, err := scanPolicy(tx.QueryRowContext(ctx, `
SELECT h.epoch, h.revision, h.status, h.activated_at_ns, h.revoked_at_ns, h.revoke_reason
FROM policy_current c
JOIN policy_history h ON h.epoch = c.epoch
WHERE c.singleton = 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.WorkOrderEmissionResult{}, statestore.ErrPolicyUnavailable
	}
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	if policy.Epoch != commit.ExpectedPolicyEpoch ||
		policy.Status != policystate.StatusActive ||
		policy.Revision != lease.PolicyRevision {
		return statestore.WorkOrderEmissionResult{}, statestore.ErrPolicyChanged
	}

	record, err := commit.Record()
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO work_order_emissions (
	lease_id, status, decision_id, work_order_id, worker_identity, trust_domain,
	authority_principal, key_id, algorithm, payload_digest, envelope_digest,
	signed_envelope, policy_revision, policy_epoch, fence_epoch, expires_at_ns,
	committed_at_ns, abort_reason_code, abort_reason
) VALUES (?, 'emitted', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
		record.LeaseID().String(),
		record.DecisionID(),
		record.WorkOrderID(),
		record.WorkerIdentity(),
		record.TrustDomain(),
		record.AuthorityPrincipal(),
		string(record.KeyID()),
		string(record.Algorithm()),
		record.PayloadDigest().Hex(),
		record.EnvelopeDigest().Hex(),
		record.SignedEnvelope(),
		record.PolicyRevision(),
		int64(record.PolicyEpoch()),
		int64(record.FenceEpoch()),
		record.ExpiresAt().UnixNano(),
		record.CommittedAt().UnixNano(),
	)
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("insert WorkOrder emission %s: %w", lease.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("commit WorkOrder emission %s: %w", lease.ID, err)
	}
	return statestore.WorkOrderEmissionResult{Record: record}, nil
}

// AbortWorkOrderEmission records that no WorkOrder will be signed for a Lease.
// It writes only work_order_emissions.
func (s *Store) AbortWorkOrderEmission(ctx context.Context, abort statestore.WorkOrderEmissionAbort) (statestore.WorkOrderEmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	if err := abort.Validate(); err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("abort WorkOrder emission %s: %w", abort.LeaseID, err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := scanWorkOrderEmission(tx.QueryRowContext(ctx, workOrderEmissionSelect+` WHERE lease_id = ?`, abort.LeaseID.String()))
	switch {
	case err == nil:
		if existing.Status() == workorderemission.StatusEmitted {
			return statestore.WorkOrderEmissionResult{}, statestore.ErrWorkOrderEmissionConflict
		}
		return statestore.WorkOrderEmissionResult{Record: existing, Replayed: true}, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return statestore.WorkOrderEmissionResult{}, err
	}

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM leases WHERE lease_id = ?`, abort.LeaseID.String()).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, abort.LeaseID)
		}
		return statestore.WorkOrderEmissionResult{}, err
	}
	record, err := workorderemission.NewAborted(workorderemission.AbortedConfig{
		LeaseID: abort.LeaseID, AbortReason: abort.AbortReason, Reason: abort.Reason, CommittedAt: abort.CommittedAt,
	})
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO work_order_emissions (
	lease_id, status, committed_at_ns, abort_reason_code, abort_reason
) VALUES (?, 'aborted', ?, ?, ?)`,
		record.LeaseID().String(),
		record.CommittedAt().UnixNano(),
		string(record.AbortReason()),
		record.Reason(),
	)
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("insert WorkOrder abort %s: %w", abort.LeaseID, err)
	}
	if err := tx.Commit(); err != nil {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("commit WorkOrder abort %s: %w", abort.LeaseID, err)
	}
	return statestore.WorkOrderEmissionResult{Record: record}, nil
}

func (s *Store) LoadWorkOrderEmission(ctx context.Context, id ids.LeaseID) (workorderemission.Record, error) {
	if err := ctx.Err(); err != nil {
		return workorderemission.Record{}, err
	}
	record, err := scanWorkOrderEmission(s.db.QueryRowContext(ctx, workOrderEmissionSelect+` WHERE lease_id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return workorderemission.Record{}, fmt.Errorf("%w: WorkOrder emission %s", statestore.ErrNotFound, id)
	}
	return record, err
}

const workOrderEmissionSelect = `
SELECT lease_id, status, decision_id, work_order_id, worker_identity, trust_domain,
       authority_principal, key_id, algorithm, payload_digest, envelope_digest,
       signed_envelope, policy_revision, policy_epoch, fence_epoch, expires_at_ns,
       committed_at_ns, abort_reason_code, abort_reason
FROM work_order_emissions`

func scanWorkOrderEmission(row rowScanner) (workorderemission.Record, error) {
	var (
		leaseID     string
		status      string
		decisionID  sql.NullString
		workOrderID sql.NullString
		worker      sql.NullString
		trustDomain sql.NullString
		principal   sql.NullString
		keyID       sql.NullString
		algorithm   sql.NullString
		payloadHex  sql.NullString
		envelopeHex sql.NullString
		signed      []byte
		policyRev   sql.NullString
		policyEpoch sql.NullInt64
		fenceEpoch  sql.NullInt64
		expiresNS   sql.NullInt64
		committedNS int64
		reasonCode  string
		reason      string
	)
	if err := row.Scan(
		&leaseID, &status, &decisionID, &workOrderID, &worker, &trustDomain,
		&principal, &keyID, &algorithm, &payloadHex, &envelopeHex,
		&signed, &policyRev, &policyEpoch, &fenceEpoch, &expiresNS,
		&committedNS, &reasonCode, &reason,
	); err != nil {
		return workorderemission.Record{}, err
	}
	id, err := ids.ParseLeaseID(leaseID)
	if err != nil {
		return workorderemission.Record{}, fmt.Errorf("%w: WorkOrder emission %s: %v", ErrCorruptRecord, leaseID, err)
	}
	committedAt := time.Unix(0, committedNS).UTC()
	switch workorderemission.Status(status) {
	case workorderemission.StatusEmitted:
		if !decisionID.Valid || !workOrderID.Valid || !worker.Valid || !trustDomain.Valid ||
			!principal.Valid || !keyID.Valid || !algorithm.Valid || !payloadHex.Valid ||
			!envelopeHex.Valid || !policyRev.Valid || !policyEpoch.Valid || policyEpoch.Int64 < 1 ||
			!fenceEpoch.Valid || fenceEpoch.Int64 < 1 || !expiresNS.Valid {
			return workorderemission.Record{}, fmt.Errorf("%w: emitted WorkOrder %s has NULL security fields", ErrCorruptRecord, leaseID)
		}
		payloadDigest, err := contract.ParseSHA256(payloadHex.String)
		if err != nil {
			return workorderemission.Record{}, fmt.Errorf("%w: WorkOrder emission %s payload digest: %v", ErrCorruptRecord, leaseID, err)
		}
		envelopeDigest, err := contract.ParseSHA256(envelopeHex.String)
		if err != nil {
			return workorderemission.Record{}, fmt.Errorf("%w: WorkOrder emission %s envelope digest: %v", ErrCorruptRecord, leaseID, err)
		}
		record, err := workorderemission.NewEmitted(workorderemission.EmittedConfig{
			LeaseID: id, DecisionID: decisionID.String, WorkOrderID: workOrderID.String,
			WorkerIdentity: worker.String, TrustDomain: trustDomain.String,
			AuthorityPrincipal: principal.String, KeyID: decisiontrust.KeyID(keyID.String),
			Algorithm: decisiontrust.Algorithm(algorithm.String), PayloadDigest: payloadDigest,
			EnvelopeDigest: envelopeDigest, SignedEnvelope: signed,
			PolicyRevision: policyRev.String, PolicyEpoch: uint64(policyEpoch.Int64),
			FenceEpoch: uint64(fenceEpoch.Int64), ExpiresAt: time.Unix(0, expiresNS.Int64).UTC(),
			CommittedAt: committedAt,
		})
		if err != nil {
			return workorderemission.Record{}, fmt.Errorf("%w: WorkOrder emission %s: %v", ErrCorruptRecord, leaseID, err)
		}
		return record, nil
	case workorderemission.StatusAborted:
		record, err := workorderemission.NewAborted(workorderemission.AbortedConfig{
			LeaseID: id, AbortReason: workorderemission.AbortReason(reasonCode), Reason: reason, CommittedAt: committedAt,
		})
		if err != nil {
			return workorderemission.Record{}, fmt.Errorf("%w: WorkOrder abort %s: %v", ErrCorruptRecord, leaseID, err)
		}
		return record, nil
	default:
		return workorderemission.Record{}, fmt.Errorf("%w: WorkOrder emission %s has unknown status %q", ErrCorruptRecord, leaseID, status)
	}
}

var _ statestore.WorkOrderEmissionStore = (*Store)(nil)
