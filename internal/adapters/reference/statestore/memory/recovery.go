package memory

import (
	"context"
	"fmt"
	"sort"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var _ statestore.RecoveryStore = (*Store)(nil)

// recoveryState is the durable recovery state. All of it is guarded by Store.mu.
type recoveryState struct {
	pending  map[ids.TaskID]recovery.Pending
	window   *statestore.RecoveryWindow
	permits  map[ids.AttemptID]statestore.StoredPermit
	releases map[uint64]contract.Digest
}

func newRecoveryState() recoveryState {
	return recoveryState{
		pending:  make(map[ids.TaskID]recovery.Pending),
		permits:  make(map[ids.AttemptID]statestore.StoredPermit),
		releases: make(map[uint64]contract.Digest),
	}
}

func (s *Store) EnqueueRecovery(ctx context.Context, pending recovery.Pending) (recovery.Pending, bool, error) {
	if err := ctx.Err(); err != nil {
		return recovery.Pending{}, false, err
	}
	if err := pending.Validate(); err != nil {
		return recovery.Pending{}, false, fmt.Errorf("%w: %v", statestore.ErrInvalidRecovery, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	attempt, ok := s.attempts[pending.PreviousAttemptID]
	if !ok {
		return recovery.Pending{}, false, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, pending.PreviousAttemptID)
	}
	task, ok := s.tasks[pending.TaskID]
	if !ok {
		return recovery.Pending{}, false, fmt.Errorf("%w: task %s", statestore.ErrNotFound, pending.TaskID)
	}
	if existing, found := s.recovery.pending[pending.TaskID]; found {
		if existing.PreviousAttemptID == pending.PreviousAttemptID {
			return existing, true, nil
		}
		return recovery.Pending{}, false, fmt.Errorf("%w: task %s already waits for a retry of attempt %s",
			statestore.ErrConflict, pending.TaskID, existing.PreviousAttemptID)
	}
	if _, found := s.recovery.permits[pending.PreviousAttemptID]; found {
		return recovery.Pending{}, false, fmt.Errorf("%w: attempt %s already has a recovery Permit", statestore.ErrAlreadyExists, pending.PreviousAttemptID)
	}
	if attempt.TaskID != pending.TaskID || attempt.Phase != attemptstate.AttemptPhaseClosed ||
		!attempt.CloseReason.IsInterruption() || attempt.Number != uint64(pending.PreviousAttemptNumber) ||
		task.TaskIRDigest != pending.TaskIRDigest {
		return recovery.Pending{}, false, fmt.Errorf("%w: attempt %s is not an interrupted Attempt of task %s",
			statestore.ErrConflict, pending.PreviousAttemptID, pending.TaskID)
	}
	if !acceptsNewAttempt(task) {
		return recovery.Pending{}, false, fmt.Errorf("%w: task %s cannot take a new Attempt", statestore.ErrConflict, pending.TaskID)
	}
	for _, other := range s.attempts {
		if other.TaskID == pending.TaskID && other.Number == uint64(pending.NextAttemptNumber) {
			return recovery.Pending{}, false, fmt.Errorf("%w: attempt number %d of task %s already exists",
				statestore.ErrConflict, pending.NextAttemptNumber, pending.TaskID)
		}
	}
	s.recovery.pending[pending.TaskID] = pending
	return pending, false, nil
}

func acceptsNewAttempt(task taskstate.TaskRecord) bool {
	return !task.CancellationRequested &&
		(task.Phase == taskstate.TaskPhaseReady || task.Phase == taskstate.TaskPhaseActive)
}

func (s *Store) LoadRecoveryPending(ctx context.Context) ([]recovery.Pending, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]recovery.Pending, 0, len(s.recovery.pending))
	for _, p := range s.recovery.pending {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID.String() < out[j].TaskID.String() })
	return out, nil
}

func (s *Store) LoadRecoveryWindow(ctx context.Context) (statestore.RecoveryWindow, bool, error) {
	if err := ctx.Err(); err != nil {
		return statestore.RecoveryWindow{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.recovery.window == nil {
		return statestore.RecoveryWindow{}, false, nil
	}
	return *s.recovery.window, true, nil
}

func (s *Store) CommitRecoveryRelease(ctx context.Context, release statestore.RecoveryRelease) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := release.Validate(); err != nil {
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sequence := release.Decision.Sequence
	fingerprint := release.Fingerprint()
	if previous, found := s.recovery.releases[sequence]; found {
		if previous == fingerprint {
			return true, nil
		}
		return false, fmt.Errorf("%w: sequence %d was already committed with another content", statestore.ErrConflict, sequence)
	}
	var last uint64
	stored := recovery.Budget{}
	if w := s.recovery.window; w != nil {
		last = w.LastSequence
		// Budgets are never carried across policy revisions: their limits differ.
		if w.PolicyRevision == release.Policy.Revision {
			stored = w.Budget
		}
	}
	if sequence != last+1 {
		return false, fmt.Errorf("%w: sequence %d does not follow %d", statestore.ErrConflict, sequence, last)
	}
	if !sameStoredBudget(stored, release.ExpectedBudget) {
		return false, fmt.Errorf("%w: the pass started from a budget that is not the stored one", statestore.ErrConflict)
	}
	for _, permit := range release.Decision.Permits {
		pending, found := s.recovery.pending[permit.TaskID()]
		if !found || !samePending(pending, permit) {
			return false, fmt.Errorf("%w: no pending retry matches the Permit for task %s", statestore.ErrConflict, permit.TaskID())
		}
	}
	for _, expired := range release.Decision.Expired {
		pending, found := s.recovery.pending[expired.TaskID]
		if !found || pending.PreviousAttemptID != expired.PreviousAttemptID {
			return false, fmt.Errorf("%w: no pending retry matches the expired retry for task %s", statestore.ErrConflict, expired.TaskID)
		}
	}

	for _, permit := range release.Decision.Permits {
		delete(s.recovery.pending, permit.TaskID())
		s.recovery.permits[permit.PreviousAttemptID()] = statestore.StoredPermit{Permit: permit}
	}
	for _, expired := range release.Decision.Expired {
		delete(s.recovery.pending, expired.TaskID)
	}
	s.recovery.window = &statestore.RecoveryWindow{
		PolicyRevision: release.Policy.Revision, Budget: release.Decision.Budget, LastSequence: sequence,
	}
	s.recovery.releases[sequence] = fingerprint
	return false, nil
}

func sameStoredBudget(a, b recovery.Budget) bool {
	return a.WindowStart.Equal(b.WindowStart) && a.Released == b.Released &&
		a.TransferBytes == b.TransferBytes && a.ComputeMillis == b.ComputeMillis
}

// samePending reports whether the Permit was issued for exactly this retry.
func samePending(p recovery.Pending, permit recovery.Permit) bool {
	return p.TaskID == permit.TaskID() && p.TaskIRDigest == permit.TaskIRDigest() &&
		p.PreviousAttemptID == permit.PreviousAttemptID() &&
		p.PreviousAttemptNumber == permit.PreviousAttemptNumber() &&
		p.NextAttemptNumber == permit.NextAttemptNumber()
}

func (s *Store) LoadRecoveryPermit(ctx context.Context, previous ids.AttemptID) (statestore.StoredPermit, bool, error) {
	if err := ctx.Err(); err != nil {
		return statestore.StoredPermit{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored, ok := s.recovery.permits[previous]
	return stored, ok, nil
}

func (s *Store) AdmitRecoveryAttempt(ctx context.Context, admission statestore.RecoveryAdmission) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := admission.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stored, ok := s.recovery.permits[admission.PreviousAttemptID]
	if !ok {
		return fmt.Errorf("%w: no Permit for attempt %s", statestore.ErrNotFound, admission.PreviousAttemptID)
	}
	if stored.SpentBy != "" {
		return fmt.Errorf("%w: the Permit for attempt %s was used by %s", statestore.ErrAlreadyExists, admission.PreviousAttemptID, stored.SpentBy)
	}
	permit := stored.Permit
	if err := permit.Validate(admission.Now, recovery.Subject{
		TaskID: permit.TaskID(), TaskIRDigest: permit.TaskIRDigest(),
		PreviousAttemptID: permit.PreviousAttemptID(), PolicyRevision: admission.PolicyRevision,
	}); err != nil {
		return err
	}
	previous, ok := s.attempts[admission.PreviousAttemptID]
	if !ok || previous.Phase != attemptstate.AttemptPhaseClosed || !previous.CloseReason.IsInterruption() {
		return fmt.Errorf("%w: attempt %s is not an interrupted Attempt", statestore.ErrConflict, admission.PreviousAttemptID)
	}
	next := admission.NewAttempt
	if next.TaskID != permit.TaskID() || next.TaskIRDigest != permit.TaskIRDigest() ||
		next.Number != uint64(permit.NextAttemptNumber()) {
		return fmt.Errorf("%w: the Attempt is not the one the Permit admits", statestore.ErrConflict)
	}
	if err := s.createAttemptLocked(next); err != nil {
		return err
	}
	stored.SpentBy = next.ID
	s.recovery.permits[admission.PreviousAttemptID] = stored
	return nil
}
