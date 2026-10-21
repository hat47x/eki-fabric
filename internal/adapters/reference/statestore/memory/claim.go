package memory

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.ResultClaimStore = (*Store)(nil)

func (s *Store) LoadResultClaim(ctx context.Context, id ids.AttemptID) (resultclaim.Claim, bool, error) {
	if err := ctx.Err(); err != nil {
		return resultclaim.Claim{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	claim, ok := s.claims[id]
	return claim, ok, nil
}

func (s *Store) ApplyResultClaim(ctx context.Context, acceptance statestore.ResultClaimAcceptance) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !acceptance.Claim.Valid() {
		return fmt.Errorf("%w: claim is not constructed", resultclaim.ErrInvalidClaim)
	}
	if !acceptance.Record && !acceptance.ApplyTransition {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	attemptID := acceptance.Claim.AttemptID()
	if acceptance.Record {
		if existing, ok := s.claims[attemptID]; ok {
			if existing.Equal(acceptance.Claim) {
				return fmt.Errorf("%w: claim for %s", statestore.ErrAlreadyExists, attemptID)
			}
			return fmt.Errorf("%w: claim for %s", statestore.ErrConflict, attemptID)
		}
	}

	var next attemptstate.AttemptRecord
	if acceptance.ApplyTransition {
		current, ok := s.attempts[attemptID]
		if !ok {
			return fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, attemptID)
		}
		if current.Revision != acceptance.ExpectedAttemptRevision {
			return fmt.Errorf("%w: attempt %s revision %d", statestore.ErrConflict, attemptID, current.Revision)
		}
		if err := attemptstate.ValidateTransition(current, acceptance.NextAttempt); err != nil {
			return fmt.Errorf("%w: %v", statestore.ErrConflict, err)
		}
		next = acceptance.NextAttempt
	}

	if acceptance.Record {
		s.claims[attemptID] = acceptance.Claim
	}
	if acceptance.ApplyTransition {
		s.attempts[attemptID] = next
	}
	return nil
}
