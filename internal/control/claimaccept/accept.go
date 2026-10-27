// Package claimaccept applies a Worker's ResultClaim to the Control Plane's
// Attempt state.
//
// It reads the Attempt, its Lease, and any claim already recorded for that
// Attempt, asks resultclaim.Decide what the claim means, records the claim once,
// and moves the Attempt to Verification or to a terminal state. The claim is
// never accepted as a result: a completed claim only opens the Verification
// step.
//
// The claim store is a port so that the durable adapter and its transaction
// boundary can be added without changing this policy.
package claimaccept

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var (
	ErrInvalidConfig   = errors.New("invalid claim acceptance configuration")
	ErrBindingMismatch = errors.New("the claim does not match the stored Attempt")
	ErrWrongPhase      = errors.New("the Attempt cannot accept this claim in its current phase")
	ErrClaimStore      = errors.New("the claim could not be recorded")
)

type AttemptStore interface {
	LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error)
	CompareAndSwapAttempt(ctx context.Context, id ids.AttemptID, expectedRevision uint64, next attemptstate.AttemptRecord) error
}

type LeaseStore interface {
	LoadLease(ctx context.Context, id ids.LeaseID) (leasestate.LeaseRecord, error)
}

type Clock interface{ Now() time.Time }

type Config struct {
	Attempts AttemptStore
	Leases   LeaseStore
	Claims   statestore.ResultClaimStore
	Clock    Clock
}

type Service struct {
	attempts AttemptStore
	leases   LeaseStore
	claims   statestore.ResultClaimStore
	clock    Clock
}

func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Attempts == nil:
		return nil, fmt.Errorf("%w: Attempt store is required", ErrInvalidConfig)
	case cfg.Leases == nil:
		return nil, fmt.Errorf("%w: Lease store is required", ErrInvalidConfig)
	case cfg.Claims == nil:
		return nil, fmt.Errorf("%w: Claim store is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}
	return &Service{attempts: cfg.Attempts, leases: cfg.Leases, claims: cfg.Claims, clock: cfg.Clock}, nil
}

// Result reports what happened to one claim. VerificationRequired is true only
// when the Attempt moved to A-VERIFYING; it is not an Accepted Result.
type Result struct {
	Decision             resultclaim.Decision
	Attempt              attemptstate.AttemptRecord
	VerificationRequired bool
}

// Accept applies one claim. It is idempotent for an exact replay: the claim is
// not recorded twice, and a close that previously crashed is retried.
func (s *Service) Accept(ctx context.Context, claim resultclaim.Claim) (Result, error) {
	if s == nil || s.attempts == nil || s.leases == nil || s.claims == nil || s.clock == nil {
		return Result{}, fmt.Errorf("%w: service is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is required", ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if !claim.Valid() {
		return Result{}, resultclaim.ErrInvalidClaim
	}
	now := s.clock.Now().UTC()
	if now.IsZero() {
		return Result{}, fmt.Errorf("%w: clock returned zero time", ErrInvalidConfig)
	}

	attempt, err := s.attempts.LoadAttempt(ctx, claim.AttemptID())
	if err != nil {
		return Result{}, err
	}
	if err := checkBinding(attempt, claim); err != nil {
		return Result{}, err
	}
	lease, err := s.leases.LoadLease(ctx, attempt.LeaseID)
	if err != nil {
		return Result{}, err
	}
	if claim.FenceEpoch() > lease.FenceEpoch {
		return Result{}, fmt.Errorf("%w: claim fence %d is ahead of the Lease fence %d",
			ErrBindingMismatch, claim.FenceEpoch(), lease.FenceEpoch)
	}
	recorded, found, err := s.claims.LoadResultClaim(ctx, claim.AttemptID())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrClaimStore, err)
	}
	var recordedPtr *resultclaim.Claim
	if found {
		recordedPtr = &recorded
	}

	decision, err := resultclaim.Decide(claim, resultclaim.Existing{
		AttemptID:    claim.AttemptID(),
		AttemptOpen:  attempt.Phase != attemptstate.AttemptPhaseClosed,
		CurrentFence: lease.FenceEpoch,
		Recorded:     recordedPtr,
	})
	if err != nil {
		return Result{}, err
	}

	verification := claim.Status().NeedsVerification()
	applyTransition := attempt.Phase != attemptstate.AttemptPhaseClosed &&
		decision.Kind != resultclaim.KindLate && decision.Kind != resultclaim.KindStale
	var (
		next            attemptstate.AttemptRecord
		needsTransition bool
	)
	switch {
	case applyTransition && verification:
		if err := checkVerificationPhase(attempt.Phase); err != nil {
			return Result{}, err
		}
		// A completed claim from DISPATCHED needs two transitions. The first
		// one (CLAIMED) carries no claim, so it is a plain CAS; the second one
		// commits together with the claim below.
		if attempt.Phase == attemptstate.AttemptPhaseDispatched {
			claimed := attempt
			claimed.Phase = attemptstate.AttemptPhaseClaimed
			claimed.Revision++
			claimed.UpdatedAt = laterOf(now, attempt.UpdatedAt)
			if err := s.attempts.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, claimed); err != nil {
				return Result{}, err
			}
			attempt = claimed
		}
		if attempt.Phase == attemptstate.AttemptPhaseClaimed {
			next = attempt
			next.Phase = attemptstate.AttemptPhaseVerifying
			next.Revision++
			next.UpdatedAt = laterOf(now, attempt.UpdatedAt)
			needsTransition = true
		}
	case decision.CloseAttempt && attempt.Phase != attemptstate.AttemptPhaseClosed:
		next = attempt
		next.Phase = attemptstate.AttemptPhaseClosed
		next.CloseReason = closeReason(claim.Status())
		next.Revision++
		next.UpdatedAt = laterOf(now, attempt.UpdatedAt)
		needsTransition = true
	}

	if decision.Record || needsTransition {
		acceptance := statestore.ResultClaimAcceptance{
			Claim:                   claim,
			Record:                  decision.Record,
			ApplyTransition:         needsTransition,
			ExpectedAttemptRevision: attempt.Revision,
			NextAttempt:             next,
		}
		if err := s.claims.ApplyResultClaim(ctx, acceptance); err != nil {
			return Result{}, fmt.Errorf("%w: %v", ErrClaimStore, err)
		}
	}
	resultAttempt := attempt
	if needsTransition {
		resultAttempt = next
	}
	return Result{
		Decision:             decision,
		Attempt:              resultAttempt,
		VerificationRequired: resultAttempt.Phase == attemptstate.AttemptPhaseVerifying,
	}, nil
}

func checkBinding(attempt attemptstate.AttemptRecord, claim resultclaim.Claim) error {
	switch {
	case attempt.TaskID != claim.TaskID():
		return fmt.Errorf("%w: Task differs", ErrBindingMismatch)
	case attempt.TaskIRDigest != claim.TaskIRDigest():
		return fmt.Errorf("%w: TaskIR differs", ErrBindingMismatch)
	case attempt.WorkerIdentity != claim.WorkerIdentity():
		return fmt.Errorf("%w: Worker differs", ErrBindingMismatch)
	case attempt.LeaseID != claim.LeaseID():
		return fmt.Errorf("%w: Lease differs", ErrBindingMismatch)
	}
	return nil
}

func checkVerificationPhase(phase attemptstate.AttemptPhase) error {
	switch phase {
	case attemptstate.AttemptPhaseDispatched, attemptstate.AttemptPhaseClaimed, attemptstate.AttemptPhaseVerifying:
		return nil
	}
	return fmt.Errorf("%w: phase %q cannot move to verifying", ErrWrongPhase, phase)
}

func closeReason(status resultclaim.Status) attemptstate.CloseReason {
	switch status {
	case resultclaim.StatusFailed:
		return attemptstate.CloseReasonExecutionFailureConfirmed
	case resultclaim.StatusReclaimed:
		return attemptstate.CloseReasonReclaimed
	case resultclaim.StatusDeadlineExceeded:
		return attemptstate.CloseReasonDeadline
	case resultclaim.StatusCancelled:
		return attemptstate.CloseReasonCancelled
	case resultclaim.StatusOutputExceeded:
		return attemptstate.CloseReasonBudgetExhausted
	}
	return attemptstate.CloseReasonNone
}

// laterOf keeps a record's time from moving backwards when the wall clock
// has stepped back since the record was last written.
func laterOf(now, recorded time.Time) time.Time {
	if recorded.After(now) {
		return recorded
	}
	return now
}
