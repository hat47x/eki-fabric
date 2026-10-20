package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.ResultClaimStore = (*Store)(nil)

func (s *Store) LoadResultClaim(ctx context.Context, id ids.AttemptID) (resultclaim.Claim, bool, error) {
	if err := ctx.Err(); err != nil {
		return resultclaim.Claim{}, false, err
	}
	row := s.db.QueryRowContext(ctx, `
SELECT task_id, task_ir_digest, attempt_id, worker_identity, lease_id, fence_epoch,
       work_order_decision, executor_digest, nonce, status, exit_code, output_bytes,
       started_at_ns, ended_at_ns
FROM result_claims WHERE attempt_id = ?`, id.String())

	var (
		taskID, taskIR, attemptID, worker, leaseID, workOrder, executor, status string
		fence                                                                   uint64
		nonce                                                                   []byte
		exitCode                                                                int
		outputBytes                                                             uint64
		startedNS, endedNS                                                      int64
	)
	err := row.Scan(&taskID, &taskIR, &attemptID, &worker, &leaseID, &fence,
		&workOrder, &executor, &nonce, &status, &exitCode, &outputBytes, &startedNS, &endedNS)
	if errors.Is(err, sql.ErrNoRows) {
		return resultclaim.Claim{}, false, nil
	}
	if err != nil {
		return resultclaim.Claim{}, false, fmt.Errorf("load ResultClaim %s: %w", id, err)
	}
	claim, err := buildClaim(taskID, taskIR, attemptID, worker, leaseID, fence, workOrder, executor,
		nonce, status, exitCode, outputBytes, startedNS, endedNS)
	if err != nil {
		return resultclaim.Claim{}, false, fmt.Errorf("load ResultClaim %s: %w", id, err)
	}
	return claim, true, nil
}

func (s *Store) ApplyResultClaim(ctx context.Context, acceptance statestore.ResultClaimAcceptance) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !acceptance.Claim.Valid() {
		return fmt.Errorf("%w: claim is not constructed", resultclaim.ErrInvalidClaim)
	}
	if !acceptance.Record && !acceptance.ApplyTransition {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("apply ResultClaim for %s: %w", acceptance.Claim.AttemptID(), err)
	}
	defer func() { _ = tx.Rollback() }()

	claim := acceptance.Claim
	attemptID := claim.AttemptID().String()

	if acceptance.Record {
		var digest string
		err := tx.QueryRowContext(ctx, `SELECT claim_digest FROM result_claims WHERE attempt_id = ?`, attemptID).Scan(&digest)
		switch {
		case err == nil:
			if digest == claim.Digest().Hex() {
				return fmt.Errorf("%w: ResultClaim for %s", statestore.ErrAlreadyExists, attemptID)
			}
			return fmt.Errorf("%w: a different ResultClaim exists for %s", statestore.ErrConflict, attemptID)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("check existing ResultClaim %s: %w", attemptID, err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO result_claims (
	attempt_id, task_id, task_ir_digest, worker_identity, lease_id, fence_epoch,
	work_order_decision, executor_digest, nonce, status, exit_code, output_bytes,
	started_at_ns, ended_at_ns, claim_digest, recorded_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			attemptID,
			claim.TaskID().String(),
			claim.TaskIRDigest().Hex(),
			claim.WorkerIdentity(),
			claim.LeaseID().String(),
			int64(claim.FenceEpoch()),
			claim.WorkOrderDecision(),
			claim.ExecutorDigest().Hex(),
			claim.Nonce(),
			string(claim.Status()),
			int64(claim.ExitCode()),
			int64(claim.OutputBytes()),
			claim.StartedAt().UnixNano(),
			claim.EndedAt().UnixNano(),
			claim.Digest().Hex(),
			time.Now().UTC().UnixNano(),
		); err != nil {
			return fmt.Errorf("insert ResultClaim %s: %w", attemptID, err)
		}
	}

	if acceptance.ApplyTransition {
		current, err := scanAttempt(tx.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, attemptID))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, attemptID)
		}
		if err != nil {
			return fmt.Errorf("load attempt %s for ResultClaim: %w", attemptID, err)
		}
		if current.Revision != acceptance.ExpectedAttemptRevision {
			return fmt.Errorf("%w: attempt %s revision %d", statestore.ErrConflict, attemptID, current.Revision)
		}
		if err := attemptstate.ValidateTransition(current, acceptance.NextAttempt); err != nil {
			return fmt.Errorf("%w: %v", statestore.ErrConflict, err)
		}
		res, err := tx.ExecContext(ctx, `
UPDATE attempts
SET phase = ?, close_reason = ?, revision = ?, updated_at_ns = ?
WHERE attempt_id = ? AND revision = ?`,
			string(acceptance.NextAttempt.Phase),
			string(acceptance.NextAttempt.CloseReason),
			int64(acceptance.NextAttempt.Revision),
			acceptance.NextAttempt.UpdatedAt.UTC().UnixNano(),
			attemptID,
			int64(acceptance.ExpectedAttemptRevision),
		)
		if err != nil {
			return fmt.Errorf("update attempt %s for ResultClaim: %w", attemptID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("update attempt %s for ResultClaim: %w", attemptID, err)
		}
		if n == 0 {
			return fmt.Errorf("%w: attempt %s revision changed", statestore.ErrConflict, attemptID)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ResultClaim for %s: %w", attemptID, err)
	}
	return nil
}

func buildClaim(taskID, taskIR, attemptID, worker, leaseID string, fence uint64,
	workOrder, executor string, nonce []byte, status string, exitCode int, outputBytes uint64,
	startedNS, endedNS int64) (resultclaim.Claim, error) {
	parsedTaskID, err := ids.ParseTaskID(taskID)
	if err != nil {
		return resultclaim.Claim{}, err
	}
	parsedAttemptID, err := ids.ParseAttemptID(attemptID)
	if err != nil {
		return resultclaim.Claim{}, err
	}
	parsedLeaseID, err := ids.ParseLeaseID(leaseID)
	if err != nil {
		return resultclaim.Claim{}, err
	}
	taskIRDigest, err := contract.ParseSHA256(taskIR)
	if err != nil {
		return resultclaim.Claim{}, err
	}
	executorDigest, err := contract.ParseSHA256(executor)
	if err != nil {
		return resultclaim.Claim{}, err
	}
	return resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: parsedTaskID, TaskIRDigest: taskIRDigest, AttemptID: parsedAttemptID,
		WorkerIdentity: worker, LeaseID: parsedLeaseID, FenceEpoch: fence,
		WorkOrderDecision: workOrder, ExecutorDigest: executorDigest, Nonce: nonce,
		Status: resultclaim.Status(status), ExitCode: exitCode, OutputBytes: outputBytes,
		StartedAt: time.Unix(0, startedNS).UTC(), EndedAt: time.Unix(0, endedNS).UTC(),
	})
}
