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
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) CommitLeaseEmission(ctx context.Context, commit statestore.LeaseEmissionCommit) (statestore.LeaseEmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	if err := commit.Validate(); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("commit Lease emission %s: %w", commit.LeaseID, err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := scanLeaseEmission(tx.QueryRowContext(ctx, leaseEmissionSelect+` WHERE lease_id = ?`, commit.LeaseID.String()))
	switch {
	case err == nil:
		if existing.Status() == leaseemission.StatusAborted {
			return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionAborted
		}
		if !commit.Matches(existing) {
			return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionConflict
		}
		return statestore.LeaseEmissionResult{Record: existing, Replayed: true}, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return statestore.LeaseEmissionResult{}, err
	}

	other, err := scanLeaseEmission(tx.QueryRowContext(
		ctx,
		leaseEmissionSelect+` WHERE status = 'emitted' AND trust_domain = ? AND authority_principal = ? AND decision_id = ?`,
		commit.TrustDomain,
		commit.AuthorityPrincipal,
		commit.DecisionID,
	))
	switch {
	case err == nil:
		if other.LeaseID() != commit.LeaseID {
			return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionConflict
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return statestore.LeaseEmissionResult{}, err
	}

	lease, err := scanLease(tx.QueryRowContext(ctx, `
SELECT lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
       placement_trust_domain, placement_authority_principal, placement_decision_id, placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
       fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
FROM leases WHERE lease_id = ?`, commit.LeaseID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, commit.LeaseID)
	}
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	if !lease.PolicyFenced() || lease.PolicyEpoch != commit.ExpectedPolicyEpoch {
		return statestore.LeaseEmissionResult{}, statestore.ErrPolicyChanged
	}
	if commit.CommittedAt.Before(lease.IssuedAt) || !commit.CommittedAt.Before(lease.ExpiresAt) {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: Lease is not live at emission commit", statestore.ErrConflict)
	}

	task, err := scanTask(tx.QueryRowContext(ctx, `
SELECT task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns
FROM tasks WHERE task_id = ?`, lease.TaskID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, lease.TaskID)
	}
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	if task.Revision != commit.ExpectedTaskRevision ||
		task.TaskIRDigest != lease.TaskIRDigest ||
		task.CancellationRequested ||
		(task.Phase != taskstate.TaskPhaseReady && task.Phase != taskstate.TaskPhaseActive) {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: Task changed before Lease emission", statestore.ErrConflict)
	}

	attempt, err := scanAttempt(tx.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, lease.AttemptID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, lease.AttemptID)
	}
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	if attempt.Revision != commit.ExpectedAttemptRevision ||
		attempt.TaskID != lease.TaskID ||
		attempt.TaskIRDigest != lease.TaskIRDigest ||
		attempt.WorkerIdentity != lease.WorkerIdentity ||
		attempt.LeaseID != lease.ID ||
		attempt.Phase != attemptstate.AttemptPhaseAssigned {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: Attempt changed before Lease emission", statestore.ErrConflict)
	}

	policy, err := scanPolicy(tx.QueryRowContext(ctx, `
SELECT h.epoch, h.revision, h.status, h.activated_at_ns, h.revoked_at_ns, h.revoke_reason
FROM policy_current c
JOIN policy_history h ON h.epoch = c.epoch
WHERE c.singleton = 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.LeaseEmissionResult{}, statestore.ErrPolicyUnavailable
	}
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	if policy.Epoch != commit.ExpectedPolicyEpoch ||
		policy.Status != policystate.StatusActive ||
		policy.Revision != lease.PolicyRevision {
		return statestore.LeaseEmissionResult{}, statestore.ErrPolicyChanged
	}

	record, err := leaseemission.NewEmitted(leaseemission.EmittedConfig{
		LeaseID:            lease.ID,
		DecisionID:         commit.DecisionID,
		TrustDomain:        commit.TrustDomain,
		AuthorityPrincipal: commit.AuthorityPrincipal,
		KeyID:              commit.KeyID,
		Algorithm:          commit.Algorithm,
		PayloadDigest:      commit.PayloadDigest,
		EnvelopeDigest:     commit.EnvelopeDigest,
		SignedEnvelope:     commit.SignedEnvelope,
		PolicyRevision:     lease.PolicyRevision,
		PolicyEpoch:        lease.PolicyEpoch,
		CommittedAt:        commit.CommittedAt,
	})
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO lease_emissions (
	lease_id, status, decision_id, trust_domain, authority_principal, key_id,
	algorithm, payload_digest, envelope_digest, signed_envelope,
	policy_revision, policy_epoch, committed_at_ns, abort_reason, abort_close_reason
) VALUES (?, 'emitted', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
		record.LeaseID().String(),
		record.DecisionID(),
		record.TrustDomain(),
		record.AuthorityPrincipal(),
		string(record.KeyID()),
		string(record.Algorithm()),
		record.PayloadDigest().Hex(),
		record.EnvelopeDigest().Hex(),
		record.SignedEnvelope(),
		record.PolicyRevision(),
		int64(record.PolicyEpoch()),
		record.CommittedAt().UnixNano(),
	)
	if err != nil {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("insert Lease emission %s: %w", lease.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("commit Lease emission %s: %w", lease.ID, err)
	}
	return statestore.LeaseEmissionResult{Record: record}, nil
}

func (s *Store) AbortLeaseEmission(ctx context.Context, abort statestore.LeaseEmissionAbort) (statestore.LeaseEmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	if err := abort.Validate(); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("abort Lease emission %s: %w", abort.LeaseID, err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := scanLeaseEmission(tx.QueryRowContext(ctx, leaseEmissionSelect+` WHERE lease_id = ?`, abort.LeaseID.String()))
	switch {
	case err == nil:
		if existing.Status() == leaseemission.StatusEmitted {
			return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionConflict
		}
		if existing.Reason() != abort.Reason || existing.CloseReason() != string(abort.CloseReason) {
			return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionConflict
		}
		return statestore.LeaseEmissionResult{Record: existing, Replayed: true}, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return statestore.LeaseEmissionResult{}, err
	}

	lease, err := scanLease(tx.QueryRowContext(ctx, `
SELECT lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
       placement_trust_domain, placement_authority_principal, placement_decision_id, placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
       fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
FROM leases WHERE lease_id = ?`, abort.LeaseID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, abort.LeaseID)
	}
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	attempt, err := scanAttempt(tx.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, lease.AttemptID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, lease.AttemptID)
	}
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	if attempt.Revision != abort.ExpectedAttemptRevision ||
		attempt.LeaseID != lease.ID ||
		attempt.WorkerIdentity != lease.WorkerIdentity ||
		attempt.Phase != attemptstate.AttemptPhaseAssigned {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: Attempt changed before Lease abort", statestore.ErrConflict)
	}

	closed := attempt
	closed.Phase = attemptstate.AttemptPhaseClosed
	closed.CloseReason = abort.CloseReason
	closed.Revision++
	closed.UpdatedAt = laterAttemptTime(attempt.UpdatedAt, abort.CommittedAt)
	if err := attemptstate.ValidateTransition(attempt, closed); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	record, err := leaseemission.NewAborted(leaseemission.AbortedConfig{
		LeaseID: lease.ID, CloseReason: string(abort.CloseReason), Reason: abort.Reason, CommittedAt: abort.CommittedAt,
	})
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO lease_emissions (
	lease_id, status, committed_at_ns, abort_reason, abort_close_reason
) VALUES (?, 'aborted', ?, ?, ?)`,
		lease.ID.String(),
		record.CommittedAt().UnixNano(),
		record.Reason(),
		record.CloseReason(),
	)
	if err != nil {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("insert Lease abort %s: %w", lease.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("commit Lease abort %s: %w", lease.ID, err)
	}
	return statestore.LeaseEmissionResult{Record: record}, nil
}

func (s *Store) LoadLeaseEmission(ctx context.Context, id ids.LeaseID) (leaseemission.Record, error) {
	if err := ctx.Err(); err != nil {
		return leaseemission.Record{}, err
	}
	record, err := scanLeaseEmission(s.db.QueryRowContext(ctx, leaseEmissionSelect+` WHERE lease_id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return leaseemission.Record{}, fmt.Errorf("%w: Lease emission %s", statestore.ErrNotFound, id)
	}
	return record, err
}

const leaseEmissionSelect = `
SELECT lease_id, status, decision_id, trust_domain, authority_principal, key_id,
       algorithm, payload_digest, envelope_digest, signed_envelope,
       policy_revision, policy_epoch, committed_at_ns, abort_reason, abort_close_reason
FROM lease_emissions`

func scanLeaseEmission(row rowScanner) (leaseemission.Record, error) {
	var (
		leaseID     string
		status      string
		decisionID  sql.NullString
		trustDomain sql.NullString
		principal   sql.NullString
		keyID       sql.NullString
		algorithm   sql.NullString
		payloadHex  sql.NullString
		envelopeHex sql.NullString
		signed      []byte
		policyRev   sql.NullString
		policyEpoch sql.NullInt64
		committedNS int64
		reason      string
		closeReason string
	)
	if err := row.Scan(
		&leaseID, &status, &decisionID, &trustDomain, &principal, &keyID,
		&algorithm, &payloadHex, &envelopeHex, &signed,
		&policyRev, &policyEpoch, &committedNS, &reason, &closeReason,
	); err != nil {
		return leaseemission.Record{}, err
	}
	id, err := ids.ParseLeaseID(leaseID)
	if err != nil {
		return leaseemission.Record{}, fmt.Errorf("%w: Lease emission %s: %v", ErrCorruptRecord, leaseID, err)
	}
	committedAt := time.Unix(0, committedNS).UTC()
	switch leaseemission.Status(status) {
	case leaseemission.StatusEmitted:
		if !decisionID.Valid || !trustDomain.Valid || !principal.Valid || !keyID.Valid ||
			!algorithm.Valid || !payloadHex.Valid || !envelopeHex.Valid || !policyRev.Valid ||
			!policyEpoch.Valid || policyEpoch.Int64 < 1 {
			return leaseemission.Record{}, fmt.Errorf("%w: emitted Lease %s has NULL security fields", ErrCorruptRecord, leaseID)
		}
		payloadDigest, err := contract.ParseSHA256(payloadHex.String)
		if err != nil {
			return leaseemission.Record{}, fmt.Errorf("%w: Lease emission %s payload digest: %v", ErrCorruptRecord, leaseID, err)
		}
		envelopeDigest, err := contract.ParseSHA256(envelopeHex.String)
		if err != nil {
			return leaseemission.Record{}, fmt.Errorf("%w: Lease emission %s envelope digest: %v", ErrCorruptRecord, leaseID, err)
		}
		record, err := leaseemission.NewEmitted(leaseemission.EmittedConfig{
			LeaseID: id, DecisionID: decisionID.String, TrustDomain: trustDomain.String,
			AuthorityPrincipal: principal.String, KeyID: decisiontrust.KeyID(keyID.String),
			Algorithm: decisiontrust.Algorithm(algorithm.String), PayloadDigest: payloadDigest,
			EnvelopeDigest: envelopeDigest, SignedEnvelope: signed,
			PolicyRevision: policyRev.String, PolicyEpoch: uint64(policyEpoch.Int64), CommittedAt: committedAt,
		})
		if err != nil {
			return leaseemission.Record{}, fmt.Errorf("%w: Lease emission %s: %v", ErrCorruptRecord, leaseID, err)
		}
		return record, nil
	case leaseemission.StatusAborted:
		record, err := leaseemission.NewAborted(leaseemission.AbortedConfig{
			LeaseID: id, CloseReason: closeReason, Reason: reason, CommittedAt: committedAt,
		})
		if err != nil {
			return leaseemission.Record{}, fmt.Errorf("%w: Lease abort %s: %v", ErrCorruptRecord, leaseID, err)
		}
		return record, nil
	default:
		return leaseemission.Record{}, fmt.Errorf("%w: Lease emission %s has unknown status %q", ErrCorruptRecord, leaseID, status)
	}
}

var _ statestore.LeaseEmissionStore = (*Store)(nil)
