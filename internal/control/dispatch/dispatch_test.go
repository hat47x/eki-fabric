package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/placementissue"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type stubIssue struct {
	placements int
	reserved   int
	emitted    int
	orders     int
	failAfter  int
	events     *[]string
}

func (s *stubIssue) issuePlacement(ctx context.Context, req placementissue.IssueRequest) ([]byte, error) {
	s.placements++
	return []byte("signed-placement"), nil
}

func (s *stubIssue) reserve(ctx context.Context, signed []byte, lifetime time.Duration) (ReservedLease, error) {
	s.reserved++
	if s.failAfter > 0 && s.reserved > s.failAfter {
		return ReservedLease{}, errors.New("reserve failed")
	}
	id, err := ids.NewLeaseID()
	if err != nil {
		return ReservedLease{}, err
	}
	return ReservedLease{LeaseID: id, FenceEpoch: 1}, nil
}

func (s *stubIssue) emitLease(ctx context.Context, id ids.LeaseID) ([]byte, error) {
	s.emitted++
	if s.events != nil {
		*s.events = append(*s.events, "emit")
	}
	return []byte("signed-lease"), nil
}

func (s *stubIssue) issueWorkOrder(ctx context.Context, id ids.LeaseID, worker string) ([]byte, error) {
	s.orders++
	if s.events != nil {
		*s.events = append(*s.events, "order")
	}
	return []byte("signed-order"), nil
}

func mustPolicy(t *testing.T) policystate.State {
	t.Helper()
	policy, err := policystate.NewActive(1, "policy-1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func mustNewAttemptID(t *testing.T) string {
	t.Helper()
	id, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func seedTask(t *testing.T, store *memory.Store, n int, phase taskstate.TaskPhase) ids.TaskID {
	t.Helper()
	taskID, err := ids.NewTaskID()
	if err != nil {
		t.Fatal(err)
	}
	task, err := taskstate.NewTask(taskID, contract.SHA256([]byte{byte(n)}), time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	// Creation is admitted-only (#365); the phase under test is a separate
	// write, the way dispatch preparation would move the Task.
	if phase != taskstate.TaskPhaseAdmitted {
		next := task
		next.Phase = phase
		next.Revision++
		next.UpdatedAt = task.CreatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, next); err != nil {
			t.Fatalf("move Task to %s: %v", phase, err)
		}
		task = next
	}
	return task.ID
}

func seedWorker(t *testing.T, store *memory.Store, n int) string {
	t.Helper()
	svc, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: &fakeClock{now: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := workerenrollment.NewAdmin("admin:test")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := svc.IssueEnrollmentCredential(context.Background(), admin, workerenrollment.IssueRequest{TrustDomain: "example.org"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := svc.ConsumeEnrollmentCredential(context.Background(), issued.Secret.Reveal(), "example.org")
	if err != nil {
		t.Fatal(err)
	}
	return worker.Identity.String()
}

func newLoop(t *testing.T, store *memory.Store, stub *stubIssue, mutate func(*Config)) *Loop {
	t.Helper()
	cfg := Config{
		Tasks: store, Attempts: store, Workers: store, Policies: store,
		IssuePlacement: stub.issuePlacement, Reserve: stub.reserve,
		EmitLease: stub.emitLease, IssueWorkOrder: stub.issueWorkOrder,
		LeaseLifetime: 3 * time.Minute, Clock: &fakeClock{now: time.Now().UTC()},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	loop, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return loop
}

// An admitted Task has been accepted but not yet found placeable; the tick is
// what makes it ready (ADR-0032 §2.2). Without a usable Worker the Task is
// promoted and then deferred for placement, which is exactly the intended
// split between admission and placement.
func TestTickPromotesAnAdmittedTaskToReady(t *testing.T) {
	store := memory.New()
	if err := store.CompareAndSwapPolicy(context.Background(), 0, mustPolicy(t)); err != nil {
		t.Fatal(err)
	}
	taskID := seedTask(t, store, 1, taskstate.TaskPhaseAdmitted)
	loop := newLoop(t, store, &stubIssue{}, nil)

	result, err := loop.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if result.Promoted != 1 {
		t.Fatalf("Promoted = %d, want 1", result.Promoted)
	}
	task, err := store.LoadTask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Phase != taskstate.TaskPhaseReady {
		t.Fatalf("phase = %s, want ready", task.Phase)
	}
}

// A Task whose principal asked to cancel it must not be promoted: starting it
// would defeat the cancellation.
func TestTickDoesNotPromoteATaskWhoseCancellationWasRequested(t *testing.T) {
	store := memory.New()
	if err := store.CompareAndSwapPolicy(context.Background(), 0, mustPolicy(t)); err != nil {
		t.Fatal(err)
	}
	taskID := seedTask(t, store, 1, taskstate.TaskPhaseAdmitted)
	task, err := store.LoadTask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	requested := task
	requested.CancellationRequested = true
	requested.Revision++
	requested.UpdatedAt = task.UpdatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), taskID, task.Revision, requested); err != nil {
		t.Fatal(err)
	}
	loop := newLoop(t, store, &stubIssue{}, nil)

	result, err := loop.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if result.Promoted != 0 {
		t.Fatalf("Promoted = %d, want 0", result.Promoted)
	}
	after, err := store.LoadTask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Phase != taskstate.TaskPhaseAdmitted {
		t.Fatalf("phase = %s, want it to stay admitted", after.Phase)
	}
}

// One tick promotes at most the configured number of Tasks, so a large backlog
// is worked through over several ticks instead of in one unbounded pass.
func TestTickPromotesAtMostTheConfiguredLimit(t *testing.T) {
	store := memory.New()
	if err := store.CompareAndSwapPolicy(context.Background(), 0, mustPolicy(t)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		seedTask(t, store, i, taskstate.TaskPhaseAdmitted)
	}
	loop := newLoop(t, store, &stubIssue{}, func(c *Config) { c.Limit = 2 })

	result, err := loop.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if result.Promoted != 2 {
		t.Fatalf("Promoted = %d, want 2 (the configured limit)", result.Promoted)
	}
	admitted, err := store.ListTasksByPhase(context.Background(), taskstate.TaskPhaseAdmitted, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(admitted) != 1 {
		t.Fatalf("admitted = %d, want 1 left for the next tick", len(admitted))
	}
}

// The Task becomes active with its first Attempt (ADR-0032 §2.3), not later.
func TestTickMarksTheTaskActiveWhenTheAttemptIsCreated(t *testing.T) {
	store := memory.New()
	if err := store.CompareAndSwapPolicy(context.Background(), 0, mustPolicy(t)); err != nil {
		t.Fatal(err)
	}
	seedWorker(t, store, 1)
	taskID := seedTask(t, store, 1, taskstate.TaskPhaseReady)
	loop := newLoop(t, store, &stubIssue{}, nil)

	if _, err := loop.Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	task, err := store.LoadTask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Phase != taskstate.TaskPhaseActive {
		t.Fatalf("phase = %s, want active", task.Phase)
	}
}

func TestTickMovesAReadyTaskToADispatchedAttempt(t *testing.T) {
	store := memory.New()
	if err := store.CompareAndSwapPolicy(context.Background(), 0, mustPolicy(t)); err != nil {
		t.Fatal(err)
	}
	worker := seedWorker(t, store, 1)
	taskID := seedTask(t, store, 1, taskstate.TaskPhaseReady)
	stub := &stubIssue{}
	loop := newLoop(t, store, stub, nil)

	result, err := loop.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if result.Dispatched != 1 || len(result.Deferred) != 0 {
		t.Fatalf("Tick() = %+v, want one dispatched Task", result)
	}
	if stub.placements != 1 || stub.reserved != 1 || stub.emitted != 1 || stub.orders != 1 {
		t.Fatalf("issuing calls = placement %d reserve %d emit %d order %d, want one each", stub.placements, stub.reserved, stub.emitted, stub.orders)
	}
	attempts, err := store.ListAttemptsByTask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("ListAttemptsByTask() = %d attempts, want 1", len(attempts))
	}
	got := attempts[0]
	if got.Phase != attemptstate.AttemptPhaseDispatched || got.WorkerIdentity != worker || got.LeaseID == "" {
		t.Fatalf("Attempt = phase %q worker %q lease %q, want a DISPATCHED Attempt bound to %s", got.Phase, got.WorkerIdentity, got.LeaseID, worker)
	}
}

func TestTickDefersWhenNoWorkerIsUsable(t *testing.T) {
	store := memory.New()
	if err := store.CompareAndSwapPolicy(context.Background(), 0, mustPolicy(t)); err != nil {
		t.Fatal(err)
	}
	taskID := seedTask(t, store, 2, taskstate.TaskPhaseReady)
	stub := &stubIssue{}
	loop := newLoop(t, store, stub, nil)

	result, err := loop.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if result.Dispatched != 0 || len(result.Deferred) != 1 {
		t.Fatalf("Tick() = %+v, want one deferred Task", result)
	}
	if result.Deferred[0].TaskID != taskID {
		t.Fatalf("deferred Task = %s, want %s", result.Deferred[0].TaskID, taskID)
	}
	if stub.placements != 0 || stub.reserved != 0 {
		t.Fatal("a tick without a usable Worker must not reach the issuing chain")
	}
	// The Task keeps its phase and gains no Attempt.
	tasks, err := store.ListTasksByPhase(context.Background(), taskstate.TaskPhaseReady, 8)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasksByPhase() = %d tasks (err %v), want the same READY Task", len(tasks), err)
	}
	attempts, err := store.ListAttemptsByTask(context.Background(), taskID)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("ListAttemptsByTask() = %d attempts (err %v), want none", len(attempts), err)
	}
}

func TestTickDefersWhenPolicyIsNotActive(t *testing.T) {
	store := memory.New()
	seedTask(t, store, 3, taskstate.TaskPhaseReady)
	stub := &stubIssue{}
	loop := newLoop(t, store, stub, nil)

	result, err := loop.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if result.Dispatched != 0 || len(result.Deferred) != 1 {
		t.Fatalf("Tick() = %+v, want one deferred Task", result)
	}
	if stub.placements != 0 {
		t.Fatal("a tick without an active policy must not reach PLC-1")
	}
}

func TestTickKeepsTheWorkerOfAnExistingPlacingAttempt(t *testing.T) {
	store := memory.New()
	if err := store.CompareAndSwapPolicy(context.Background(), 0, mustPolicy(t)); err != nil {
		t.Fatal(err)
	}
	worker := seedWorker(t, store, 5)
	taskID := seedTask(t, store, 4, taskstate.TaskPhaseReady)
	// A previous tick created the Attempt and bound the Worker, then died.
	attempt, err := attemptstate.New(ids.AttemptID(mustNewAttemptID(t)), taskID, contract.SHA256([]byte{byte(4)}), 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	placing := attempt
	placing.Phase, placing.WorkerIdentity = attemptstate.AttemptPhasePlacing, worker
	placing.Revision, placing.UpdatedAt = attempt.Revision+1, time.Now().UTC()
	if err := store.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, placing); err != nil {
		t.Fatal(err)
	}

	stub := &stubIssue{}
	loop := newLoop(t, store, stub, nil)
	result, err := loop.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if result.Dispatched != 1 {
		t.Fatalf("Tick() = %+v, want one dispatched Task", result)
	}
	attempts, err := store.ListAttemptsByTask(context.Background(), taskID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("ListAttemptsByTask() = %d attempts (err %v), want the same Attempt", len(attempts), err)
	}
	if attempts[0].WorkerIdentity != worker || attempts[0].Phase != attemptstate.AttemptPhaseDispatched {
		t.Fatalf("Attempt = phase %q worker %q, want the same Worker after assignment", attempts[0].Phase, attempts[0].WorkerIdentity)
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	store := memory.New()
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(zero config) error = %v, want ErrInvalidConfig", err)
	}
	loop := newLoop(t, store, &stubIssue{}, nil)
	if loop == nil {
		t.Fatal("New() = nil")
	}
}

type orderedAttempts struct {
	*memory.Store
	events *[]string
}

func (o orderedAttempts) CompareAndSwapAttempt(ctx context.Context, id ids.AttemptID, expectedRevision uint64, next attemptstate.AttemptRecord) error {
	if next.Phase == attemptstate.AttemptPhaseDispatched {
		*o.events = append(*o.events, "dispatched")
	}
	return o.Store.CompareAndSwapAttempt(ctx, id, expectedRevision, next)
}

func TestTickMarksDispatchedAfterIssuingTheWorkOrder(t *testing.T) {
	store := memory.New()
	if err := store.CompareAndSwapPolicy(context.Background(), 0, mustPolicy(t)); err != nil {
		t.Fatal(err)
	}
	seedWorker(t, store, 1)
	seedTask(t, store, 1, taskstate.TaskPhaseReady)
	events := []string{}
	stub := &stubIssue{events: &events}
	loop := newLoop(t, store, stub, func(c *Config) {
		c.Attempts = orderedAttempts{Store: store, events: &events}
	})
	if _, err := loop.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"emit", "order", "dispatched"}
	if len(events) != len(want) {
		t.Fatalf("issuing order = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("issuing order = %v, want %v", events, want)
		}
	}
}

// Only READY Tasks are dispatched. An ACTIVE Task already has an Attempt and a
// VERIFYING Task is being judged, so neither may get another Attempt (#381).
func TestTickLeavesActiveAndVerifyingTasksAlone(t *testing.T) {
	store := memory.New()
	if err := store.CompareAndSwapPolicy(context.Background(), 0, mustPolicy(t)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// move walks a Task along the allowed path to the phase under test.
	move := func(id ids.TaskID, phase taskstate.TaskPhase) {
		t.Helper()
		current, err := store.LoadTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		next := current
		next.Phase = phase
		next.Revision++
		next.UpdatedAt = current.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(ctx, id, current.Revision, next); err != nil {
			t.Fatalf("move Task to %s: %v", phase, err)
		}
	}
	active := seedTask(t, store, 0, taskstate.TaskPhaseReady)
	move(active, taskstate.TaskPhaseActive)
	verifying := seedTask(t, store, 1, taskstate.TaskPhaseReady)
	move(verifying, taskstate.TaskPhaseActive)
	move(verifying, taskstate.TaskPhaseVerifying)

	before := map[ids.TaskID]uint64{}
	for _, id := range []ids.TaskID{active, verifying} {
		task, err := store.LoadTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		before[id] = task.Revision
	}
	loop := newLoop(t, store, &stubIssue{}, nil)
	result, err := loop.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if result.Dispatched != 0 {
		t.Fatalf("Dispatched = %d, want 0", result.Dispatched)
	}
	for _, id := range []ids.TaskID{active, verifying} {
		task, err := store.LoadTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Revision != before[id] {
			t.Fatalf("task %s revision = %d, want %d (untouched)", id, task.Revision, before[id])
		}
	}
}
