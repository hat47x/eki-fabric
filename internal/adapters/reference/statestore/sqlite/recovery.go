package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.RecoveryStore = (*Store)(nil)

const (
	selectAttemptByID = `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`
	selectTaskByID = `
SELECT task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns
FROM tasks WHERE task_id = ?`
	pendingColumns = `task_id, task_ir_digest, previous_attempt_id, previous_attempt_number, next_attempt_number,
	max_attempts, priority, deadline_ns, not_before_ns, estimated_transfer_bytes, estimated_compute_millis`
	permitColumns = `previous_attempt_id, task_id, task_ir_digest, previous_attempt_number, next_attempt_number,
	policy_revision, sequence, issued_at_ns, not_before_ns, expires_at_ns,
	estimated_transfer_bytes, estimated_compute_millis, permit_digest, spent_by`
)

// int64 range guard: values that SQLite stores as INTEGER must fit.
func fitsInt64(values ...uint64) bool {
	for _, v := range values {
		if v > 1<<63-1 {
			return false
		}
	}
	return true
}

func (s *Store) EnqueueRecovery(ctx context.Context, pending recovery.Pending) (recovery.Pending, bool, error) {
	if err := ctx.Err(); err != nil {
		return recovery.Pending{}, false, err
	}
	if err := pending.Validate(); err != nil {
		return recovery.Pending{}, false, fmt.Errorf("%w: %v", statestore.ErrInvalidRecovery, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return recovery.Pending{}, false, fmt.Errorf("enqueue recovery for %s: %w", pending.TaskID, err)
	}
	defer func() { _ = tx.Rollback() }()

	attempt, err := scanAttempt(tx.QueryRowContext(ctx, selectAttemptByID, pending.PreviousAttemptID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return recovery.Pending{}, false, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, pending.PreviousAttemptID)
	}
	if err != nil {
		return recovery.Pending{}, false, fmt.Errorf("load attempt %s for recovery: %w", pending.PreviousAttemptID, err)
	}
	task, err := scanTask(tx.QueryRowContext(ctx, selectTaskByID, pending.TaskID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return recovery.Pending{}, false, fmt.Errorf("%w: task %s", statestore.ErrNotFound, pending.TaskID)
	}
	if err != nil {
		return recovery.Pending{}, false, fmt.Errorf("load task %s for recovery: %w", pending.TaskID, err)
	}

	existing, err := scanPending(tx.QueryRowContext(ctx, `SELECT `+pendingColumns+` FROM recovery_pending WHERE task_id = ?`, pending.TaskID.String()))
	switch {
	case err == nil:
		if existing.PreviousAttemptID == pending.PreviousAttemptID {
			return existing, true, nil
		}
		return recovery.Pending{}, false, fmt.Errorf("%w: task %s already waits for a retry of attempt %s",
			statestore.ErrConflict, pending.TaskID, existing.PreviousAttemptID)
	case !errors.Is(err, sql.ErrNoRows):
		return recovery.Pending{}, false, err
	}
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM recovery_permits WHERE previous_attempt_id = ?`, pending.PreviousAttemptID.String()).Scan(&one)
	switch {
	case err == nil:
		return recovery.Pending{}, false, fmt.Errorf("%w: attempt %s already has a recovery Permit", statestore.ErrAlreadyExists, pending.PreviousAttemptID)
	case !errors.Is(err, sql.ErrNoRows):
		return recovery.Pending{}, false, fmt.Errorf("check Permit for %s: %w", pending.PreviousAttemptID, err)
	}

	if attempt.TaskID != pending.TaskID || attempt.Phase != attemptstate.AttemptPhaseClosed ||
		!attempt.CloseReason.IsInterruption() || attempt.Number != uint64(pending.PreviousAttemptNumber) ||
		task.TaskIRDigest != pending.TaskIRDigest {
		return recovery.Pending{}, false, fmt.Errorf("%w: attempt %s is not an interrupted Attempt of task %s",
			statestore.ErrConflict, pending.PreviousAttemptID, pending.TaskID)
	}
	if task.CancellationRequested || (task.Phase != taskstate.TaskPhaseReady && task.Phase != taskstate.TaskPhaseActive) {
		return recovery.Pending{}, false, fmt.Errorf("%w: task %s cannot take a new Attempt", statestore.ErrConflict, pending.TaskID)
	}
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM attempts WHERE task_id = ? AND attempt_number = ?`,
		pending.TaskID.String(), int64(pending.NextAttemptNumber)).Scan(&one)
	switch {
	case err == nil:
		return recovery.Pending{}, false, fmt.Errorf("%w: attempt number %d of task %s already exists",
			statestore.ErrConflict, pending.NextAttemptNumber, pending.TaskID)
	case !errors.Is(err, sql.ErrNoRows):
		return recovery.Pending{}, false, fmt.Errorf("check attempt number for %s: %w", pending.TaskID, err)
	}

	var deadline int64
	if !pending.Deadline.IsZero() {
		deadline = pending.Deadline.UTC().UnixNano()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_pending (`+pendingColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pending.TaskID.String(), pending.TaskIRDigest.Hex(), pending.PreviousAttemptID.String(),
		int64(pending.PreviousAttemptNumber), int64(pending.NextAttemptNumber), int64(pending.MaxAttempts),
		int64(pending.Priority), deadline, pending.NotBefore.UTC().UnixNano(),
		strconv.FormatUint(pending.EstimatedTransferBytes, 10), strconv.FormatUint(pending.EstimatedComputeMillis, 10),
	); err != nil {
		return recovery.Pending{}, false, fmt.Errorf("store pending retry for %s: %w", pending.TaskID, err)
	}
	if err := tx.Commit(); err != nil {
		return recovery.Pending{}, false, fmt.Errorf("commit pending retry for %s: %w", pending.TaskID, err)
	}
	return pending, false, nil
}

func scanPending(row rowScanner) (recovery.Pending, error) {
	var (
		taskID, digest, previous, transfer, compute                            string
		previousNumber, nextNumber, maxAttempts, priority, deadline, notBefore int64
	)
	if err := row.Scan(&taskID, &digest, &previous, &previousNumber, &nextNumber, &maxAttempts, &priority,
		&deadline, &notBefore, &transfer, &compute); err != nil {
		return recovery.Pending{}, err
	}
	corrupt := func(err error) error { return fmt.Errorf("%w: pending retry %s: %v", ErrCorruptRecord, taskID, err) }
	parsed, err := contract.ParseSHA256(digest)
	if err != nil {
		return recovery.Pending{}, corrupt(err)
	}
	transferBytes, err := strconv.ParseUint(transfer, 10, 64)
	if err != nil {
		return recovery.Pending{}, corrupt(err)
	}
	computeMillis, err := strconv.ParseUint(compute, 10, 64)
	if err != nil {
		return recovery.Pending{}, corrupt(err)
	}
	p := recovery.Pending{
		TaskID: ids.TaskID(taskID), TaskIRDigest: parsed, PreviousAttemptID: ids.AttemptID(previous),
		PreviousAttemptNumber: uint32(previousNumber), NextAttemptNumber: uint32(nextNumber),
		MaxAttempts: uint32(maxAttempts), Priority: recovery.Priority(priority),
		NotBefore:              time.Unix(0, notBefore).UTC(),
		EstimatedTransferBytes: transferBytes, EstimatedComputeMillis: computeMillis,
	}
	if deadline != 0 {
		p.Deadline = time.Unix(0, deadline).UTC()
	}
	if err := p.Validate(); err != nil {
		return recovery.Pending{}, corrupt(err)
	}
	return p, nil
}

func (s *Store) LoadRecoveryPending(ctx context.Context) ([]recovery.Pending, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+pendingColumns+` FROM recovery_pending ORDER BY task_id`)
	if err != nil {
		return nil, fmt.Errorf("load pending retries: %w", err)
	}
	defer rows.Close()
	var out []recovery.Pending
	for rows.Next() {
		p, err := scanPending(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load pending retries: %w", err)
	}
	return out, nil
}

func scanWindow(row rowScanner) (statestore.RecoveryWindow, error) {
	var (
		revision, start, released, last int64
		transfer, compute               string
	)
	if err := row.Scan(&revision, &start, &released, &transfer, &compute, &last); err != nil {
		return statestore.RecoveryWindow{}, err
	}
	corrupt := func(err error) error { return fmt.Errorf("%w: recovery window: %v", ErrCorruptRecord, err) }
	transferBytes, err := strconv.ParseUint(transfer, 10, 64)
	if err != nil {
		return statestore.RecoveryWindow{}, corrupt(err)
	}
	computeMillis, err := strconv.ParseUint(compute, 10, 64)
	if err != nil {
		return statestore.RecoveryWindow{}, corrupt(err)
	}
	return statestore.RecoveryWindow{
		PolicyRevision: uint64(revision), LastSequence: uint64(last),
		Budget: recovery.Budget{
			WindowStart: time.Unix(0, start).UTC(), Released: uint32(released),
			TransferBytes: transferBytes, ComputeMillis: computeMillis,
		},
	}, nil
}

const windowColumns = `policy_revision, window_start_ns, released, transfer_bytes, compute_millis, last_sequence`

func (s *Store) LoadRecoveryWindow(ctx context.Context) (statestore.RecoveryWindow, bool, error) {
	if err := ctx.Err(); err != nil {
		return statestore.RecoveryWindow{}, false, err
	}
	w, err := scanWindow(s.db.QueryRowContext(ctx, `SELECT `+windowColumns+` FROM recovery_window WHERE singleton = 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.RecoveryWindow{}, false, nil
	}
	if err != nil {
		return statestore.RecoveryWindow{}, false, err
	}
	return w, true, nil
}

func sameStoredBudget(a, b recovery.Budget) bool {
	return a.WindowStart.Equal(b.WindowStart) && a.Released == b.Released &&
		a.TransferBytes == b.TransferBytes && a.ComputeMillis == b.ComputeMillis
}

func (s *Store) CommitRecoveryRelease(ctx context.Context, release statestore.RecoveryRelease) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := release.Validate(); err != nil {
		return false, err
	}
	d := release.Decision
	if !fitsInt64(d.Sequence, release.Policy.Revision) {
		return false, fmt.Errorf("%w: the sequence or the policy revision is out of range", statestore.ErrInvalidRecovery)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("commit recovery pass %d: %w", d.Sequence, err)
	}
	defer func() { _ = tx.Rollback() }()

	fingerprint := release.Fingerprint().Hex()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT fingerprint FROM recovery_releases WHERE sequence = ?`, int64(d.Sequence)).Scan(&previous)
	switch {
	case err == nil:
		if previous == fingerprint {
			return true, nil
		}
		return false, fmt.Errorf("%w: sequence %d was already committed with another content", statestore.ErrConflict, d.Sequence)
	case !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("check recovery pass %d: %w", d.Sequence, err)
	}

	var last uint64
	stored := recovery.Budget{}
	haveWindow := false
	w, err := scanWindow(tx.QueryRowContext(ctx, `SELECT `+windowColumns+` FROM recovery_window WHERE singleton = 1`))
	switch {
	case err == nil:
		haveWindow, last = true, w.LastSequence
		// Budgets are never carried across policy revisions: their limits differ.
		if w.PolicyRevision == release.Policy.Revision {
			stored = w.Budget
		}
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	if d.Sequence != last+1 {
		return false, fmt.Errorf("%w: sequence %d does not follow %d", statestore.ErrConflict, d.Sequence, last)
	}
	if !sameStoredBudget(stored, release.ExpectedBudget) {
		return false, fmt.Errorf("%w: the pass started from a budget that is not the stored one", statestore.ErrConflict)
	}

	for _, permit := range d.Permits {
		pending, err := scanPending(tx.QueryRowContext(ctx, `SELECT `+pendingColumns+` FROM recovery_pending WHERE task_id = ?`, permit.TaskID().String()))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !pendingMatchesPermit(pending, permit)) {
			return false, fmt.Errorf("%w: no pending retry matches the Permit for task %s", statestore.ErrConflict, permit.TaskID())
		}
		if err != nil {
			return false, err
		}
	}
	for _, expired := range d.Expired {
		pending, err := scanPending(tx.QueryRowContext(ctx, `SELECT `+pendingColumns+` FROM recovery_pending WHERE task_id = ?`, expired.TaskID.String()))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && pending.PreviousAttemptID != expired.PreviousAttemptID) {
			return false, fmt.Errorf("%w: no pending retry matches the expired retry for task %s", statestore.ErrConflict, expired.TaskID)
		}
		if err != nil {
			return false, err
		}
	}

	for _, permit := range d.Permits {
		if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_pending WHERE task_id = ?`, permit.TaskID().String()); err != nil {
			return false, fmt.Errorf("release retry of %s: %w", permit.TaskID(), err)
		}
		r := permit.Record()
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_permits (`+permitColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			r.PreviousAttemptID.String(), r.TaskID.String(), r.TaskIRDigest.Hex(),
			int64(r.PreviousAttemptNumber), int64(r.NextAttemptNumber), int64(r.PolicyRevision), int64(r.Sequence),
			r.IssuedAt.UnixNano(), r.NotBefore.UnixNano(), r.ExpiresAt.UnixNano(),
			strconv.FormatUint(r.EstimatedTransferBytes, 10), strconv.FormatUint(r.EstimatedComputeMillis, 10),
			r.Digest.Hex()); err != nil {
			return false, fmt.Errorf("store Permit for %s: %w", r.PreviousAttemptID, err)
		}
	}
	for _, expired := range d.Expired {
		if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_pending WHERE task_id = ?`, expired.TaskID.String()); err != nil {
			return false, fmt.Errorf("drop expired retry of %s: %w", expired.TaskID, err)
		}
	}

	b := d.Budget
	if haveWindow {
		_, err = tx.ExecContext(ctx, `
UPDATE recovery_window
SET policy_revision = ?, window_start_ns = ?, released = ?, transfer_bytes = ?, compute_millis = ?, last_sequence = ?
WHERE singleton = 1`,
			int64(release.Policy.Revision), b.WindowStart.UnixNano(), int64(b.Released),
			strconv.FormatUint(b.TransferBytes, 10), strconv.FormatUint(b.ComputeMillis, 10), int64(d.Sequence))
	} else {
		_, err = tx.ExecContext(ctx, `
INSERT INTO recovery_window (singleton, `+windowColumns+`) VALUES (1, ?, ?, ?, ?, ?, ?)`,
			int64(release.Policy.Revision), b.WindowStart.UnixNano(), int64(b.Released),
			strconv.FormatUint(b.TransferBytes, 10), strconv.FormatUint(b.ComputeMillis, 10), int64(d.Sequence))
	}
	if err != nil {
		return false, fmt.Errorf("store the recovery window: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_releases (sequence, fingerprint) VALUES (?, ?)`, int64(d.Sequence), fingerprint); err != nil {
		return false, fmt.Errorf("store recovery pass %d: %w", d.Sequence, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit recovery pass %d: %w", d.Sequence, err)
	}
	return false, nil
}

func pendingMatchesPermit(p recovery.Pending, permit recovery.Permit) bool {
	return p.TaskID == permit.TaskID() && p.TaskIRDigest == permit.TaskIRDigest() &&
		p.PreviousAttemptID == permit.PreviousAttemptID() &&
		p.PreviousAttemptNumber == permit.PreviousAttemptNumber() &&
		p.NextAttemptNumber == permit.NextAttemptNumber()
}

func scanPermit(row rowScanner) (statestore.StoredPermit, error) {
	var (
		previous, taskID, taskIR, transfer, compute, digest string
		spent                                               sql.NullString
		previousNumber, nextNumber, revision, sequence      int64
		issued, notBefore, expires                          int64
	)
	if err := row.Scan(&previous, &taskID, &taskIR, &previousNumber, &nextNumber, &revision, &sequence,
		&issued, &notBefore, &expires, &transfer, &compute, &digest, &spent); err != nil {
		return statestore.StoredPermit{}, err
	}
	corrupt := func(err error) error { return fmt.Errorf("%w: Permit for %s: %v", ErrCorruptRecord, previous, err) }
	taskDigest, err := contract.ParseSHA256(taskIR)
	if err != nil {
		return statestore.StoredPermit{}, corrupt(err)
	}
	permitDigest, err := contract.ParseSHA256(digest)
	if err != nil {
		return statestore.StoredPermit{}, corrupt(err)
	}
	transferBytes, err := strconv.ParseUint(transfer, 10, 64)
	if err != nil {
		return statestore.StoredPermit{}, corrupt(err)
	}
	computeMillis, err := strconv.ParseUint(compute, 10, 64)
	if err != nil {
		return statestore.StoredPermit{}, corrupt(err)
	}
	permit, err := recovery.RestorePermit(recovery.PermitRecord{
		TaskID: ids.TaskID(taskID), TaskIRDigest: taskDigest, PreviousAttemptID: ids.AttemptID(previous),
		PreviousAttemptNumber: uint32(previousNumber), NextAttemptNumber: uint32(nextNumber),
		PolicyRevision: uint64(revision), Sequence: uint64(sequence),
		IssuedAt: time.Unix(0, issued).UTC(), NotBefore: time.Unix(0, notBefore).UTC(), ExpiresAt: time.Unix(0, expires).UTC(),
		EstimatedTransferBytes: transferBytes, EstimatedComputeMillis: computeMillis, Digest: permitDigest,
	})
	if err != nil {
		return statestore.StoredPermit{}, corrupt(err)
	}
	out := statestore.StoredPermit{Permit: permit}
	if spent.Valid {
		out.SpentBy = ids.AttemptID(spent.String)
	}
	return out, nil
}

func (s *Store) LoadRecoveryPermit(ctx context.Context, previous ids.AttemptID) (statestore.StoredPermit, bool, error) {
	if err := ctx.Err(); err != nil {
		return statestore.StoredPermit{}, false, err
	}
	stored, err := scanPermit(s.db.QueryRowContext(ctx, `SELECT `+permitColumns+` FROM recovery_permits WHERE previous_attempt_id = ?`, previous.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.StoredPermit{}, false, nil
	}
	if err != nil {
		return statestore.StoredPermit{}, false, err
	}
	return stored, true, nil
}

func (s *Store) AdmitRecoveryAttempt(ctx context.Context, admission statestore.RecoveryAdmission) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := admission.Validate(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("admit recovery attempt after %s: %w", admission.PreviousAttemptID, err)
	}
	defer func() { _ = tx.Rollback() }()

	stored, err := scanPermit(tx.QueryRowContext(ctx, `SELECT `+permitColumns+` FROM recovery_permits WHERE previous_attempt_id = ?`, admission.PreviousAttemptID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no Permit for attempt %s", statestore.ErrNotFound, admission.PreviousAttemptID)
	}
	if err != nil {
		return err
	}
	if stored.SpentBy != "" {
		return fmt.Errorf("%w: the Permit for attempt %s was used by %s", statestore.ErrAlreadyExists, admission.PreviousAttemptID, stored.SpentBy)
	}
	permit := stored.Permit
	if err := permit.Validate(admission.Now, recovery.Subject{
		TaskID: permit.TaskID(), TaskIRDigest: permit.TaskIRDigest(),
		PreviousAttemptID: permit.PreviousAttemptID(), PolicyRevision: admission.PolicyRevision,
	}); err != nil {
		return err
	}
	previous, err := scanAttempt(tx.QueryRowContext(ctx, selectAttemptByID, admission.PreviousAttemptID.String()))
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (previous.Phase != attemptstate.AttemptPhaseClosed || !previous.CloseReason.IsInterruption())) {
		return fmt.Errorf("%w: attempt %s is not an interrupted Attempt", statestore.ErrConflict, admission.PreviousAttemptID)
	}
	if err != nil {
		return err
	}
	next := admission.NewAttempt
	if next.TaskID != permit.TaskID() || next.TaskIRDigest != permit.TaskIRDigest() ||
		next.Number != uint64(permit.NextAttemptNumber()) {
		return fmt.Errorf("%w: the Attempt is not the one the Permit admits", statestore.ErrConflict)
	}
	if err := createAttemptTx(ctx, tx, next); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE recovery_permits SET spent_by = ? WHERE previous_attempt_id = ? AND spent_by IS NULL`,
		next.ID.String(), admission.PreviousAttemptID.String())
	if err != nil {
		return fmt.Errorf("mark the Permit for %s as used: %w", admission.PreviousAttemptID, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%w: the Permit for attempt %s was used meanwhile", statestore.ErrConflict, admission.PreviousAttemptID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit recovery attempt after %s: %w", admission.PreviousAttemptID, err)
	}
	return nil
}
