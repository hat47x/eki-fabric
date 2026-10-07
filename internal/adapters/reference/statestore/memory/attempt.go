package memory

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) CreateAttempt(ctx context.Context, record attemptstate.AttemptRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[record.TaskID]
	if !ok {
		return fmt.Errorf("%w: task %s", statestore.ErrNotFound, record.TaskID)
	}
	if task.TaskIRDigest != record.TaskIRDigest {
		return fmt.Errorf("%w: attempt %s TaskIR differs from parent Task", statestore.ErrConflict, record.ID)
	}
	if _, exists := s.attempts[record.ID]; exists {
		return fmt.Errorf("%w: attempt %s", statestore.ErrAlreadyExists, record.ID)
	}
	for _, existing := range s.attempts {
		if existing.TaskID == record.TaskID && existing.Number == record.Number {
			return fmt.Errorf("%w: task %s attempt number %d", statestore.ErrAlreadyExists, record.TaskID, record.Number)
		}
	}
	s.attempts[record.ID] = record
	return nil
}

func (s *Store) LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error) {
	if err := ctx.Err(); err != nil {
		return attemptstate.AttemptRecord{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.attempts[id]
	if !ok {
		return attemptstate.AttemptRecord{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, id)
	}
	return record, nil
}

func (s *Store) CompareAndSwapAttempt(ctx context.Context, id ids.AttemptID, expectedRevision uint64, next attemptstate.AttemptRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if next.ID != id {
		return fmt.Errorf("%w: next attempt ID does not match target", statestore.ErrConflict)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.attempts[id]
	if !ok {
		return fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, id)
	}
	if current.Revision != expectedRevision {
		return fmt.Errorf("%w: attempt %s is at revision %d, expected %d", statestore.ErrConflict, id, current.Revision, expectedRevision)
	}
	if err := attemptstate.ValidateTransition(current, next); err != nil {
		return err
	}
	s.attempts[id] = next
	return nil
}

var _ statestore.AttemptStore = (*Store)(nil)
