package memory

import (
	"context"
	"fmt"
	"sort"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

// ListTasksByPhase returns at most limit tasks whose phase is the requested
// one, oldest creation first, like the SQLite adapter.
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

	s.mu.RLock()
	defer s.mu.RUnlock()
	records := []taskstate.TaskRecord{}
	for _, record := range s.tasks {
		if record.Phase != phase {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.Before(records[j].CreatedAt)
		}
		return records[i].ID < records[j].ID
	})
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

// phaseAcceptsListing mirrors the SQLite adapter: terminal Tasks are found
// through their principal, not by scanning for them.
func phaseAcceptsListing(phase taskstate.TaskPhase) bool {
	switch phase {
	case taskstate.TaskPhaseAdmitted, taskstate.TaskPhaseReady, taskstate.TaskPhaseActive,
		taskstate.TaskPhaseVerifying, taskstate.TaskPhaseReconciling, taskstate.TaskPhaseHeld:
		return true
	}
	return false
}

// ListAttemptsByTask returns every Attempt of one Task in Attempt number
// order, closed Attempts included.
func (s *Store) ListAttemptsByTask(ctx context.Context, taskID ids.TaskID) ([]attemptstate.AttemptRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	records := []attemptstate.AttemptRecord{}
	for _, record := range s.attempts {
		if record.TaskID != taskID {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Number < records[j].Number
	})
	return records, nil
}

// ListWorkers returns every registered Worker in identity order, including
// revoked ones.
func (s *Store) ListWorkers(ctx context.Context) ([]workerregistry.Worker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	workers := []workerregistry.Worker{}
	for _, worker := range s.workers {
		workers = append(workers, worker)
	}
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].Identity < workers[j].Identity
	})
	return workers, nil
}
