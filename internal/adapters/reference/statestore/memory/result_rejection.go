package memory

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/rejectedresult"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.ResultRejectionStore = (*Store)(nil)

func (s *Store) LoadRejectedResult(ctx context.Context, id ids.AttemptID) (rejectedresult.Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return rejectedresult.Record{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.rejected[id]
	return record, ok, nil
}

func (s *Store) CommitRejectedResult(ctx context.Context, commit statestore.ResultRejectionCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := commit.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	taskID, attemptID := commit.Result.TaskID(), commit.Result.AttemptID()
	if existing, ok := s.rejected[attemptID]; ok {
		if existing.Equal(commit.Result) {
			return fmt.Errorf("%w: rejected result for attempt %s", statestore.ErrAlreadyExists, attemptID)
		}
		return fmt.Errorf("%w: attempt %s already has a different rejected result", statestore.ErrConflict, attemptID)
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
		return fmt.Errorf("%w: attempt %s has no completed claim matching the rejected result", statestore.ErrConflict, attemptID)
	}

	s.rejected[attemptID] = commit.Result
	s.attempts[attemptID] = commit.NextAttempt
	return nil
}
