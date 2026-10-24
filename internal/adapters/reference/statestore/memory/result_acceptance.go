package memory

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/acceptedresult"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.ResultAcceptanceStore = (*Store)(nil)

func (s *Store) LoadAcceptedResult(ctx context.Context, id ids.TaskID) (acceptedresult.Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return acceptedresult.Record{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.accepted[id]
	return record, ok, nil
}

func (s *Store) CommitAcceptedResult(ctx context.Context, commit statestore.ResultAcceptanceCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := commit.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	taskID, attemptID := commit.Result.TaskID(), commit.Result.AttemptID()
	if existing, ok := s.accepted[taskID]; ok {
		if existing.Equal(commit.Result) {
			return fmt.Errorf("%w: accepted result for task %s", statestore.ErrAlreadyExists, taskID)
		}
		return fmt.Errorf("%w: task %s already has a different accepted result", statestore.ErrConflict, taskID)
	}

	attempt, ok := s.attempts[attemptID]
	if !ok {
		return fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, attemptID)
	}
	if attempt.TaskID != taskID || attempt.Revision != commit.ExpectedAttemptRevision ||
		attempt.Phase != attemptstate.AttemptPhaseVerifying {
		return fmt.Errorf("%w: attempt %s is not in verification at revision %d", statestore.ErrConflict, attemptID, commit.ExpectedAttemptRevision)
	}
	if err := attemptstate.ValidateTransition(attempt, commit.NextAttempt); err != nil {
		return fmt.Errorf("%w: %v", statestore.ErrConflict, err)
	}

	claim, ok := s.claims[attemptID]
	if !ok || claim.Status() != resultclaim.StatusCompleted ||
		claim.Digest() != commit.Result.ClaimDigest() || claim.OutputDigest() != commit.Result.OutputDigest() {
		return fmt.Errorf("%w: attempt %s has no completed claim matching the accepted result", statestore.ErrConflict, attemptID)
	}

	// The Attempt must still hold the Task's latest fence. A later Lease means
	// the Task was placed again, so this Attempt's result is a stale late
	// result and must not complete the Task (V3).
	lease, ok := s.leases[attempt.LeaseID]
	if !ok || lease.AttemptID != attemptID || lease.FenceEpoch != claim.FenceEpoch() ||
		s.maxFence[taskID] != lease.FenceEpoch {
		return fmt.Errorf("%w: attempt %s no longer holds the latest fence of task %s", statestore.ErrConflict, attemptID, taskID)
	}

	task, ok := s.tasks[taskID]
	if !ok {
		return fmt.Errorf("%w: task %s", statestore.ErrNotFound, taskID)
	}
	if task.Revision != commit.ExpectedTaskRevision || task.CancellationRequested ||
		task.TaskIRDigest != commit.NextTask.TaskIRDigest || !task.Phase.AcceptsResult() {
		return fmt.Errorf("%w: task %s cannot complete at revision %d", statestore.ErrConflict, taskID, commit.ExpectedTaskRevision)
	}

	s.accepted[taskID] = commit.Result
	s.attempts[attemptID] = commit.NextAttempt
	s.tasks[taskID] = commit.NextTask
	return nil
}
