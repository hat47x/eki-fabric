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
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) CreateAttempt(ctx context.Context, record attemptstate.AttemptRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.ValidateInitial(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create attempt %s: %w", record.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		parentDigest    string
		parentPhase     string
		parentCancelled int64
	)
	err = tx.QueryRowContext(ctx, `
SELECT task_ir_digest, phase, cancellation_requested
FROM tasks WHERE task_id = ?`, record.TaskID.String()).Scan(&parentDigest, &parentPhase, &parentCancelled)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: task %s", statestore.ErrNotFound, record.TaskID)
	}
	if err != nil {
		return fmt.Errorf("load parent task for attempt %s: %w", record.ID, err)
	}
	if parentDigest != record.TaskIRDigest.Hex() {
		return fmt.Errorf("%w: attempt %s TaskIR differs from parent Task", statestore.ErrConflict, record.ID)
	}
	phase := taskstate.TaskPhase(parentPhase)
	if parentCancelled != 0 ||
		(phase != taskstate.TaskPhaseReady && phase != taskstate.TaskPhaseActive) {
		return fmt.Errorf("%w: task %s cannot create a new Attempt", statestore.ErrConflict, record.TaskID)
	}

	var lease any
	if record.LeaseID != "" {
		lease = record.LeaseID.String()
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO attempts (
	attempt_id, task_id, task_ir_digest, attempt_number, phase,
	worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`,
		record.ID.String(),
		record.TaskID.String(),
		record.TaskIRDigest.Hex(),
		int64(record.Number),
		string(record.Phase),
		record.WorkerIdentity,
		lease,
		string(record.CloseReason),
		int64(record.Revision),
		record.CreatedAt.UTC().UnixNano(),
		record.UpdatedAt.UTC().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("create attempt %s: %w", record.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("create attempt %s: %w", record.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: attempt %s or task %s number %d", statestore.ErrAlreadyExists, record.ID, record.TaskID, record.Number)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit attempt %s: %w", record.ID, err)
	}
	return nil
}

func (s *Store) LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error) {
	if err := ctx.Err(); err != nil {
		return attemptstate.AttemptRecord{}, err
	}
	record, err := scanAttempt(s.db.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return attemptstate.AttemptRecord{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, id)
	}
	return record, err
}

func (s *Store) CompareAndSwapAttempt(ctx context.Context, id ids.AttemptID, expectedRevision uint64, next attemptstate.AttemptRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if next.ID != id {
		return fmt.Errorf("%w: next attempt ID does not match target", statestore.ErrConflict)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compare-and-swap attempt %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanAttempt(tx.QueryRowContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE attempt_id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return fmt.Errorf("%w: attempt %s is at revision %d, expected %d", statestore.ErrConflict, id, current.Revision, expectedRevision)
	}
	if err := attemptstate.ValidateTransition(current, next); err != nil {
		return err
	}

	var lease any
	if next.LeaseID != "" {
		lease = next.LeaseID.String()
	}
	res, err := tx.ExecContext(ctx, `
UPDATE attempts
SET phase = ?, worker_identity = ?, lease_id = ?, close_reason = ?, revision = ?, updated_at_ns = ?
WHERE attempt_id = ? AND revision = ?`,
		string(next.Phase),
		next.WorkerIdentity,
		lease,
		string(next.CloseReason),
		int64(next.Revision),
		next.UpdatedAt.UTC().UnixNano(),
		id.String(),
		int64(expectedRevision),
	)
	if err != nil {
		return fmt.Errorf("compare-and-swap attempt %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("compare-and-swap attempt %s: %w", id, err)
	}
	if n != 1 {
		return fmt.Errorf("%w: attempt %s changed concurrently", statestore.ErrConflict, id)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit attempt %s: %w", id, err)
	}
	return nil
}

func scanAttempt(row rowScanner) (attemptstate.AttemptRecord, error) {
	var (
		attemptID, taskID, digestHex, phase, worker, closeReason string
		leaseID                                                  sql.NullString
		number, revision, createdNS, updatedNS                   int64
	)
	if err := row.Scan(
		&attemptID, &taskID, &digestHex, &number, &phase,
		&worker, &leaseID, &closeReason, &revision, &createdNS, &updatedNS,
	); err != nil {
		return attemptstate.AttemptRecord{}, err
	}
	id, err := ids.ParseAttemptID(attemptID)
	if err != nil {
		return attemptstate.AttemptRecord{}, corruptAttempt(attemptID, err)
	}
	parentID, err := ids.ParseTaskID(taskID)
	if err != nil {
		return attemptstate.AttemptRecord{}, corruptAttempt(attemptID, err)
	}
	digest, err := contract.ParseSHA256(digestHex)
	if err != nil {
		return attemptstate.AttemptRecord{}, corruptAttempt(attemptID, err)
	}
	if number < 1 || revision < 1 {
		return attemptstate.AttemptRecord{}, corruptAttempt(attemptID, errors.New("non-positive Attempt number or revision"))
	}
	var lease ids.LeaseID
	if leaseID.Valid {
		lease, err = ids.ParseLeaseID(leaseID.String)
		if err != nil {
			return attemptstate.AttemptRecord{}, corruptAttempt(attemptID, err)
		}
	}
	record := attemptstate.AttemptRecord{
		ID:             id,
		TaskID:         parentID,
		TaskIRDigest:   digest,
		Number:         uint64(number),
		Phase:          attemptstate.AttemptPhase(phase),
		WorkerIdentity: worker,
		LeaseID:        lease,
		CloseReason:    attemptstate.CloseReason(closeReason),
		Revision:       uint64(revision),
		CreatedAt:      time.Unix(0, createdNS).UTC(),
		UpdatedAt:      time.Unix(0, updatedNS).UTC(),
	}
	if err := record.Validate(); err != nil {
		return attemptstate.AttemptRecord{}, corruptAttempt(attemptID, err)
	}
	return record, nil
}

func corruptAttempt(id string, err error) error {
	return fmt.Errorf("%w: attempt %s: %v", ErrCorruptRecord, id, err)
}

var _ statestore.AttemptStore = (*Store)(nil)
