// Package recoveryadmission runs the recovery boundary over the State Store: it
// turns an Attempt that was interrupted into a retry that waits, releases the
// retries a time window may carry, and creates the next Attempt from a Permit.
//
// The decisions are the pure functions of the recovery package. This package
// supplies what they must not take from a Worker: the Task's reliability terms
// and the recovery policy come from trusted sources, an interruption is derived
// from how an Attempt was closed, and every change goes through the State Store's
// atomic RecoveryStore operations.
//
//	closed Attempt  -> Intake  -> a retry waits (or the Task is held)
//	waiting retries -> Release -> Permits, within the window budget
//	Permit          -> Admit   -> the next Attempt, created CREATED
//
// A Permit is not a Lease and not a Placement Approval. The Attempt that Admit
// creates still has to be placed, approved, leased and signed like any other.
package recoveryadmission

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var (
	ErrInvalidConfig  = errors.New("invalid recovery admission configuration")
	ErrNotInterrupted = errors.New("the Attempt was not closed by an interruption")
	ErrNoPermit       = errors.New("there is no recovery Permit for the Attempt")
	ErrStateChanged   = errors.New("the recovery state changed while the pass was being committed")
)

// Terms is what an admitted Task says about retrying. It is read from the
// Task's contract, never from a Worker or from the Attempt that failed.
type Terms struct {
	Retryable   bool
	MaxAttempts uint32
	// Deadline is zero when the Task has none.
	Deadline time.Time
	Priority recovery.Priority
	// The estimates bound what one retry may cost a window.
	EstimatedTransferBytes uint64
	EstimatedComputeMillis uint64
}

// ContractSource returns the Terms of a Task. A Task it cannot describe must be an
// error: an unknown contract is never read as "retryable".
type ContractSource interface {
	RecoveryTerms(ctx context.Context, task ids.TaskID) (Terms, error)
}

// PolicySource returns the recovery policy in force.
type PolicySource interface {
	RecoveryPolicy(ctx context.Context) (recovery.Policy, error)
}

// Store is the State Store this package needs.
type Store interface {
	statestore.RecoveryStore
	statestore.TaskStore
	LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error)
}

type Clock interface{ Now() time.Time }

type Config struct {
	Store     Store
	Contracts ContractSource
	Policies  PolicySource
	Clock     Clock
	// NewAttemptID creates the ID of a new Attempt. Nil means ids.NewAttemptID.
	NewAttemptID func() (ids.AttemptID, error)
}

type Service struct {
	store     Store
	contracts ContractSource
	policies  PolicySource
	clock     Clock
	newID     func() (ids.AttemptID, error)
}

func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: store is required", ErrInvalidConfig)
	case cfg.Contracts == nil:
		return nil, fmt.Errorf("%w: contract source is required", ErrInvalidConfig)
	case cfg.Policies == nil:
		return nil, fmt.Errorf("%w: policy source is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}
	newID := cfg.NewAttemptID
	if newID == nil {
		newID = ids.NewAttemptID
	}
	return &Service{store: cfg.Store, contracts: cfg.Contracts, policies: cfg.Policies, clock: cfg.Clock, newID: newID}, nil
}

// IntakeResult says what became of an interrupted Attempt.
type IntakeResult struct {
	// Pending is the retry that waits. It is the stored one, so a repeat does not
	// move its start time. Zero when the Task is held.
	Pending  recovery.Pending
	Replayed bool
	// Hold is set when the Task will not be retried automatically. The Task stays
	// as it is for a person or an upper layer to decide; it is not marked failed.
	Hold *recovery.Hold
}

// Intake derives an interruption from a closed Attempt and either queues a retry
// or reports why the Task is held. It is idempotent: the same Attempt again gives
// the same stored retry.
func (s *Service) Intake(ctx context.Context, attemptID ids.AttemptID) (IntakeResult, error) {
	if err := s.ready(ctx); err != nil {
		return IntakeResult{}, err
	}
	attempt, err := s.store.LoadAttempt(ctx, attemptID)
	if err != nil {
		return IntakeResult{}, err
	}
	if attempt.Phase != attemptstate.AttemptPhaseClosed || !attempt.CloseReason.IsInterruption() {
		return IntakeResult{}, fmt.Errorf("%w: Attempt %s is %s (%q)", ErrNotInterrupted, attempt.ID, attempt.Phase, attempt.CloseReason)
	}
	task, err := s.store.LoadTask(ctx, attempt.TaskID)
	if err != nil {
		return IntakeResult{}, err
	}
	if task.Phase == taskstate.TaskPhaseTerminal {
		return IntakeResult{}, fmt.Errorf("%w: Task %s already finished", ErrNotInterrupted, task.ID)
	}
	terms, err := s.contracts.RecoveryTerms(ctx, attempt.TaskID)
	if err != nil {
		return IntakeResult{}, fmt.Errorf("read the contract of Task %s: %w", attempt.TaskID, err)
	}
	policy, err := s.policies.RecoveryPolicy(ctx)
	if err != nil {
		return IntakeResult{}, fmt.Errorf("read the recovery policy: %w", err)
	}

	intake, err := recovery.Classify(s.clock.Now().UTC(), policy, []recovery.Interruption{{
		AttemptID: attempt.ID, AttemptNumber: uint32(attempt.Number),
		Contract: recovery.Contract{
			TaskID: attempt.TaskID, TaskIRDigest: attempt.TaskIRDigest,
			Retryable: terms.Retryable, MaxAttempts: terms.MaxAttempts, Deadline: terms.Deadline,
		},
		Priority:               terms.Priority,
		EstimatedTransferBytes: terms.EstimatedTransferBytes, EstimatedComputeMillis: terms.EstimatedComputeMillis,
	}})
	if err != nil {
		return IntakeResult{}, err
	}
	if len(intake.Held) == 1 {
		hold := intake.Held[0]
		return IntakeResult{Hold: &hold}, nil
	}
	if len(intake.Pending) != 1 {
		return IntakeResult{}, fmt.Errorf("recovery classified Attempt %s into %d retries", attempt.ID, len(intake.Pending))
	}
	stored, replayed, err := s.store.EnqueueRecovery(ctx, intake.Pending[0])
	if err != nil {
		return IntakeResult{}, err
	}
	return IntakeResult{Pending: stored, Replayed: replayed}, nil
}

// ReleaseResult is the outcome of one pass.
type ReleaseResult struct {
	Permits  []recovery.Permit
	Deferred int
	Expired  int
	// Replayed is true when this pass had already been committed.
	Replayed bool
}

// Release runs one pass over the waiting retries and commits it. The window
// budget and the sequence come from the State Store, so a restart or a second
// caller cannot reset them. When another pass committed first, Release returns
// ErrStateChanged and the caller runs it again.
func (s *Service) Release(ctx context.Context) (ReleaseResult, error) {
	if err := s.ready(ctx); err != nil {
		return ReleaseResult{}, err
	}
	policy, err := s.policies.RecoveryPolicy(ctx)
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("read the recovery policy: %w", err)
	}
	pending, err := s.store.LoadRecoveryPending(ctx)
	if err != nil {
		return ReleaseResult{}, err
	}
	var (
		before recovery.Budget
		last   uint64
	)
	window, found, err := s.store.LoadRecoveryWindow(ctx)
	if err != nil {
		return ReleaseResult{}, err
	}
	if found {
		last = window.LastSequence
		// A budget is never carried into another policy revision.
		if window.PolicyRevision == policy.Revision {
			before = window.Budget
		}
	}

	now := s.clock.Now().UTC()
	decision, err := recovery.Release(now, policy, pending, before, last+1)
	if err != nil {
		return ReleaseResult{}, err
	}
	release := statestore.RecoveryRelease{Policy: policy, Now: now, ExpectedBudget: before, Decision: decision}
	replayed, err := s.store.CommitRecoveryRelease(ctx, release)
	if err != nil {
		if errors.Is(err, statestore.ErrConflict) {
			return ReleaseResult{}, fmt.Errorf("%w: %v", ErrStateChanged, err)
		}
		return ReleaseResult{}, err
	}
	return ReleaseResult{
		Permits: decision.Permits, Deferred: len(decision.Deferred), Expired: len(decision.Expired), Replayed: replayed,
	}, nil
}

// Admit creates the next Attempt from the Permit stored for a previous Attempt.
// It is idempotent: when the Permit was already used, it returns the Attempt it
// created. If the process stopped between committing the Permit and calling
// Admit, calling Admit later works until the Permit expires.
func (s *Service) Admit(ctx context.Context, previous ids.AttemptID) (attemptstate.AttemptRecord, error) {
	if err := s.ready(ctx); err != nil {
		return attemptstate.AttemptRecord{}, err
	}
	stored, found, err := s.store.LoadRecoveryPermit(ctx, previous)
	if err != nil {
		return attemptstate.AttemptRecord{}, err
	}
	if !found {
		return attemptstate.AttemptRecord{}, fmt.Errorf("%w: %s", ErrNoPermit, previous)
	}
	if stored.SpentBy != "" {
		return s.store.LoadAttempt(ctx, stored.SpentBy)
	}
	policy, err := s.policies.RecoveryPolicy(ctx)
	if err != nil {
		return attemptstate.AttemptRecord{}, fmt.Errorf("read the recovery policy: %w", err)
	}
	now := s.clock.Now().UTC()
	id, err := s.newID()
	if err != nil {
		return attemptstate.AttemptRecord{}, err
	}
	permit := stored.Permit
	next, err := attemptstate.New(id, permit.TaskID(), permit.TaskIRDigest(), uint64(permit.NextAttemptNumber()), now)
	if err != nil {
		return attemptstate.AttemptRecord{}, err
	}
	if err := s.store.AdmitRecoveryAttempt(ctx, statestore.RecoveryAdmission{
		PreviousAttemptID: previous, NewAttempt: next, PolicyRevision: policy.Revision, Now: now,
	}); err != nil {
		if errors.Is(err, statestore.ErrAlreadyExists) {
			// Another call used the Permit between the read and the write.
			if again, ok, loadErr := s.store.LoadRecoveryPermit(ctx, previous); loadErr == nil && ok && again.SpentBy != "" {
				return s.store.LoadAttempt(ctx, again.SpentBy)
			}
		}
		return attemptstate.AttemptRecord{}, err
	}
	return next, nil
}

func (s *Service) ready(ctx context.Context) error {
	if s == nil || s.store == nil || s.contracts == nil || s.policies == nil || s.clock == nil || s.newID == nil {
		return fmt.Errorf("%w: service is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidConfig)
	}
	return ctx.Err()
}
