package claimaccept

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
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

type fakeLeases struct{ record leasestate.LeaseRecord }

func (f fakeLeases) LoadLease(context.Context, ids.LeaseID) (leasestate.LeaseRecord, error) {
	return f.record, nil
}

type fakeClaims struct {
	claim     resultclaim.Claim
	found     bool
	appendErr error
	appends   int
}

func (f *fakeClaims) LoadResultClaim(context.Context, ids.AttemptID) (resultclaim.Claim, bool, error) {
	return f.claim, f.found, nil
}

func (f *fakeClaims) AppendResultClaim(_ context.Context, claim resultclaim.Claim) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.claim = claim
	f.found = true
	f.appends++
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
	record, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: testTask, TaskIRDigest: contract.SHA256([]byte("taskir")),
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
