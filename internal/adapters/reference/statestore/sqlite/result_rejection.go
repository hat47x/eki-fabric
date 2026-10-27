package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/rejectedresult"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.ResultRejectionStore = (*Store)(nil)

const rejectedResultColumns = `attempt_id, task_id, claim_digest, output_digest, policy_digest,
	reason, dissent, verifiers, excluded, rejected_at_ns`

// The JSON forms of the record's lists. Field names are fixed here so that the
// stored text does not depend on Go struct names.
type dissentRow struct {
	VerifierID     string `json:"verifier"`
	Check          uint8  `json:"check"`
	ObservedDigest string `json:"observed"`
}

type exclusionRow struct {
	VerifierID string `json:"verifier"`
	Reason     string `json:"reason"`
}

func (s *Store) LoadRejectedResult(ctx context.Context, id ids.AttemptID) (rejectedresult.Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return rejectedresult.Record{}, false, err
	}
	record, err := scanRejectedResult(s.db.QueryRowContext(ctx,
		`SELECT `+rejectedResultColumns+` FROM rejected_results WHERE attempt_id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return rejectedresult.Record{}, false, nil
	}
	if err != nil {
		return rejectedresult.Record{}, false, fmt.Errorf("load rejected result %s: %w", id, err)
	}
	return record, true, nil
}

func (s *Store) CommitRejectedResult(ctx context.Context, commit statestore.ResultRejectionCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := commit.Validate(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("commit rejected result for %s: %w", commit.Result.AttemptID(), err)
	}
	defer func() { _ = tx.Rollback() }()

	taskID, attemptID := commit.Result.TaskID(), commit.Result.AttemptID()

	existing, err := scanRejectedResult(tx.QueryRowContext(ctx,
		`SELECT `+rejectedResultColumns+` FROM rejected_results WHERE attempt_id = ?`, attemptID.String()))
	switch {
	case err == nil:
		if existing.Equal(commit.Result) {
			return fmt.Errorf("%w: rejected result for attempt %s", statestore.ErrAlreadyExists, attemptID)
		}
		return fmt.Errorf("%w: attempt %s already has a different rejected result", statestore.ErrConflict, attemptID)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check rejected result %s: %w", attemptID, err)
	}

	attempt, err := scanAttempt(tx.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, attemptID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, attemptID)
	}
	if err != nil {
		return fmt.Errorf("load attempt %s for rejection: %w", attemptID, err)
	}
	if attempt.TaskID != taskID || attempt.Revision != commit.ExpectedAttemptRevision ||
		attempt.Phase != attemptstate.AttemptPhaseVerifying {
		return fmt.Errorf("%w: attempt %s is not in verification at revision %d", statestore.ErrConflict, attemptID, commit.ExpectedAttemptRevision)
	}
	if err := attemptstate.ValidateTransition(attempt, commit.NextAttempt); err != nil {
		return fmt.Errorf("%w: %v", statestore.ErrConflict, err)
	}

	var status, claimDigest, outputDigest string
	err = tx.QueryRowContext(ctx,
		`SELECT status, claim_digest, output_digest FROM result_claims WHERE attempt_id = ?`, attemptID.String()).
		Scan(&status, &claimDigest, &outputDigest)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (status != string(resultclaim.StatusCompleted) ||
			claimDigest != commit.Result.ClaimDigest().Hex() || outputDigest != commit.Result.OutputDigest().Hex())) {
		return fmt.Errorf("%w: attempt %s has no completed claim matching the rejected result", statestore.ErrConflict, attemptID)
	}
	if err != nil {
		return fmt.Errorf("load ResultClaim %s for rejection: %w", attemptID, err)
	}

	// The row is inserted while the Attempt is still verifying, so the schema
	// guard rejected_results_attempt_guard can check it.
	dissent := make([]dissentRow, 0, len(commit.Result.Dissent()))
	for _, d := range commit.Result.Dissent() {
		dissent = append(dissent, dissentRow{VerifierID: d.VerifierID, Check: uint8(d.Check), ObservedDigest: d.ObservedDigest})
	}
	excluded := make([]exclusionRow, 0, len(commit.Result.Excluded()))
	for _, e := range commit.Result.Excluded() {
		excluded = append(excluded, exclusionRow{VerifierID: e.VerifierID, Reason: e.Reason})
	}
	verifiers := commit.Result.Verifiers()
	if verifiers == nil {
		verifiers = []string{}
	}
	dissentJSON, err := json.Marshal(dissent)
	if err != nil {
		return fmt.Errorf("encode dissent for %s: %w", attemptID, err)
	}
	verifiersJSON, err := json.Marshal(verifiers)
	if err != nil {
		return fmt.Errorf("encode verifiers for %s: %w", attemptID, err)
	}
	excludedJSON, err := json.Marshal(excluded)
	if err != nil {
		return fmt.Errorf("encode exclusions for %s: %w", attemptID, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO rejected_results (
	attempt_id, task_id, claim_digest, output_digest, policy_digest,
	reason, dissent, verifiers, excluded, rejected_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attemptID.String(), taskID.String(), commit.Result.ClaimDigest().Hex(),
		commit.Result.OutputDigest().Hex(), commit.Result.PolicyDigest().Hex(),
		string(commit.Result.Reason()), string(dissentJSON), string(verifiersJSON), string(excludedJSON),
		commit.Result.RejectedAt().UnixNano()); err != nil {
		return fmt.Errorf("insert rejected result %s: %w", attemptID, err)
	}

	res, err := tx.ExecContext(ctx, `
UPDATE attempts
SET phase = ?, close_reason = ?, revision = ?, updated_at_ns = ?
WHERE attempt_id = ? AND revision = ?`,
		string(commit.NextAttempt.Phase), string(commit.NextAttempt.CloseReason),
		int64(commit.NextAttempt.Revision), commit.NextAttempt.UpdatedAt.UTC().UnixNano(),
		attemptID.String(), int64(commit.ExpectedAttemptRevision))
	if err != nil {
		return fmt.Errorf("close attempt %s for rejection: %w", attemptID, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%w: attempt %s revision changed", statestore.ErrConflict, attemptID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rejected result for %s: %w", attemptID, err)
	}
	return nil
}

func scanRejectedResult(row rowScanner) (rejectedresult.Record, error) {
	var (
		attemptID, taskID, claimHex, outputHex, policyHex, reason string
		dissentJSON, verifiersJSON, excludedJSON                  string
		rejectedNS                                                int64
	)
	if err := row.Scan(&attemptID, &taskID, &claimHex, &outputHex, &policyHex, &reason,
		&dissentJSON, &verifiersJSON, &excludedJSON, &rejectedNS); err != nil {
		return rejectedresult.Record{}, err
	}
	corrupt := func(err error) error {
		return fmt.Errorf("%w: rejected result %s: %v", ErrCorruptRecord, attemptID, err)
	}
	claimDigest, err := contract.ParseSHA256(claimHex)
	if err != nil {
		return rejectedresult.Record{}, corrupt(err)
	}
	outputDigest, err := contract.ParseSHA256(outputHex)
	if err != nil {
		return rejectedresult.Record{}, corrupt(err)
	}
	policyDigest, err := contract.ParseSHA256(policyHex)
	if err != nil {
		return rejectedresult.Record{}, corrupt(err)
	}
	var (
		dissentRows   []dissentRow
		verifiers     []string
		exclusionRows []exclusionRow
	)
	if err := json.Unmarshal([]byte(dissentJSON), &dissentRows); err != nil {
		return rejectedresult.Record{}, corrupt(err)
	}
	if err := json.Unmarshal([]byte(verifiersJSON), &verifiers); err != nil {
		return rejectedresult.Record{}, corrupt(err)
	}
	if err := json.Unmarshal([]byte(excludedJSON), &exclusionRows); err != nil {
		return rejectedresult.Record{}, corrupt(err)
	}
	cfg := rejectedresult.Config{
		TaskID: ids.TaskID(taskID), AttemptID: ids.AttemptID(attemptID),
		ClaimDigest: claimDigest, OutputDigest: outputDigest, PolicyDigest: policyDigest,
		Reason: resultaccept.Reason(reason), Verifiers: verifiers, RejectedAt: time.Unix(0, rejectedNS).UTC(),
	}
	for _, d := range dissentRows {
		cfg.Dissent = append(cfg.Dissent, rejectedresult.Dissent{
			VerifierID: d.VerifierID, Check: resultaccept.Check(d.Check), ObservedDigest: d.ObservedDigest,
		})
	}
	for _, e := range exclusionRows {
		cfg.Excluded = append(cfg.Excluded, rejectedresult.Exclusion{VerifierID: e.VerifierID, Reason: e.Reason})
	}
	record, err := rejectedresult.New(cfg)
	if err != nil {
		return rejectedresult.Record{}, corrupt(err)
	}
	return record, nil
}
