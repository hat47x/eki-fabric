// Package leaseissue composes the LSE-1 Lease issuance boundaries.
//
// Service reserves a Lease: it accepts signed Placement Approval bytes, not a
// caller-built LeaseReservation. Authority and subject fields are derived only
// from the cryptographically verified Placement Approval and current durable
// state. Emitter then signs a RESERVED Lease and durably commits it as EMITTED
// or ABORTED; only EMITTED bytes are worker-facing Authority.
package leaseissue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/placementverify"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var (
	ErrInvalidConfig     = errors.New("invalid LSE-1 lease issue configuration")
	ErrInvalidRequest    = errors.New("invalid LSE-1 lease issue request")
	ErrCurrentState      = errors.New("current state does not match verified Placement Approval")
	ErrPlacementConsumed = errors.New("Placement Approval already maps to an expired Lease")
	ErrStoreInvariant    = errors.New("Lease Store returned a record outside the verified Placement binding")
)

type State interface {
	LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error)
	LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error)
	LoadCurrentPolicy(ctx context.Context) (policystate.State, error)
	ReserveLease(ctx context.Context, reservation statestore.LeaseReservation) (statestore.LeaseReservationResult, error)
}

type TrustBundleSource interface {
	CurrentTrustBundle(ctx context.Context) (decisiontrust.TrustBundleMetadata, error)
}

type Clock interface {
	Now() time.Time
}

type Config struct {
	Verifier    *placementverify.Verifier
	State       State
	TrustBundle TrustBundleSource
	Clock       Clock
	MaxLifetime time.Duration
}

type Service struct {
	verifier    *placementverify.Verifier
	state       State
	trustBundle TrustBundleSource
	clock       Clock
	maxLifetime time.Duration
}

func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Verifier == nil:
		return nil, fmt.Errorf("%w: Placement verifier is required", ErrInvalidConfig)
	case cfg.State == nil:
		return nil, fmt.Errorf("%w: State is required", ErrInvalidConfig)
	case cfg.TrustBundle == nil:
		return nil, fmt.Errorf("%w: Trust Bundle source is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: trusted Clock is required", ErrInvalidConfig)
	case cfg.MaxLifetime <= 0:
		return nil, fmt.Errorf("%w: maximum Lease lifetime must be positive", ErrInvalidConfig)
	}
	return &Service{
		verifier:    cfg.Verifier,
		state:       cfg.State,
		trustBundle: cfg.TrustBundle,
		clock:       cfg.Clock,
		maxLifetime: cfg.MaxLifetime,
	}, nil
}

type Request struct {
	SignedPlacement   []byte
	RequestedLifetime time.Duration
}

type Result struct {
	Lease    leasestate.LeaseRecord
	Replayed bool
}

func (s *Service) Reserve(ctx context.Context, req Request) (Result, error) {
	if s == nil || s.verifier == nil || s.state == nil || s.trustBundle == nil || s.clock == nil {
		return Result{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(req.SignedPlacement) == 0 {
		return Result{}, fmt.Errorf("%w: signed Placement Approval is required", ErrInvalidRequest)
	}
	if req.RequestedLifetime <= 0 {
		return Result{}, fmt.Errorf("%w: requested Lease lifetime must be positive", ErrInvalidRequest)
	}
	now := s.clock.Now().UTC()
	if now.IsZero() {
		return Result{}, fmt.Errorf("%w: trusted Clock returned zero time", ErrInvalidConfig)
	}

	bundle, err := s.trustBundle.CurrentTrustBundle(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("read current Trust Bundle: %w", err)
	}
	verified, err := s.verifier.Verify(placementverify.Request{
		Raw:         req.SignedPlacement,
		Now:         now,
		TrustBundle: bundle,
	})
	if err != nil {
		return Result{}, err
	}
	approval := verified.Approval()

	task, err := s.state.LoadTask(ctx, approval.TaskID())
	if err != nil {
		return Result{}, fmt.Errorf("load Placement Task %s: %w", approval.TaskID(), err)
	}
	attempt, err := s.state.LoadAttempt(ctx, approval.AttemptID())
	if err != nil {
		return Result{}, fmt.Errorf("load Placement Attempt %s: %w", approval.AttemptID(), err)
	}
	if err := validateCurrentAttempt(task, attempt, approval.WorkerIdentity()); err != nil {
		return Result{}, err
	}

	policy, err := s.state.LoadCurrentPolicy(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("read current durable policy state: %w", err)
	}
	if policy.Status != policystate.StatusActive {
		return Result{}, fmt.Errorf("%w: revision=%s epoch=%d", statestore.ErrPolicyRevoked, policy.Revision, policy.Epoch)
	}
	if err := verified.Revalidate(placementverify.Current{
		Task:           task,
		AttemptID:      attempt.ID,
		WorkerIdentity: attempt.WorkerIdentity,
		PolicyRevision: policy.Revision,
		Now:            now,
	}); err != nil {
		return Result{}, err
	}

	expiresAt := now.Add(req.RequestedLifetime)
	maxExpiresAt := now.Add(s.maxLifetime)
	if expiresAt.After(maxExpiresAt) {
		expiresAt = maxExpiresAt
	}
	if expiresAt.After(approval.ExpiresAt()) {
		expiresAt = approval.ExpiresAt()
	}
	if !now.Before(expiresAt) {
		return Result{}, fmt.Errorf("%w: no positive Lease window remains", ErrInvalidRequest)
	}

	leaseID, err := ids.NewLeaseID()
	if err != nil {
		return Result{}, fmt.Errorf("generate Lease ID: %w", err)
	}
	reservation := statestore.LeaseReservation{
		LeaseID:                     leaseID,
		TaskID:                      approval.TaskID(),
		TaskIRDigest:                approval.TaskIRDigest(),
		ExpectedTaskRevision:        task.Revision,
		AttemptID:                   approval.AttemptID(),
		ExpectedAttemptRevision:     attempt.Revision,
		WorkerIdentity:              approval.WorkerIdentity(),
		PlacementTrustDomain:        verified.TrustDomain(),
		PlacementAuthorityPrincipal: approval.AuthorityPrincipal(),
		PlacementDecisionID:         approval.DecisionID(),
		PlacementPayloadDigest:      verified.PayloadDigest(),
		PlacementExpiresAt:          approval.ExpiresAt(),
		PolicyRevision:              approval.PolicyRevision(),
		ExpectedPolicyEpoch:         policy.Epoch,
		IssuedAt:                    now,
		ExpiresAt:                   expiresAt,
	}
	stored, err := s.state.ReserveLease(ctx, reservation)
	if err != nil {
		return Result{}, err
	}
	if !reservation.MatchesReservedPlacement(stored.Lease) {
		return Result{}, ErrStoreInvariant
	}
	if stored.Lease.ID == "" || stored.Lease.FenceEpoch == 0 || !stored.Lease.PolicyFenced() {
		return Result{}, ErrStoreInvariant
	}
	if stored.Replayed && !stored.Lease.ActiveAt(now) {
		return Result{Lease: stored.Lease, Replayed: true}, ErrPlacementConsumed
	}
	return Result{Lease: stored.Lease, Replayed: stored.Replayed}, nil
}

func validateCurrentAttempt(task taskstate.TaskRecord, attempt attemptstate.AttemptRecord, approvedWorker string) error {
	if err := task.Validate(); err != nil {
		return fmt.Errorf("%w: Task: %v", ErrCurrentState, err)
	}
	if err := attempt.Validate(); err != nil {
		return fmt.Errorf("%w: Attempt: %v", ErrCurrentState, err)
	}
	if attempt.TaskID != task.ID || attempt.TaskIRDigest != task.TaskIRDigest {
		return fmt.Errorf("%w: Attempt does not belong to current Task/TaskIR", ErrCurrentState)
	}
	if attempt.WorkerIdentity != approvedWorker {
		return fmt.Errorf("%w: Attempt Worker %q differs from approved Worker %q", ErrCurrentState, attempt.WorkerIdentity, approvedWorker)
	}
	switch attempt.Phase {
	case attemptstate.AttemptPhasePlacing,
		attemptstate.AttemptPhaseAssigned,
		attemptstate.AttemptPhaseDispatched,
		attemptstate.AttemptPhaseClaimed,
		attemptstate.AttemptPhaseVerifying:
		return nil
	default:
		return fmt.Errorf("%w: Attempt phase %q cannot issue or replay a Lease", ErrCurrentState, attempt.Phase)
	}
}
