package claimaccept

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var (
	baseTime    = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	testTask    = ids.TaskID("tsk_00000000000000000000000000000001")
	testAttempt = ids.AttemptID("atm_00000000000000000000000000000001")
	testLease   = ids.LeaseID("les_00000000000000000000000000000001")
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeAttempts struct {
	record  attemptstate.AttemptRecord
	casErr  error
	updates []attemptstate.AttemptRecord
}

func (f *fakeAttempts) LoadAttempt(context.Context, ids.AttemptID) (attemptstate.AttemptRecord, error) {
	return f.record, nil
}

func (f *fakeAttempts) CompareAndSwapAttempt(_ context.Context, _ ids.AttemptID, expected uint64, next attemptstate.AttemptRecord) error {
	if f.casErr != nil {
		return f.casErr
	}
	if expected != f.record.Revision {
		return errors.New("revision conflict")
	}
	f.record = next
	f.updates = append(f.updates, next)
	return nil
}

func (f *fakeAttempts) ListAttemptsByTask(_ context.Context, _ ids.TaskID) ([]attemptstate.AttemptRecord, error) {
	return []attemptstate.AttemptRecord{f.record}, nil
}

type fakeLeases struct{ record leasestate.LeaseRecord }

func (f fakeLeases) LoadLease(context.Context, ids.LeaseID) (leasestate.LeaseRecord, error) {
	return f.record, nil
}

type fakeClaims struct {
	attempts  *fakeAttempts
	claim     resultclaim.Claim
	found     bool
	appendErr error
	appends   int
}

func (f *fakeClaims) LoadResultClaim(context.Context, ids.AttemptID) (resultclaim.Claim, bool, error) {
	return f.claim, f.found, nil
}

func (f *fakeClaims) ApplyResultClaim(ctx context.Context, acceptance statestore.ResultClaimAcceptance) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	if acceptance.Record && f.found {
		if f.claim.Equal(acceptance.Claim) {
			return fmt.Errorf("%w: claim", statestore.ErrAlreadyExists)
		}
		return fmt.Errorf("%w: claim", statestore.ErrConflict)
	}
	if acceptance.ApplyTransition {
		if f.attempts == nil {
			return errors.New("fake has no attempt store")
		}
		if err := f.attempts.CompareAndSwapAttempt(ctx, acceptance.NextAttempt.ID, acceptance.ExpectedAttemptRevision, acceptance.NextAttempt); err != nil {
			return err
		}
	}
	if acceptance.Record {
		f.claim = acceptance.Claim
		f.found = true
		f.appends++
	}
	return nil
}

func attempt(t *testing.T, phase attemptstate.AttemptPhase) attemptstate.AttemptRecord {
	t.Helper()
	id := testAttempt
	record, err := attemptstate.New(id, testTask, contract.SHA256([]byte("taskir")), 1, baseTime.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	record.Phase = phase
	record.WorkerIdentity = "worker-a"
	record.LeaseID = testLease
	record.Revision = 5
	record.UpdatedAt = baseTime.Add(-time.Minute)
	return record
}

func lease(t *testing.T) leasestate.LeaseRecord {
	t.Helper()
	record, err := leasestate.New(leasestate.Config{
		ID: testLease, TaskID: testTask, TaskIRDigest: contract.SHA256([]byte("taskir")),
		AttemptID: testAttempt, WorkerIdentity: "worker-a",
		PlacementTrustDomain: "example.org", PlacementAuthorityPrincipal: "plc-1",
		PlacementDecisionID: "placement-001", PlacementPayloadDigest: contract.SHA256([]byte("placement")),
		PlacementExpiresAt: baseTime.Add(2 * time.Hour), PolicyRevision: "P1", PolicyEpoch: 5,
		FenceEpoch: 7, RenewalSequence: 1, IssuedAt: baseTime.Add(-time.Hour), ExpiresAt: baseTime.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func claim(t *testing.T, status resultclaim.Status, exit int) resultclaim.Claim {
	t.Helper()
	var outputDigest contract.Digest
	if status == resultclaim.StatusCompleted {
		outputDigest = contract.SHA256([]byte("output"))
	}
	record, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		OutputDigest: outputDigest,
		TaskID:       testTask, TaskIRDigest: contract.SHA256([]byte("taskir")),
		AttemptID: testAttempt, WorkerIdentity: "worker-a", LeaseID: testLease,
		FenceEpoch: 7, WorkOrderDecision: "wor-001",
		ExecutorDigest: contract.SHA256([]byte("executable")),
		Nonce:          []byte("0123456789abcdef"), Status: status, ExitCode: exit,
		OutputBytes: 10, StartedAt: baseTime.Add(-time.Minute), EndedAt: baseTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func newService(t *testing.T, attempts *fakeAttempts, claims *fakeClaims) *Service {
	t.Helper()
	claims.attempts = attempts
	service, err := New(Config{
		Attempts: attempts, Leases: fakeLeases{record: lease(t)}, Claims: claims,
		Clock: &fakeClock{now: baseTime},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestCompletedClaimMovesTheAttemptToVerifying(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseClaimed)}
	claims := &fakeClaims{}
	result, err := newService(t, attempts, claims).Accept(context.Background(), claim(t, resultclaim.StatusCompleted, 0))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if !result.VerificationRequired || result.Attempt.Phase != attemptstate.AttemptPhaseVerifying {
		t.Fatalf("result = %+v, want verification", result)
	}
	if claims.appends != 1 {
		t.Fatalf("appends = %d, want 1", claims.appends)
	}
}

func TestCompletedClaimAdvancesFromDispatched(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseDispatched)}
	result, err := newService(t, attempts, &fakeClaims{}).Accept(context.Background(), claim(t, resultclaim.StatusCompleted, 0))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Attempt.Phase != attemptstate.AttemptPhaseVerifying || result.Attempt.Revision != 7 {
		t.Fatalf("attempt = %+v, want verifying at revision 7", result.Attempt)
	}
	if len(attempts.updates) != 2 {
		t.Fatalf("updates = %d, want claimed then verifying", len(attempts.updates))
	}
}

func TestReclaimedClaimClosesTheAttempt(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseDispatched)}
	result, err := newService(t, attempts, &fakeClaims{}).Accept(context.Background(), claim(t, resultclaim.StatusReclaimed, 0))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Attempt.Phase != attemptstate.AttemptPhaseClosed || result.Attempt.CloseReason != attemptstate.CloseReasonReclaimed {
		t.Fatalf("attempt = %+v, want closed as reclaimed", result.Attempt)
	}
	if result.VerificationRequired {
		t.Fatal("a reclaimed claim must not require verification")
	}
}

func TestRunnerStuckClaimDoesNotClose(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseDispatched)}
	result, err := newService(t, attempts, &fakeClaims{}).Accept(context.Background(), claim(t, resultclaim.StatusRunnerStuck, 0))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Attempt.Phase == attemptstate.AttemptPhaseClosed {
		t.Fatal("runner_stuck closed the Attempt")
	}
	if len(attempts.updates) != 0 {
		t.Fatal("runner_stuck changed the Attempt")
	}
}

func TestExactReplayRetriesTheCloseAfterACrash(t *testing.T) {
	recorded := claim(t, resultclaim.StatusFailed, 1)
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseClaimed)}
	claims := &fakeClaims{claim: recorded, found: true}
	result, err := newService(t, attempts, claims).Accept(context.Background(), recorded)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Decision.Kind != resultclaim.KindDuplicate {
		t.Fatalf("decision = %+v, want duplicate", result.Decision)
	}
	if result.Attempt.Phase != attemptstate.AttemptPhaseClosed || result.Attempt.CloseReason != attemptstate.CloseReasonExecutionFailureConfirmed {
		t.Fatalf("attempt = %+v, want the close retried", result.Attempt)
	}
	if claims.appends != 0 {
		t.Fatal("the replay recorded the claim twice")
	}
}

func TestConflictingClaimIsRejected(t *testing.T) {
	recorded := claim(t, resultclaim.StatusFailed, 1)
	other := claim(t, resultclaim.StatusReclaimed, 0)
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseClaimed)}
	if _, err := newService(t, attempts, &fakeClaims{claim: recorded, found: true}).Accept(context.Background(), other); !errors.Is(err, resultclaim.ErrConflict) {
		t.Fatalf("error = %v, want %v", err, resultclaim.ErrConflict)
	}
}

func TestStaleClaimIsRecordedWithoutChangingTheAttempt(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseDispatched)}
	claims := &fakeClaims{}
	result, err := newServiceWithFence(t, attempts, claims, 8).Accept(context.Background(), claim(t, resultclaim.StatusReclaimed, 0))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Decision.Kind != resultclaim.KindStale || !claims.found {
		t.Fatalf("result = %+v, want a recorded stale claim", result)
	}
	if len(attempts.updates) != 0 {
		t.Fatal("a stale claim changed the Attempt")
	}
}

func TestClaimFenceAheadOfLeaseIsRejected(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseDispatched)}
	service, err := New(Config{
		Attempts: attempts, Leases: fakeLeases{record: leaseWithFence(t, 3)},
		Claims: &fakeClaims{}, Clock: &fakeClock{now: baseTime},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Accept(context.Background(), claim(t, resultclaim.StatusFailed, 1)); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrBindingMismatch)
	}
}

func TestClaimForAnotherWorkerOrLeaseIsRejected(t *testing.T) {
	other := attempt(t, attemptstate.AttemptPhaseDispatched)
	other.WorkerIdentity = "worker-b"
	service, err := New(Config{
		Attempts: &fakeAttempts{record: other}, Leases: fakeLeases{record: lease(t)},
		Claims: &fakeClaims{}, Clock: &fakeClock{now: baseTime},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Accept(context.Background(), claim(t, resultclaim.StatusReclaimed, 0)); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("worker mismatch error = %v, want %v", err, ErrBindingMismatch)
	}
}

func TestLateClaimIsRecordedWithoutReopening(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseClosed)}
	claims := &fakeClaims{}
	result, err := newService(t, attempts, claims).Accept(context.Background(), claim(t, resultclaim.StatusCompleted, 0))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Decision.Kind != resultclaim.KindLate || !claims.found {
		t.Fatalf("result = %+v, want a recorded late claim", result)
	}
	if len(attempts.updates) != 0 || result.VerificationRequired {
		t.Fatal("a late claim changed the Attempt")
	}
}

func TestCompletedClaimInAnEarlyPhaseIsRejectedBeforeRecording(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseAssigned)}
	claims := &fakeClaims{}
	if _, err := newService(t, attempts, claims).Accept(context.Background(), claim(t, resultclaim.StatusCompleted, 0)); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("error = %v, want %v", err, ErrWrongPhase)
	}
	if claims.found {
		t.Fatal("a claim that cannot be applied was recorded")
	}
}

func TestAttemptCasConflictIsReturned(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseDispatched), casErr: errors.New("conflict")}
	if _, err := newService(t, attempts, &fakeClaims{}).Accept(context.Background(), claim(t, resultclaim.StatusReclaimed, 0)); err == nil {
		t.Fatal("Accept() hid a CAS conflict")
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	base := Config{Attempts: &fakeAttempts{}, Leases: fakeLeases{}, Claims: &fakeClaims{}, Clock: &fakeClock{now: baseTime}}
	cases := map[string]func(*Config){
		"attempts": func(c *Config) { c.Attempts = nil },
		"leases":   func(c *Config) { c.Leases = nil },
		"claims":   func(c *Config) { c.Claims = nil },
		"clock":    func(c *Config) { c.Clock = nil },
	}
	for name, mutate := range cases {
		cfg := base
		mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: New() error = %v, want %v", name, err, ErrInvalidConfig)
		}
	}
}

// newServiceWithFence builds a service whose Lease reports the given fence.
func newServiceWithFence(t *testing.T, attempts *fakeAttempts, claims *fakeClaims, fence uint64) *Service {
	t.Helper()
	claims.attempts = attempts
	record := leaseWithFence(t, fence)
	service, err := New(Config{
		Attempts: attempts, Leases: fakeLeases{record: record}, Claims: claims,
		Clock: &fakeClock{now: baseTime},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func leaseWithFence(t *testing.T, fence uint64) leasestate.LeaseRecord {
	t.Helper()
	record := lease(t)
	record.FenceEpoch = fence
	return record
}

type fakeTasks struct {
	record  taskstate.TaskRecord
	updates []taskstate.TaskRecord
}

func (f *fakeTasks) CreateTask(context.Context, taskstate.TaskRecord) error { return nil }

func (f *fakeTasks) LoadTask(_ context.Context, id ids.TaskID) (taskstate.TaskRecord, error) {
	return f.record, nil
}

func (f *fakeTasks) CompareAndSwapTask(_ context.Context, _ ids.TaskID, expected uint64, next taskstate.TaskRecord) error {
	if expected != f.record.Revision {
		return errors.New("revision conflict")
	}
	f.record = next
	f.updates = append(f.updates, next)
	return nil
}

func (f *fakeTasks) ListTasksByPhase(context.Context, taskstate.TaskPhase, int) ([]taskstate.TaskRecord, error) {
	return nil, nil
}

// A completed claim moves the Task into verification beside its Attempt, so
// "running" and "verifying" stay distinguishable from the outside (ADR-0032
// §2.4, #267).
func TestCompletedClaimMovesTheTaskToVerifying(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseClaimed)}
	tasks := &fakeTasks{record: taskstate.TaskRecord{
		ID: testTask, TaskIRDigest: contract.SHA256([]byte("taskir")),
		Phase: taskstate.TaskPhaseActive, Revision: 4, CreatedAt: baseTime.Add(-time.Hour), UpdatedAt: baseTime.Add(-time.Minute),
	}}
	claims := &fakeClaims{attempts: attempts}
	service, err := New(Config{
		Attempts: attempts, Leases: fakeLeases{record: lease(t)}, Claims: claims,
		Clock: &fakeClock{now: baseTime}, Tasks: tasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Accept(context.Background(), claim(t, resultclaim.StatusCompleted, 0))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Attempt.Phase != attemptstate.AttemptPhaseVerifying {
		t.Fatalf("attempt = %+v, want verifying", result.Attempt)
	}
	if len(tasks.updates) != 1 || tasks.record.Phase != taskstate.TaskPhaseVerifying {
		t.Fatalf("task updates = %d, phase = %s, want one write to verifying", len(tasks.updates), tasks.record.Phase)
	}
	if tasks.updates[0].Revision != 5 {
		t.Fatalf("task revision = %d, want 5", tasks.updates[0].Revision)
	}
}

// A retryable close (a Worker-reported failure, a deadline, a budget stop)
// returns the Task to ready when no open Attempt remains, so the dispatch
// loop can create the next Attempt (ADR-0032 §2.5, #381).
func TestAFailedClaimReturnsTheTaskToReady(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseClaimed)}
	tasks := &fakeTasks{record: taskstate.TaskRecord{
		ID: testTask, TaskIRDigest: contract.SHA256([]byte("taskir")),
		Phase: taskstate.TaskPhaseActive, Revision: 4, CreatedAt: baseTime.Add(-time.Hour), UpdatedAt: baseTime.Add(-time.Minute),
	}}
	service, err := New(Config{
		Attempts: attempts, Leases: fakeLeases{record: lease(t)}, Claims: &fakeClaims{attempts: attempts},
		Clock: &fakeClock{now: baseTime}, Tasks: tasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Accept(context.Background(), claim(t, resultclaim.StatusFailed, 1))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Attempt.Phase != attemptstate.AttemptPhaseClosed {
		t.Fatalf("attempt = %+v, want closed", result.Attempt)
	}
	if len(tasks.updates) != 1 || tasks.record.Phase != taskstate.TaskPhaseReady {
		t.Fatalf("task updates = %d, phase = %s, want one write to ready", len(tasks.updates), tasks.record.Phase)
	}
}

// A cancellation reported by the Worker converges through the Task's own
// cancellation path (#263), not through a restart here.
func TestACancelledClaimDoesNotRestartTheTask(t *testing.T) {
	attempts := &fakeAttempts{record: attempt(t, attemptstate.AttemptPhaseClaimed)}
	tasks := &fakeTasks{record: taskstate.TaskRecord{
		ID: testTask, TaskIRDigest: contract.SHA256([]byte("taskir")),
		Phase: taskstate.TaskPhaseActive, Revision: 4, CreatedAt: baseTime.Add(-time.Hour), UpdatedAt: baseTime.Add(-time.Minute),
	}}
	service, err := New(Config{
		Attempts: attempts, Leases: fakeLeases{record: lease(t)}, Claims: &fakeClaims{attempts: attempts},
		Clock: &fakeClock{now: baseTime}, Tasks: tasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Accept(context.Background(), claim(t, resultclaim.StatusCancelled, 0)); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if len(tasks.updates) != 0 {
		t.Fatalf("task updates = %d, want none", len(tasks.updates))
	}
}
