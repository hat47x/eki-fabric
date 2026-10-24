package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/acceptedresult"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.ResultAcceptanceStore = (*Store)(nil)

const acceptedResultColumns = `task_id, attempt_id, claim_digest, output_digest, assurance_level,
	policy_digest, verifiers, accepted_at_ns`

func (s *Store) LoadAcceptedResult(ctx context.Context, id ids.TaskID) (acceptedresult.Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return acceptedresult.Record{}, false, err
	}
	record, err := scanAcceptedResult(s.db.QueryRowContext(ctx,
		`SELECT `+acceptedResultColumns+` FROM accepted_results WHERE task_id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return acceptedresult.Record{}, false, nil
	}
	if err != nil {
		return acceptedresult.Record{}, false, fmt.Errorf("load accepted result %s: %w", id, err)
	}
	return record, true, nil
}

func (s *Store) CommitAcceptedResult(ctx context.Context, commit statestore.ResultAcceptanceCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := commit.Validate(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("commit accepted result for %s: %w", commit.Result.TaskID(), err)
	}
	defer func() { _ = tx.Rollback() }()

	taskID, attemptID := commit.Result.TaskID(), commit.Result.AttemptID()

	existing, err := scanAcceptedResult(tx.QueryRowContext(ctx,
		`SELECT `+acceptedResultColumns+` FROM accepted_results WHERE task_id = ?`, taskID.String()))
	switch {
	case err == nil:
		if existing.Equal(commit.Result) {
			return fmt.Errorf("%w: accepted result for task %s", statestore.ErrAlreadyExists, taskID)
		}
		return fmt.Errorf("%w: task %s already has a different accepted result", statestore.ErrConflict, taskID)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check accepted result %s: %w", taskID, err)
	}

	attempt, err := scanAttempt(tx.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, attemptID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, attemptID)
	}
	if err != nil {
		return fmt.Errorf("load attempt %s for acceptance: %w", attemptID, err)
	}
	if attempt.TaskID != taskID || attempt.Revision != commit.ExpectedAttemptRevision ||
		attempt.Phase != attemptstate.AttemptPhaseVerifying {
		return fmt.Errorf("%w: attempt %s is not in verification at revision %d", statestore.ErrConflict, attemptID, commit.ExpectedAttemptRevision)
	}
	if err := attemptstate.ValidateTransition(attempt, commit.NextAttempt); err != nil {
		return fmt.Errorf("%w: %v", statestore.ErrConflict, err)
	}

	var (
		status, claimDigest, outputDigest string
		claimFence                        int64
	)
	err = tx.QueryRowContext(ctx,
		`SELECT status, claim_digest, output_digest, fence_epoch FROM result_claims WHERE attempt_id = ?`, attemptID.String()).
		Scan(&status, &claimDigest, &outputDigest, &claimFence)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (status != string(resultclaim.StatusCompleted) ||
			claimDigest != commit.Result.ClaimDigest().Hex() || outputDigest != commit.Result.OutputDigest().Hex())) {
		return fmt.Errorf("%w: attempt %s has no completed claim matching the accepted result", statestore.ErrConflict, attemptID)
	}
	if err != nil {
		return fmt.Errorf("load ResultClaim %s for acceptance: %w", attemptID, err)
	}

	// The Attempt must still hold the Task's latest fence. A later Lease means
	// the Task was placed again, so this Attempt's result is a stale late
	// result and must not complete the Task (V3). The schema trigger
	// accepted_results_fence_guard repeats this check.
	var leaseAttempt string
	var leaseFence, lastFence int64
	err = tx.QueryRowContext(ctx, `
SELECT l.attempt_id, l.fence_epoch, lf.last_fence_epoch
FROM leases l JOIN lease_fences lf ON lf.task_id = l.task_id
WHERE l.lease_id = ?`, attempt.LeaseID.String()).Scan(&leaseAttempt, &leaseFence, &lastFence)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (leaseAttempt != attemptID.String() ||
		leaseFence != claimFence || lastFence != leaseFence)) {
		return fmt.Errorf("%w: attempt %s no longer holds the latest fence of task %s", statestore.ErrConflict, attemptID, taskID)
	}
	if err != nil {
		return fmt.Errorf("load Lease fence of %s for acceptance: %w", attemptID, err)
	}

	task, err := scanTask(tx.QueryRowContext(ctx, `
SELECT task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns
FROM tasks WHERE task_id = ?`, taskID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: task %s", statestore.ErrNotFound, taskID)
	}
	if err != nil {
		return fmt.Errorf("load task %s for acceptance: %w", taskID, err)
	}
	if task.Revision != commit.ExpectedTaskRevision || task.CancellationRequested ||
		task.TaskIRDigest != commit.NextTask.TaskIRDigest || !task.Phase.AcceptsResult() {
		return fmt.Errorf("%w: task %s cannot complete at revision %d", statestore.ErrConflict, taskID, commit.ExpectedTaskRevision)
	}

	res, err := tx.ExecContext(ctx, `
UPDATE attempts
SET phase = ?, close_reason = ?, revision = ?, updated_at_ns = ?
WHERE attempt_id = ? AND revision = ?`,
		string(commit.NextAttempt.Phase), string(commit.NextAttempt.CloseReason),
		int64(commit.NextAttempt.Revision), commit.NextAttempt.UpdatedAt.UTC().UnixNano(),
		attemptID.String(), int64(commit.ExpectedAttemptRevision))
	if err != nil {
		return fmt.Errorf("close attempt %s for acceptance: %w", attemptID, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%w: attempt %s revision changed", statestore.ErrConflict, attemptID)
	}

	res, err = tx.ExecContext(ctx, `
UPDATE tasks
SET phase = ?, terminal_outcome = ?, cancellation_requested = ?, revision = ?, updated_at_ns = ?
WHERE task_id = ? AND revision = ? AND task_ir_digest = ?`,
		string(commit.NextTask.Phase), string(commit.NextTask.TerminalOutcome),
		boolToInt(commit.NextTask.CancellationRequested), int64(commit.NextTask.Revision),
		commit.NextTask.UpdatedAt.UTC().UnixNano(),
		taskID.String(), int64(commit.ExpectedTaskRevision), commit.NextTask.TaskIRDigest.Hex())
	if err != nil {
		return fmt.Errorf("complete task %s for acceptance: %w", taskID, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%w: task %s revision changed", statestore.ErrConflict, taskID)
	}

	verifiers, err := json.Marshal(commit.Result.Verifiers())
	if err != nil {
		return fmt.Errorf("encode verifiers for %s: %w", taskID, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO accepted_results (
	task_id, attempt_id, claim_digest, output_digest, assurance_level,
	policy_digest, verifiers, accepted_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID.String(), attemptID.String(), commit.Result.ClaimDigest().Hex(),
		commit.Result.OutputDigest().Hex(), string(commit.Result.Level()),
		commit.Result.PolicyDigest().Hex(), string(verifiers),
		commit.Result.AcceptedAt().UnixNano()); err != nil {
		return fmt.Errorf("insert accepted result %s: %w", taskID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit accepted result for %s: %w", taskID, err)
	}
	return nil
}

func scanAcceptedResult(row rowScanner) (acceptedresult.Record, error) {
	var (
		taskID, attemptID, claimHex, outputHex, level, policyHex, verifiersJSON string
		acceptedNS                                                              int64
	)
	if err := row.Scan(&taskID, &attemptID, &claimHex, &outputHex, &level, &policyHex, &verifiersJSON, &acceptedNS); err != nil {
		return acceptedresult.Record{}, err
	}
	corrupt := func(err error) error {
		return fmt.Errorf("%w: accepted result %s: %v", ErrCorruptRecord, taskID, err)
	}
	claimDigest, err := contract.ParseSHA256(claimHex)
	if err != nil {
		return acceptedresult.Record{}, corrupt(err)
	}
	outputDigest, err := contract.ParseSHA256(outputHex)
	if err != nil {
		return acceptedresult.Record{}, corrupt(err)
	}
	policyDigest, err := contract.ParseSHA256(policyHex)
	if err != nil {
		return acceptedresult.Record{}, corrupt(err)
	}
	var verifiers []string
	if err := json.Unmarshal([]byte(verifiersJSON), &verifiers); err != nil {
		return acceptedresult.Record{}, corrupt(err)
	}
	record, err := acceptedresult.New(acceptedresult.Config{
		TaskID: ids.TaskID(taskID), AttemptID: ids.AttemptID(attemptID),
		ClaimDigest: claimDigest, OutputDigest: outputDigest,
		Level: resultaccept.AssuranceLevel(level), PolicyDigest: policyDigest,
		Verifiers: verifiers, AcceptedAt: time.Unix(0, acceptedNS).UTC(),
	})
	if err != nil {
		return acceptedresult.Record{}, corrupt(err)
	}
	return record, nil
}
