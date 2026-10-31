package sqlite

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

// ListTasksByPhase returns at most limit tasks whose phase is the requested
// one, oldest creation first. The dispatch loop reads it with phase
// 'admitted' to find work in arrival order without knowing Task IDs.
func (s *Store) ListTasksByPhase(ctx context.Context, phase taskstate.TaskPhase, limit int) ([]taskstate.TaskRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list tasks by phase: limit must be positive")
	}
	if !phaseAcceptsListing(phase) {
		return nil, fmt.Errorf("list tasks by phase %q: unsupported listing phase", phase)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns
FROM tasks WHERE phase = ? ORDER BY created_at_ns, task_id LIMIT ?`, string(phase), int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list tasks by phase %s: %w", phase, err)
	}
	defer rows.Close()
	records := []taskstate.TaskRecord{}
	for rows.Next() {
		record, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list tasks by phase %s: %w", phase, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks by phase %s: %w", phase, err)
	}
	return records, nil
}

// phaseAcceptsListing reports whether a phase is a listing target. Terminal
// Tasks are found through their principal, not by scanning for them; a
// terminal scan would also be the one query that grows without bound.
func phaseAcceptsListing(phase taskstate.TaskPhase) bool {
	switch phase {
	case taskstate.TaskPhaseAdmitted, taskstate.TaskPhaseReady, taskstate.TaskPhaseActive,
		taskstate.TaskPhaseVerifying, taskstate.TaskPhaseReconciling, taskstate.TaskPhaseHeld:
		return true
	}
	return false
}

// ListAttemptsByTask returns every Attempt of one Task in Attempt number
// order. Dispatch reads it to take the next free Attempt number.
func (s *Store) ListAttemptsByTask(ctx context.Context, taskID ids.TaskID) ([]attemptstate.AttemptRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT attempt_id, task_id, task_ir_digest, attempt_number, phase,
       worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
FROM attempts WHERE task_id = ? ORDER BY attempt_number, attempt_id`, taskID.String())
	if err != nil {
		return nil, fmt.Errorf("list attempts of task %s: %w", taskID, err)
	}
	defer rows.Close()
	records := []attemptstate.AttemptRecord{}
	for rows.Next() {
		record, err := scanAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("list attempts of task %s: %w", taskID, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list attempts of task %s: %w", taskID, err)
	}
	return records, nil
}

// ListWorkers returns every registered Worker in identity order, including
// revoked ones. Dispatch filters with Usable().
func (s *Store) ListWorkers(ctx context.Context) ([]workerregistry.Worker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT worker_identity, trust_domain, state, revision, credential_id, state_changed_by,
       cert_serial, cert_not_after_ns, created_at_ns, updated_at_ns
FROM workers ORDER BY worker_identity`)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()
	workers := []workerregistry.Worker{}
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, fmt.Errorf("list workers: %w", err)
		}
		workers = append(workers, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	return workers, nil
}
