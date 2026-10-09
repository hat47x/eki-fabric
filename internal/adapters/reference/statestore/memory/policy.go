package memory

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) LoadCurrentPolicy(ctx context.Context) (policystate.State, error) {
	if err := ctx.Err(); err != nil {
		return policystate.State{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.policyCurrent == 0 {
		return policystate.State{}, statestore.ErrPolicyUnavailable
	}
	state, ok := s.policyHistory[s.policyCurrent]
	if !ok {
		return policystate.State{}, fmt.Errorf("%w: current epoch %d is missing", statestore.ErrConflict, s.policyCurrent)
	}
	return state, nil
}

func (s *Store) CompareAndSwapPolicy(ctx context.Context, expectedEpoch uint64, next policystate.State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.policyCurrent != expectedEpoch {
		return fmt.Errorf("%w: current epoch=%d expected=%d", statestore.ErrPolicyChanged, s.policyCurrent, expectedEpoch)
	}
	if expectedEpoch == 0 {
		if next.Epoch != 1 || next.Status != policystate.StatusActive {
			return fmt.Errorf("%w: initial policy must be active at epoch 1", statestore.ErrConflict)
		}
	} else {
		current, ok := s.policyHistory[expectedEpoch]
		if !ok {
			return fmt.Errorf("%w: current epoch %d is missing", statestore.ErrConflict, expectedEpoch)
		}
		if err := policystate.ValidateTransition(current, next); err != nil {
			return err
		}
	}

	if next.Status == policystate.StatusActive {
		for _, prior := range s.policyHistory {
			if prior.Revision == next.Revision && prior.Status == policystate.StatusRevoked {
				return fmt.Errorf("%w: %s", statestore.ErrPolicyReactivation, next.Revision)
			}
		}
	}
	if _, exists := s.policyHistory[next.Epoch]; exists {
		return fmt.Errorf("%w: policy epoch %d already exists", statestore.ErrAlreadyExists, next.Epoch)
	}

	s.policyHistory[next.Epoch] = next
	s.policyCurrent = next.Epoch
	return nil
}

var _ statestore.PolicyStore = (*Store)(nil)
