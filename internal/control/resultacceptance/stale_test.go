package resultacceptance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/recoveryadmission"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// supersede places the fixture's Task again on a second Attempt after the
// first Lease expired, so the first Attempt no longer holds the latest fence.
func (f *fixture) supersede(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	lease, err := f.store.LoadLease(ctx, f.attempt.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	a2ID, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	a2, err := attemptstate.New(a2ID, f.task.ID, f.task.TaskIRDigest, 2, lease.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateAttempt(ctx, a2); err != nil {
		t.Fatal(err)
	}
	placing := a2
	placing.Phase = attemptstate.AttemptPhasePlacing
	placing.WorkerIdentity = "worker-1"
	placing.Revision++
	if err := f.store.CompareAndSwapAttempt(ctx, a2.ID, a2.Revision, placing); err != nil {
		t.Fatal(err)
	}
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	issued := lease.ExpiresAt.Add(time.Minute)
	reserved, err := f.store.ReserveLease(ctx, statestore.LeaseReservation{
		LeaseID: leaseID, TaskID: f.task.ID, TaskIRDigest: f.task.TaskIRDigest,
		ExpectedTaskRevision: f.task.Revision, AttemptID: a2.ID, ExpectedAttemptRevision: placing.Revision,
		WorkerIdentity: "worker-1", PlacementTrustDomain: "local.example",
		PlacementAuthorityPrincipal: "control-plane/plc-1", PlacementDecisionID: "placement-2",
		PlacementPayloadDigest: contract.SHA256([]byte("placement-2")), PlacementExpiresAt: issued.Add(2 * time.Hour),
		PolicyRevision: "policy-1", ExpectedPolicyEpoch: 1,
		IssuedAt: issued, ExpiresAt: issued.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("ReserveLease(A2) error = %v", err)
	}
	if reserved.Lease.FenceEpoch <= lease.FenceEpoch {
		t.Fatalf("A2 fence %d does not supersede A1 fence %d", reserved.Lease.FenceEpoch, lease.FenceEpoch)
	}
	if f.task, err = f.store.LoadTask(ctx, f.task.ID); err != nil {
		t.Fatal(err)
	}
	f.clock.now = issued.Add(time.Minute)
}

type retryableTerms struct{}

func (retryableTerms) RecoveryTerms(context.Context, ids.TaskID) (recoveryadmission.Terms, error) {
	return recoveryadmission.Terms{Retryable: true, MaxAttempts: 3, Priority: recovery.PriorityNormal}, nil
}

type unusedRecoveryPolicy struct{}

func (unusedRecoveryPolicy) RecoveryPolicy(context.Context) (recovery.Policy, error) {
	return recovery.Policy{}, errors.New("not used")
}

// V3 follow-up of #213: an Attempt whose fence was superseded is refused with a
// distinct, non-retryable error, is closed as stale_attempt instead of waiting
// in A-VERIFYING forever, and does not enter recovery.
func TestAcceptClosesAnAttemptWhoseFenceWasSuperseded(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	ctx := context.Background()
	f.supersede(t)

	_, err := f.service.Accept(ctx, f.request(f.twoIndependentVerifiers()...))
	if !errors.Is(err, ErrSuperseded) || errors.Is(err, ErrStateChanged) {
		t.Fatalf("Accept() error = %v, want %v and not a retryable race", err, ErrSuperseded)
	}
	if !errors.Is(err, statestore.ErrStaleFence) {
		t.Fatalf("Accept() error = %v, want it to keep the cause %v", err, statestore.ErrStaleFence)
	}

	attempt, err := f.store.LoadAttempt(ctx, f.attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Phase != attemptstate.AttemptPhaseClosed || attempt.CloseReason != attemptstate.CloseReasonStaleAttempt ||
		attempt.Revision != f.attempt.Revision+1 {
		t.Fatalf("Attempt = %+v, want closed as %s", attempt, attemptstate.CloseReasonStaleAttempt)
	}
	task, err := f.store.LoadTask(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Phase != f.task.Phase || task.Revision != f.task.Revision {
		t.Fatalf("the Task changed: %+v, want %+v", task, f.task)
	}
	if _, ok, err := f.store.LoadAcceptedResult(ctx, f.task.ID); err != nil || ok {
		t.Fatalf("an accepted result exists: %v, %v", ok, err)
	}

	// A repeat gives the same answer and changes nothing.
	if _, err := f.service.Accept(ctx, f.request(f.twoIndependentVerifiers()...)); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("repeat Accept() error = %v, want %v", err, ErrSuperseded)
	}
	if again, _ := f.store.LoadAttempt(ctx, f.attempt.ID); again.Revision != attempt.Revision {
		t.Fatalf("the repeat changed the Attempt: %+v", again)
	}

	// The superseded Attempt is not an interruption, so recovery does not
	// queue a retry for it and the Task's attempt budget is not used again.
	admission, err := recoveryadmission.New(recoveryadmission.Config{
		Store: f.store, Contracts: retryableTerms{}, Policies: unusedRecoveryPolicy{}, Clock: f.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admission.Intake(ctx, f.attempt.ID); !errors.Is(err, recoveryadmission.ErrNotInterrupted) {
		t.Fatalf("Intake() error = %v, want %v", err, recoveryadmission.ErrNotInterrupted)
	}
	if pending, err := f.store.LoadRecoveryPending(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("recovery pending = %+v, %v, want none", pending, err)
	}
}

// staleRaceStore reports a superseded fence and then loses the close to a
// concurrent change that is not a stale close.
type staleRaceStore struct {
	statestore.ResultAcceptanceStore
}

func (staleRaceStore) CommitAcceptedResult(context.Context, statestore.ResultAcceptanceCommit) error {
	return statestore.ErrStaleFence
}

func (staleRaceStore) CloseStaleAttempt(context.Context, statestore.StaleAttemptClose) error {
	return statestore.ErrConflict
}

// If the stale close loses to some other change, Accept does not claim the
// Attempt was closed as superseded.
func TestAcceptReportsAChangeWhenTheStaleCloseLosesARace(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	svc, err := New(Config{
		Attempts: f.store, Tasks: f.store, Claims: f.store, Results: staleRaceStore{f.store}, Rejections: f.store,
		Policies: f.policy, Producers: producerSource{dims: producerDims()}, Clock: f.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Accept(context.Background(), f.request(f.twoIndependentVerifiers()...))
	if !errors.Is(err, ErrStateChanged) || errors.Is(err, ErrSuperseded) {
		t.Fatalf("Accept() error = %v, want %v", err, ErrStateChanged)
	}
	f.assertUntouched(t)
}
