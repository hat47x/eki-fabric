package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

const selectAdmission = `
SELECT t.task_id, t.task_ir_digest, t.phase, t.terminal_outcome, t.cancellation_requested, t.revision, t.created_at_ns, t.updated_at_ns,
       a.principal, a.idempotency_key, a.request_fingerprint, a.max_attempts, a.content
FROM admissions a JOIN tasks t ON t.task_id = a.task_id`

func (s *Store) AdmitTask(ctx context.Context, admission statestore.Admission, limit statestore.AdmitLimit) (statestore.AdmitResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.AdmitResult{}, err
	}
	if err := admission.Validate(); err != nil {
		return statestore.AdmitResult{}, err
	}

	// The DSN makes every transaction BEGIN IMMEDIATE, so the idempotency
	// lookup, the quota count and the inserts below cannot interleave with
	// another writer, in this process or another.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return statestore.AdmitResult{}, fmt.Errorf("admit task %s: %w", admission.Task.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := scanAdmission(tx.QueryRowContext(ctx,
		selectAdmission+` WHERE a.principal = ? AND a.idempotency_key = ?`,
		admission.Principal, admission.IdempotencyKey))
	switch {
	case err == nil:
		if existing.RequestFingerprint != admission.RequestFingerprint {
			return statestore.AdmitResult{}, fmt.Errorf("%w: principal %q", statestore.ErrIdempotencyConflict, admission.Principal)
		}
		return statestore.AdmitResult{Admission: existing, Replayed: true}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return statestore.AdmitResult{}, fmt.Errorf("admit task %s: %w", admission.Task.ID, err)
	}

	if limit.MaxOutstandingTasks > 0 {
		var outstanding int64
		if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM admissions a JOIN tasks t ON t.task_id = a.task_id
WHERE a.principal = ? AND t.phase <> 'terminal'`, admission.Principal).Scan(&outstanding); err != nil {
			return statestore.AdmitResult{}, fmt.Errorf("admit task %s: count outstanding: %w", admission.Task.ID, err)
		}
		if outstanding >= int64(limit.MaxOutstandingTasks) {
			return statestore.AdmitResult{}, fmt.Errorf("%w: principal %q", statestore.ErrQuotaExceeded, admission.Principal)
		}
	}

	record := admission.Task
	res, err := tx.ExecContext(ctx, `
INSERT INTO tasks (task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (task_id) DO NOTHING`,
		string(record.ID), record.TaskIRDigest.Hex(), string(record.Phase), string(record.TerminalOutcome),
		boolToInt(record.CancellationRequested), int64(record.Revision),
		record.CreatedAt.UTC().UnixNano(), record.UpdatedAt.UTC().UnixNano())
	if err != nil {
		return statestore.AdmitResult{}, fmt.Errorf("admit task %s: insert task: %w", record.ID, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return statestore.AdmitResult{}, fmt.Errorf("%w: task %s", statestore.ErrAlreadyExists, record.ID)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO admissions (task_id, principal, idempotency_key, request_fingerprint, max_attempts, content)
VALUES (?, ?, ?, ?, ?, ?)`,
		string(record.ID), admission.Principal, admission.IdempotencyKey,
		admission.RequestFingerprint.Hex(), int64(admission.MaxAttempts), admission.Content); err != nil {
		return statestore.AdmitResult{}, fmt.Errorf("admit task %s: insert admission: %w", record.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return statestore.AdmitResult{}, fmt.Errorf("admit task %s: commit: %w", record.ID, err)
	}
	return statestore.AdmitResult{Admission: admission}, nil
}

func (s *Store) LoadAdmission(ctx context.Context, id ids.TaskID) (statestore.Admission, error) {
	if err := ctx.Err(); err != nil {
		return statestore.Admission{}, err
	}
	admission, err := scanAdmission(s.db.QueryRowContext(ctx, selectAdmission+` WHERE a.task_id = ?`, string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.Admission{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, id)
	}
	if err != nil {
		return statestore.Admission{}, err
	}
	return admission, nil
}

// scanAdmission reads a row of selectAdmission. The first eight columns are the
// task row, in the order scanTask expects.
func scanAdmission(row rowScanner) (statestore.Admission, error) {
	var (
		taskCols              taskColumns
		principal, key, fpHex string
		maxAttempts           int64
		content               []byte
	)
	if err := row.Scan(&taskCols.id, &taskCols.digestHex, &taskCols.phase, &taskCols.outcome,
		&taskCols.cancel, &taskCols.revision, &taskCols.createdNS, &taskCols.updatedNS,
		&principal, &key, &fpHex, &maxAttempts, &content); err != nil {
		return statestore.Admission{}, err
	}
	record, err := taskCols.record()
	if err != nil {
		return statestore.Admission{}, err
	}
	fingerprint, err := contract.ParseSHA256(fpHex)
	if err != nil {
		return statestore.Admission{}, fmt.Errorf("%w: task %s: fingerprint: %v", ErrCorruptRecord, record.ID, err)
	}
	if maxAttempts < 1 || maxAttempts > statestore.MaxAdmissionAttempts {
		return statestore.Admission{}, fmt.Errorf("%w: task %s: max attempts %d", ErrCorruptRecord, record.ID, maxAttempts)
	}
	admission := statestore.Admission{
		Task:               record,
		Principal:          principal,
		IdempotencyKey:     key,
		RequestFingerprint: fingerprint,
		MaxAttempts:        uint32(maxAttempts),
		Content:            content,
	}
	if err := admission.Validate(); err != nil {
		return statestore.Admission{}, fmt.Errorf("%w: task %s: %v", ErrCorruptRecord, record.ID, err)
	}
	return admission, nil
}

var _ statestore.Store = (*Store)(nil)

// LoadAdmissionByIdempotencyKey implements statestore.ReplayLookup: it reads
// the admission a (principal, key) scope converges on, without creating
// anything (#264).
func (s *Store) LoadAdmissionByIdempotencyKey(ctx context.Context, principal, key string) (statestore.Admission, error) {
	if err := ctx.Err(); err != nil {
		return statestore.Admission{}, err
	}
	admission, err := scanAdmission(s.db.QueryRowContext(ctx,
		selectAdmission+` WHERE a.principal = ? AND a.idempotency_key = ?`, principal, key))
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.Admission{}, fmt.Errorf("%w: key %q for principal %q", statestore.ErrNotFound, key, principal)
	}
	if err != nil {
		return statestore.Admission{}, err
	}
	return admission, nil
}
