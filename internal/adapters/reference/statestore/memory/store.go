package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type Store struct {
	mu    sync.RWMutex
	tasks map[ids.TaskID]taskstate.TaskRecord
}

func New() *Store {
	return &Store{tasks: make(map[ids.TaskID]taskstate.TaskRecord)}
}

func (s *Store) CreateTask(ctx context.Context, record taskstate.TaskRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[record.ID]; exists {
		return fmt.Errorf("%w: task %s", statestore.ErrAlreadyExists, record.ID)
	}
	s.tasks[record.ID] = record
	return nil
}

func (s *Store) LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error) {
	if err := ctx.Err(); err != nil {
		return taskstate.TaskRecord{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.tasks[id]
	if !ok {
		return taskstate.TaskRecord{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, id)
	}
	return record, nil
}

func (s *Store) CompareAndSwapTask(ctx context.Context, id ids.TaskID, expectedRevision uint64, next taskstate.TaskRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if next.ID != id {
		return fmt.Errorf("%w: next task ID does not match target", statestore.ErrConflict)
	}
	if next.Revision != expectedRevision+1 {
		return fmt.Errorf("%w: next revision %d does not follow expected revision %d", statestore.ErrConflict, next.Revision, expectedRevision)
	}
	if err := next.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("%w: task %s", statestore.ErrNotFound, id)
	}
	if current.Revision != expectedRevision {
		return fmt.Errorf("%w: task %s is at revision %d, expected %d", statestore.ErrConflict, id, current.Revision, expectedRevision)
	}

	s.tasks[id] = next
	return nil
}

var _ statestore.TaskStore = (*Store)(nil)
