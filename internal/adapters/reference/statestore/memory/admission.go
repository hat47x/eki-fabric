package memory

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) AdmitTask(ctx context.Context, admission statestore.Admission, limit statestore.AdmitLimit) (statestore.AdmitResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.AdmitResult{}, err
	}
	if err := admission.Validate(); err != nil {
		return statestore.AdmitResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	scope := idempotencyScope{principal: admission.Principal, key: admission.IdempotencyKey}
	if existingID, ok := s.idempotent[scope]; ok {
		existing := s.admissions[existingID]
		if existing.RequestFingerprint != admission.RequestFingerprint {
			return statestore.AdmitResult{}, fmt.Errorf("%w: principal %q", statestore.ErrIdempotencyConflict, admission.Principal)
		}
		existing.Task = s.tasks[existingID]
		return statestore.AdmitResult{Admission: existing, Replayed: true}, nil
	}

	if _, exists := s.tasks[admission.Task.ID]; exists {
		return statestore.AdmitResult{}, fmt.Errorf("%w: task %s", statestore.ErrAlreadyExists, admission.Task.ID)
	}
	if limit.MaxOutstandingTasks > 0 && s.outstandingLocked(admission.Principal) >= limit.MaxOutstandingTasks {
		return statestore.AdmitResult{}, fmt.Errorf("%w: principal %q", statestore.ErrQuotaExceeded, admission.Principal)
	}

	s.tasks[admission.Task.ID] = admission.Task
	s.admissions[admission.Task.ID] = admission
	s.idempotent[scope] = admission.Task.ID
	return statestore.AdmitResult{Admission: admission}, nil
}

func (s *Store) LoadAdmission(ctx context.Context, id ids.TaskID) (statestore.Admission, error) {
	if err := ctx.Err(); err != nil {
		return statestore.Admission{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	admission, ok := s.admissions[id]
	if !ok {
		return statestore.Admission{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, id)
	}
	admission.Task = s.tasks[id]
	return admission, nil
}

// outstandingLocked counts the principal's tasks that are not terminal. The
// caller holds s.mu.
func (s *Store) outstandingLocked(principal string) uint32 {
	var n uint32
	for id, admission := range s.admissions {
		if admission.Principal == principal && s.tasks[id].Phase != taskstate.TaskPhaseTerminal {
			n++
		}
	}
	return n
}

var _ statestore.Store = (*Store)(nil)
