package recoveryadmission

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var baseTime = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type contracts struct {
	terms Terms
	err   error
}

func (c *contracts) RecoveryTerms(context.Context, ids.TaskID) (Terms, error) { return c.terms, c.err }

type policies struct{ policy recovery.Policy }

func (p *policies) RecoveryPolicy(context.Context) (recovery.Policy, error) { return p.policy, nil }

func testPolicy() recovery.Policy {
	return recovery.Policy{
		Revision: 7, BaseBackoff: time.Second, MaxBackoff: 30 * time.Second, Window: time.Minute,
		MaxReleasesPerWindow: 5, MaxTransferBytesPerWindow: 1 << 30, MaxComputeMillisPerWindow: 600_000,
		PermitTTL: 10 * time.Minute,
	}
}

type fixture struct {
	t         *testing.T
	store     *memory.Store
	service   *Service
	clock     *fakeClock
	contracts *contracts
	policies  *policies
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := memory.New()
	f := &fixture{
		t: t, store: store, clock: &fakeClock{now: baseTime},
		contracts: &contracts{terms: Terms{
			Retryable: true, MaxAttempts: 3, Priority: recovery.PriorityNormal,
			EstimatedTransferBytes: 1 << 20, EstimatedComputeMillis: 100,
		}},
		policies: &policies{policy: testPolicy()},
	}
	f.service = f.build(store)
	return f
}

func (f *fixture) build(store Store) *Service {
	f.t.Helper()
	svc, err := New(Config{Store: store, Contracts: f.contracts, Policies: f.policies, Clock: f.clock})
	if err != nil {
		f.t.Fatal(err)
	}
	return svc
}

// interrupted creates a Task with one Attempt that was closed for the reason.
func (f *fixture) interrupted(seed string, reason attemptstate.CloseReason) (taskstate.TaskRecord, attemptstate.AttemptRecord) {
	f.t.Helper()
	ctx := context.Background()
	taskID, err := ids.NewTaskID()
	if err != nil {
		f.t.Fatal(err)
	}
	task, err := taskstate.NewTask(taskID, contract.SHA256([]byte(seed)), baseTime.Add(-time.Hour))
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.CreateTask(ctx, task); err != nil {
		f.t.Fatal(err)
	}
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.UpdatedAt.Add(time.Second)
	if err := f.store.CompareAndSwapTask(ctx, task.ID, task.Revision, ready); err != nil {
		f.t.Fatal(err)
	}
	attemptID, err := ids.NewAttemptID()
	if err != nil {
		f.t.Fatal(err)
	}
	attempt, err := attemptstate.New(attemptID, ready.ID, ready.TaskIRDigest, 1, ready.UpdatedAt)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.CreateAttempt(ctx, attempt); err != nil {
		f.t.Fatal(err)
	}
	closed := attempt
	closed.Phase, closed.CloseReason = attemptstate.AttemptPhaseClosed, reason
	closed.Revision++
	closed.UpdatedAt = attempt.UpdatedAt.Add(time.Second)
	if err := f.store.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, closed); err != nil {
		f.t.Fatal(err)
	}
	return ready, closed
}

func TestIntakeQueuesARetryForAnInterruptedAttemptAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	_, attempt := f.interrupted("intake", attemptstate.CloseReasonReclaimed)

	first, err := f.service.Intake(context.Background(), attempt.ID)
	if err != nil || first.Replayed || first.Hold != nil || first.Pending.PreviousAttemptID != attempt.ID ||
		first.Pending.NextAttemptNumber != 2 {
		t.Fatalf("Intake() = %+v, %v", first, err)
	}
	// A repeat later returns the stored retry. Its start time does not move.
	f.clock.now = f.clock.now.Add(time.Hour)
	again, err := f.service.Intake(context.Background(), attempt.ID)
	if err != nil || !again.Replayed || again.Pending != first.Pending {
		t.Fatalf("repeat = %+v, %v, want the stored retry %+v", again, err, first.Pending)
	}
}

func TestIntakeHoldsATaskThatMustNotBeRetriedAutomatically(t *testing.T) {
	cases := map[string]struct {
		terms  Terms
		reason recovery.HoldReason
	}{
		"not retryable":    {terms: Terms{Retryable: false, MaxAttempts: 3}, reason: recovery.HoldNotRetryable},
		"attempts used up": {terms: Terms{Retryable: true, MaxAttempts: 1}, reason: recovery.HoldRetryBudgetExhausted},
	}
	for name, tc := range cases {
		f := newFixture(t)
		f.contracts.terms = tc.terms
		task, attempt := f.interrupted("hold-"+name, attemptstate.CloseReasonReclaimed)
		got, err := f.service.Intake(context.Background(), attempt.ID)
		if err != nil || got.Hold == nil || got.Hold.Reason != tc.reason {
			t.Fatalf("%s: Intake() = %+v, %v, want a hold for %s", name, got, err, tc.reason)
		}
		if pending, _ := f.store.LoadRecoveryPending(context.Background()); len(pending) != 0 {
			t.Fatalf("%s: a held Task was queued: %+v", name, pending)
		}
		// Holding does not fail the Task.
		if after, _ := f.store.LoadTask(context.Background(), task.ID); after.Phase != task.Phase || after.Revision != task.Revision {
			t.Fatalf("%s: holding changed the Task: %+v", name, after)
		}
	}
}

func TestIntakeRefusesAnAttemptThatWasNotInterrupted(t *testing.T) {
	f := newFixture(t)
	for _, reason := range []attemptstate.CloseReason{
		attemptstate.CloseReasonResultAccepted, attemptstate.CloseReasonCancelled, attemptstate.CloseReasonSuperseded,
		attemptstate.CloseReasonStaleAttempt,
	} {
		_, attempt := f.interrupted("not-interrupted-"+string(reason), reason)
		if _, err := f.service.Intake(context.Background(), attempt.ID); !errors.Is(err, ErrNotInterrupted) {
			t.Fatalf("%s: Intake() error = %v, want %v", reason, err, ErrNotInterrupted)
		}
	}
}

func TestIntakeFailsClosedWhenTheContractCannotBeRead(t *testing.T) {
	f := newFixture(t)
	_, attempt := f.interrupted("contract-down", attemptstate.CloseReasonReclaimed)
	f.contracts.err = errors.New("the Task contract is unavailable")
	if _, err := f.service.Intake(context.Background(), attempt.ID); err == nil {
		t.Fatal("a Task with an unreadable contract was queued")
	}
	if pending, _ := f.store.LoadRecoveryPending(context.Background()); len(pending) != 0 {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestReleaseAndAdmitCarryARetryToANewAttempt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	task, attempt := f.interrupted("release-admit", attemptstate.CloseReasonReclaimed)
	queued, err := f.service.Intake(ctx, attempt.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Before its start time nothing is released.
	early, err := f.service.Release(ctx)
	if err != nil || len(early.Permits) != 0 || early.Deferred != 1 {
		t.Fatalf("early Release() = %+v, %v", early, err)
	}
	if _, err := f.service.Admit(ctx, attempt.ID); !errors.Is(err, ErrNoPermit) {
		t.Fatalf("Admit() before a Permit error = %v, want %v", err, ErrNoPermit)
	}

	f.clock.now = queued.Pending.NotBefore
	released, err := f.service.Release(ctx)
	if err != nil || len(released.Permits) != 1 {
		t.Fatalf("Release() = %+v, %v", released, err)
	}
	next, err := f.service.Admit(ctx, attempt.ID)
	if err != nil || next.Number != 2 || next.TaskID != task.ID || next.Phase != attemptstate.AttemptPhaseCreated {
		t.Fatalf("Admit() = %+v, %v", next, err)
	}
	// Admit again returns the Attempt it created. It does not create another.
	again, err := f.service.Admit(ctx, attempt.ID)
	if err != nil || again.ID != next.ID {
		t.Fatalf("second Admit() = %+v, %v, want %s", again, err, next.ID)
	}
}

func TestReleaseKeepsTheWindowAcrossAServiceRestart(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.policies.policy.MaxReleasesPerWindow = 1
	_, a := f.interrupted("window-a", attemptstate.CloseReasonReclaimed)
	_, b := f.interrupted("window-b", attemptstate.CloseReasonReclaimed)
	qa, err := f.service.Intake(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	qb, err := f.service.Intake(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.now = qa.Pending.NotBefore
	if qb.Pending.NotBefore.After(f.clock.now) {
		f.clock.now = qb.Pending.NotBefore
	}

	first, err := f.service.Release(ctx)
	if err != nil || len(first.Permits) != 1 || first.Deferred != 1 {
		t.Fatalf("first Release() = %+v, %v", first, err)
	}

	// A new service over the same store: the budget the window consumed is not reset.
	restarted := f.build(f.store)
	f.clock.now = f.clock.now.Add(10 * time.Second)
	second, err := restarted.Release(ctx)
	if err != nil || len(second.Permits) != 0 {
		t.Fatalf("Release() after a restart = %+v, %v, want the spent window to hold", second, err)
	}
	f.clock.now = f.clock.now.Add(2 * time.Minute)
	third, err := restarted.Release(ctx)
	if err != nil || len(third.Permits) != 1 {
		t.Fatalf("Release() in the next window = %+v, %v", third, err)
	}
}

type conflictingStore struct{ Store }

func (conflictingStore) CommitRecoveryRelease(context.Context, statestore.RecoveryRelease) (bool, error) {
	return false, statestore.ErrConflict
}

func TestReleaseReportsThatAnotherPassCommittedFirst(t *testing.T) {
	f := newFixture(t)
	_, attempt := f.interrupted("race", attemptstate.CloseReasonReclaimed)
	queued, err := f.service.Intake(context.Background(), attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.now = queued.Pending.NotBefore
	racing := f.build(conflictingStore{f.store})
	if _, err := racing.Release(context.Background()); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("Release() error = %v, want %v", err, ErrStateChanged)
	}
}

func TestAdmitRefusesAPermitThatExpiredAndCreatesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, attempt := f.interrupted("expired-permit", attemptstate.CloseReasonReclaimed)
	queued, err := f.service.Intake(ctx, attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.now = queued.Pending.NotBefore
	released, err := f.service.Release(ctx)
	if err != nil || len(released.Permits) != 1 {
		t.Fatalf("Release() = %+v, %v", released, err)
	}

	f.clock.now = released.Permits[0].ExpiresAt().Add(time.Second)
	if _, err := f.service.Admit(ctx, attempt.ID); !errors.Is(err, recovery.ErrPermitExpired) {
		t.Fatalf("Admit() error = %v, want %v", err, recovery.ErrPermitExpired)
	}
	stored, _, _ := f.store.LoadRecoveryPermit(ctx, attempt.ID)
	if stored.SpentBy != "" {
		t.Fatalf("an expired Permit was used: %+v", stored)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	f := newFixture(t)
	valid := Config{Store: f.store, Contracts: f.contracts, Policies: f.policies, Clock: f.clock}
	cases := map[string]func(*Config){
		"store":     func(c *Config) { c.Store = nil },
		"contracts": func(c *Config) { c.Contracts = nil },
		"policies":  func(c *Config) { c.Policies = nil },
		"clock":     func(c *Config) { c.Clock = nil },
	}
	for name, mutate := range cases {
		cfg := valid
		mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: New() error = %v, want %v", name, err, ErrInvalidConfig)
		}
	}
	if _, err := (&Service{}).Release(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("an uninitialised Service error = %v, want %v", err, ErrInvalidConfig)
	}
}
