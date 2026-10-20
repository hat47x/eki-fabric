package recovery

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var baseTime = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func testPolicy() Policy {
	return Policy{
		Revision:                  7,
		BaseBackoff:               time.Second,
		MaxBackoff:                30 * time.Second,
		Window:                    time.Second,
		MaxReleasesPerWindow:      50,
		MaxTransferBytesPerWindow: 2 << 30,
		MaxComputeMillisPerWindow: 60_000,
		PermitTTL:                 time.Minute,
	}
}

func mustTaskID(t *testing.T, n int) ids.TaskID {
	t.Helper()
	id, err := ids.ParseTaskID(fmt.Sprintf("tsk_%032x", n))
	if err != nil {
		t.Fatalf("ParseTaskID() error = %v", err)
	}
	return id
}

func mustAttemptID(t *testing.T, n int) ids.AttemptID {
	t.Helper()
	id, err := ids.ParseAttemptID(fmt.Sprintf("atm_%032x", n))
	if err != nil {
		t.Fatalf("ParseAttemptID() error = %v", err)
	}
	return id
}

func testContract(t *testing.T, task int, retryable bool, maxAttempts uint32) Contract {
	t.Helper()
	return Contract{
		TaskID:       mustTaskID(t, task),
		TaskIRDigest: contract.SHA256([]byte(fmt.Sprintf("task-ir-%d", task))),
		Retryable:    retryable,
		MaxAttempts:  maxAttempts,
	}
}

func testInterruption(t *testing.T, task, attempt, maxAttempts int) Interruption {
	t.Helper()
	return Interruption{
		AttemptID:              mustAttemptID(t, task),
		AttemptNumber:          uint32(attempt),
		Contract:               testContract(t, task, true, uint32(maxAttempts)),
		Priority:               PriorityNormal,
		EstimatedTransferBytes: 64 << 20,
		EstimatedComputeMillis: 100,
	}
}

func pendingFor(t *testing.T, task int, priority Priority, deadline time.Time) Pending {
	t.Helper()
	return Pending{
		TaskID:                 mustTaskID(t, task),
		TaskIRDigest:           contract.SHA256([]byte(fmt.Sprintf("task-ir-%d", task))),
		PreviousAttemptID:      mustAttemptID(t, task),
		PreviousAttemptNumber:  1,
		NextAttemptNumber:      2,
		MaxAttempts:            4,
		Priority:               priority,
		Deadline:               deadline,
		NotBefore:              baseTime.Add(-time.Minute),
		EstimatedTransferBytes: 1024,
		EstimatedComputeMillis: 10,
	}
}

// TestMassReclaimStaysInsideTransferBudget reproduces the mass-reclaim Spike
// numbers on the production boundary: 1,000 Tasks with 64 MiB inputs cannot
// release more than 32 in a 2 GiB window, even though the count budget allows
// 50.
func TestMassReclaimStaysInsideTransferBudget(t *testing.T) {
	policy := testPolicy()
	interrupted := make([]Interruption, 0, 1000)
	for i := 0; i < 1000; i++ {
		interrupted = append(interrupted, testInterruption(t, i, 1, 4))
	}

	intake, err := Classify(baseTime, policy, interrupted)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if len(intake.Held) != 0 {
		t.Fatalf("held = %d, want 0", len(intake.Held))
	}
	if len(intake.Pending) != 1000 {
		t.Fatalf("pending = %d, want 1000", len(intake.Pending))
	}

	releaseAt := baseTime.Add(31 * time.Second) // every jittered start has passed
	decision, err := Release(releaseAt, policy, intake.Pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 32 {
		t.Fatalf("released = %d, want 32 (2 GiB / 64 MiB)", len(decision.Permits))
	}
	if decision.Budget.TransferBytes != policy.MaxTransferBytesPerWindow {
		t.Fatalf("transfer = %d, want %d", decision.Budget.TransferBytes, policy.MaxTransferBytesPerWindow)
	}
	if decision.Budget.Released != 32 {
		t.Fatalf("count = %d, want 32", decision.Budget.Released)
	}
	if len(decision.Deferred) != 968 {
		t.Fatalf("deferred = %d, want 968", len(decision.Deferred))
	}
	for _, permit := range decision.Permits {
		if permit.NotBefore().After(releaseAt) {
			t.Fatalf("permit NotBefore %s is after the release time %s", permit.NotBefore(), releaseAt)
		}
	}
}

func TestCountBudgetIsIndependent(t *testing.T) {
	policy := testPolicy()
	policy.MaxTransferBytesPerWindow = 1 << 30
	policy.MaxReleasesPerWindow = 10

	interrupted := make([]Interruption, 0, 100)
	for i := 0; i < 100; i++ {
		item := testInterruption(t, i, 1, 4)
		item.EstimatedTransferBytes = 1024
		interrupted = append(interrupted, item)
	}
	intake, err := Classify(baseTime, policy, interrupted)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	decision, err := Release(baseTime.Add(31*time.Second), policy, intake.Pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 10 || decision.Budget.Released != 10 {
		t.Fatalf("released = %d/%d, want 10/10", len(decision.Permits), decision.Budget.Released)
	}
	if decision.Budget.TransferBytes != 10*1024 {
		t.Fatalf("transfer = %d, want %d", decision.Budget.TransferBytes, 10*1024)
	}
	if got := decision.Deferred[0].Reason; got != DeferCountBudget {
		t.Fatalf("first defer reason = %q, want %q", got, DeferCountBudget)
	}
}

func TestComputeBudgetIsIndependent(t *testing.T) {
	policy := testPolicy()
	policy.MaxComputeMillisPerWindow = 12_000

	interrupted := []Interruption{
		testInterruption(t, 1, 1, 4),
		testInterruption(t, 2, 1, 4),
		testInterruption(t, 3, 1, 4),
	}
	for i := range interrupted {
		interrupted[i].EstimatedTransferBytes = 1024
		interrupted[i].EstimatedComputeMillis = 5_000
	}
	intake, err := Classify(baseTime, policy, interrupted)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	decision, err := Release(baseTime.Add(31*time.Second), policy, intake.Pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 2 {
		t.Fatalf("released = %d, want 2", len(decision.Permits))
	}
	if decision.Budget.ComputeMillis != 10_000 {
		t.Fatalf("compute = %d, want 10000", decision.Budget.ComputeMillis)
	}
	if got := decision.Deferred[0].Reason; got != DeferComputeBudget {
		t.Fatalf("defer reason = %q, want %q", got, DeferComputeBudget)
	}
}

func TestSingleItemLargerThanWindowIsDeferred(t *testing.T) {
	policy := testPolicy()
	item := testInterruption(t, 1, 1, 4)
	item.EstimatedTransferBytes = policy.MaxTransferBytesPerWindow + 1

	intake, err := Classify(baseTime, policy, []Interruption{item})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	decision, err := Release(baseTime.Add(31*time.Second), policy, intake.Pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 0 {
		t.Fatalf("released = %d, want 0", len(decision.Permits))
	}
	if len(decision.Deferred) != 1 || decision.Deferred[0].Reason != DeferSingleItemTooLarge {
		t.Fatalf("deferred = %+v, want one single-item deferral", decision.Deferred)
	}
	if decision.Budget.Released != 0 || decision.Budget.TransferBytes != 0 {
		t.Fatalf("budget = %+v, want an unchanged budget", decision.Budget)
	}
}

func TestClassifyHoldsTasksThatMustNotRetry(t *testing.T) {
	policy := testPolicy()

	nonRetryable := testInterruption(t, 1, 1, 4)
	nonRetryable.Contract.Retryable = false

	exhausted := testInterruption(t, 2, 4, 4)

	pastDeadline := testInterruption(t, 3, 1, 4)
	pastDeadline.Contract.Deadline = baseTime.Add(-time.Second)

	unreachable := testInterruption(t, 4, 1, 4)
	probe, err := Classify(baseTime, policy, []Interruption{unreachable})
	if err != nil {
		t.Fatalf("Classify(probe) error = %v", err)
	}
	unreachable.Contract.Deadline = probe.Pending[0].NotBefore

	intake, err := Classify(baseTime, policy, []Interruption{nonRetryable, exhausted, pastDeadline, unreachable})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if len(intake.Pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(intake.Pending))
	}
	want := []HoldReason{HoldNotRetryable, HoldRetryBudgetExhausted, HoldDeadlinePassed, HoldDeadlineUnreachable}
	if len(intake.Held) != len(want) {
		t.Fatalf("held = %d, want %d", len(intake.Held), len(want))
	}
	for i, reason := range want {
		if intake.Held[i].Reason != reason {
			t.Fatalf("held[%d] = %q, want %q", i, intake.Held[i].Reason, reason)
		}
	}
}

func TestClassifyCollapsesDuplicateDeliveryButRejectsConflict(t *testing.T) {
	policy := testPolicy()
	item := testInterruption(t, 1, 1, 4)

	intake, err := Classify(baseTime, policy, []Interruption{item, item})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if len(intake.Pending) != 1 || intake.Duplicates != 1 {
		t.Fatalf("pending = %d, duplicates = %d, want 1/1", len(intake.Pending), intake.Duplicates)
	}

	conflict := item
	conflict.AttemptNumber = 2
	conflict.Contract.MaxAttempts = 5
	if _, err := Classify(baseTime, policy, []Interruption{item, conflict}); !errors.Is(err, ErrDuplicateConflict) {
		t.Fatalf("Classify(conflict) error = %v, want %v", err, ErrDuplicateConflict)
	}
}

func TestClassifyJitterIsDeterministicPerTask(t *testing.T) {
	policy := testPolicy()
	first, err := Classify(baseTime, policy, []Interruption{testInterruption(t, 1, 2, 4)})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	second, err := Classify(baseTime.Add(time.Hour), policy, []Interruption{testInterruption(t, 1, 2, 4)})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	firstDelay := first.Pending[0].NotBefore.Sub(baseTime)
	secondDelay := second.Pending[0].NotBefore.Sub(baseTime.Add(time.Hour))
	if firstDelay != secondDelay {
		t.Fatalf("jitter = %s and %s, want the same delay", firstDelay, secondDelay)
	}
	if firstDelay < 0 || firstDelay >= policy.MaxBackoff {
		t.Fatalf("jitter = %s, want [0, %s)", firstDelay, policy.MaxBackoff)
	}
}

func TestReleaseOrdersByPriorityButKeepsBudgets(t *testing.T) {
	policy := testPolicy()
	policy.MaxReleasesPerWindow = 1

	pending := []Pending{
		pendingFor(t, 3, PriorityBestEffort, time.Time{}),
		pendingFor(t, 2, PriorityUrgent, time.Time{}),
		pendingFor(t, 1, PriorityNormal, time.Time{}),
	}
	decision, err := Release(baseTime, policy, pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 1 {
		t.Fatalf("released = %d, want 1", len(decision.Permits))
	}
	if got := decision.Permits[0].TaskID(); got != mustTaskID(t, 2) {
		t.Fatalf("released task = %s, want the urgent task", got)
	}
	if decision.Budget.Released != 1 {
		t.Fatalf("count = %d, want 1: priority must not raise the budget", decision.Budget.Released)
	}
}

func TestReleaseOrdersByDeadlineThenNotBeforeThenTaskID(t *testing.T) {
	pending := []Pending{
		pendingFor(t, 3, PriorityNormal, time.Time{}),
		pendingFor(t, 2, PriorityNormal, time.Time{}),
		pendingFor(t, 1, PriorityNormal, baseTime.Add(2*time.Hour)),
		pendingFor(t, 4, PriorityNormal, baseTime.Add(time.Hour)),
	}
	ordered := append([]Pending(nil), pending...)
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if lessPending(ordered[j], ordered[i]) {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}
	want := []ids.TaskID{mustTaskID(t, 4), mustTaskID(t, 1), mustTaskID(t, 2), mustTaskID(t, 3)}
	for i, id := range want {
		if ordered[i].TaskID != id {
			t.Fatalf("order[%d] = %s, want %s", i, ordered[i].TaskID, id)
		}
	}
}

func TestReleaseDefersUntilNotBefore(t *testing.T) {
	policy := testPolicy()
	item := testInterruption(t, 1, 1, 4)
	intake, err := Classify(baseTime, policy, []Interruption{item})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	decision, err := Release(baseTime, policy, intake.Pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 0 {
		t.Fatalf("released = %d, want 0 before the jittered start", len(decision.Permits))
	}
	if len(decision.Deferred) != 1 || decision.Deferred[0].Reason != DeferNotYetEligible {
		t.Fatalf("deferred = %+v, want one not-yet-eligible deferral", decision.Deferred)
	}
}

func TestReleaseAdvancesWindowAndResetsBudget(t *testing.T) {
	policy := testPolicy()
	policy.MaxReleasesPerWindow = 1

	first, err := Release(baseTime, policy, []Pending{pendingFor(t, 1, PriorityNormal, time.Time{})}, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(first.Permits) != 1 {
		t.Fatalf("released = %d, want 1", len(first.Permits))
	}

	blocked, err := Release(baseTime.Add(500*time.Millisecond), policy, []Pending{pendingFor(t, 2, PriorityNormal, time.Time{})}, first.Budget, 2)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(blocked.Permits) != 0 || blocked.Deferred[0].Reason != DeferCountBudget {
		t.Fatalf("released = %d, want 0 in the same window", len(blocked.Permits))
	}

	next, err := Release(baseTime.Add(time.Second), policy, []Pending{pendingFor(t, 2, PriorityNormal, time.Time{})}, blocked.Budget, 3)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(next.Permits) != 1 {
		t.Fatalf("released = %d, want 1 in the next window", len(next.Permits))
	}
	if !next.Budget.WindowStart.Equal(baseTime.Add(time.Second)) {
		t.Fatalf("window start = %s, want %s", next.Budget.WindowStart, baseTime.Add(time.Second))
	}
}

func TestReleaseRejectsClockRegressionAndCorruptBudget(t *testing.T) {
	policy := testPolicy()
	pending := []Pending{pendingFor(t, 1, PriorityNormal, time.Time{})}

	budget := Budget{WindowStart: baseTime}
	if _, err := Release(baseTime.Add(-time.Second), policy, pending, budget, 1); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("clock regression error = %v, want %v", err, ErrClockRegression)
	}

	corrupt := Budget{WindowStart: baseTime, Released: policy.MaxReleasesPerWindow + 1}
	if _, err := Release(baseTime, policy, pending, corrupt, 1); !errors.Is(err, ErrInvalidPending) {
		t.Fatalf("corrupt budget error = %v, want %v", err, ErrInvalidPending)
	}
}

func TestReleaseDoesNotOverflowNearTheBudgetLimit(t *testing.T) {
	policy := testPolicy()
	policy.MaxTransferBytesPerWindow = 100
	policy.MaxReleasesPerWindow = 10

	a := pendingFor(t, 1, PriorityNormal, time.Time{})
	a.EstimatedTransferBytes = 60
	b := pendingFor(t, 2, PriorityNormal, time.Time{})
	b.EstimatedTransferBytes = 60

	decision, err := Release(baseTime, policy, []Pending{b, a}, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 1 {
		t.Fatalf("released = %d, want 1", len(decision.Permits))
	}
	if decision.Budget.TransferBytes != 60 {
		t.Fatalf("transfer = %d, want 60", decision.Budget.TransferBytes)
	}
	if decision.Deferred[0].Reason != DeferTransferBudget {
		t.Fatalf("defer reason = %q, want %q", decision.Deferred[0].Reason, DeferTransferBudget)
	}
}

func TestReleaseExpiresPendingWhoseDeadlinePassed(t *testing.T) {
	policy := testPolicy()
	item := pendingFor(t, 1, PriorityNormal, baseTime.Add(time.Second))

	decision, err := Release(baseTime.Add(2*time.Second), policy, []Pending{item}, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 0 || len(decision.Deferred) != 0 {
		t.Fatalf("permits/deferred = %d/%d, want 0/0", len(decision.Permits), len(decision.Deferred))
	}
	if len(decision.Expired) != 1 || decision.Expired[0].TaskID != item.TaskID {
		t.Fatalf("expired = %+v, want the pending Task", decision.Expired)
	}
}

func TestReleaseCollapsesDuplicatePendingAndRejectsConflict(t *testing.T) {
	policy := testPolicy()
	item := pendingFor(t, 1, PriorityNormal, time.Time{})

	decision, err := Release(baseTime, policy, []Pending{item, item}, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 1 {
		t.Fatalf("released = %d, want 1", len(decision.Permits))
	}

	conflict := item
	conflict.EstimatedComputeMillis++
	if _, err := Release(baseTime, policy, []Pending{item, conflict}, Budget{}, 1); !errors.Is(err, ErrDuplicateConflict) {
		t.Fatalf("conflict error = %v, want %v", err, ErrDuplicateConflict)
	}
}

func TestPermitIsBoundToSubjectAndTime(t *testing.T) {
	policy := testPolicy()
	item := testInterruption(t, 1, 1, 4)
	intake, err := Classify(baseTime, policy, []Interruption{item})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	releaseAt := baseTime.Add(time.Minute)
	decision, err := Release(releaseAt, policy, intake.Pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 1 {
		t.Fatalf("released = %d, want 1", len(decision.Permits))
	}
	permit := decision.Permits[0]
	subject := Subject{
		TaskID:            item.Contract.TaskID,
		TaskIRDigest:      item.Contract.TaskIRDigest,
		PreviousAttemptID: item.AttemptID,
		PolicyRevision:    policy.Revision,
	}
	if err := permit.Validate(releaseAt, subject); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	wrongTask := subject
	wrongTask.TaskID = mustTaskID(t, 99)
	if err := permit.Validate(releaseAt, wrongTask); !errors.Is(err, ErrPermitSubject) {
		t.Fatalf("wrong task error = %v, want %v", err, ErrPermitSubject)
	}

	wrongPolicy := subject
	wrongPolicy.PolicyRevision++
	if err := permit.Validate(releaseAt, wrongPolicy); !errors.Is(err, ErrPermitPolicyRevision) {
		t.Fatalf("wrong policy error = %v, want %v", err, ErrPermitPolicyRevision)
	}

	if err := permit.Validate(permit.NotBefore().Add(-time.Nanosecond), subject); !errors.Is(err, ErrPermitNotYetValid) {
		t.Fatalf("early error = %v, want %v", err, ErrPermitNotYetValid)
	}
	if err := permit.Validate(permit.ExpiresAt(), subject); !errors.Is(err, ErrPermitExpired) {
		t.Fatalf("expiry error = %v, want %v", err, ErrPermitExpired)
	}

	var zero Permit
	if err := zero.Validate(releaseAt, subject); !errors.Is(err, ErrInvalidPermit) {
		t.Fatalf("zero permit error = %v, want %v", err, ErrInvalidPermit)
	}
}

func TestPermitExpiryIsCappedByTaskDeadline(t *testing.T) {
	policy := testPolicy()
	policy.PermitTTL = 2 * time.Hour
	item := testInterruption(t, 1, 1, 4)
	item.Contract.Deadline = baseTime.Add(time.Hour)

	intake, err := Classify(baseTime, policy, []Interruption{item})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	decision, err := Release(baseTime.Add(time.Minute), policy, intake.Pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(decision.Permits) != 1 {
		t.Fatalf("released = %d, want 1", len(decision.Permits))
	}
	if got := decision.Permits[0].ExpiresAt(); !got.Equal(item.Contract.Deadline) {
		t.Fatalf("expiry = %s, want the Task deadline %s", got, item.Contract.Deadline)
	}
}

func TestPermitDigestIsStableAndBound(t *testing.T) {
	policy := testPolicy()
	item := testInterruption(t, 1, 1, 4)
	intake, err := Classify(baseTime, policy, []Interruption{item})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	first, err := Release(baseTime.Add(time.Minute), policy, intake.Pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	second, err := Release(baseTime.Add(time.Minute), policy, intake.Pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if first.Permits[0].Digest() != second.Permits[0].Digest() {
		t.Fatal("permit digest is not stable for identical inputs")
	}

	other, err := Release(baseTime.Add(time.Minute), policy, intake.Pending, Budget{}, 2)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if first.Permits[0].Digest() == other.Permits[0].Digest() {
		t.Fatal("permit digest does not change when the release sequence changes")
	}
}

func TestPolicyAndInputValidation(t *testing.T) {
	policy := testPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	cases := map[string]func(*Policy){
		"revision":   func(p *Policy) { p.Revision = 0 },
		"base":       func(p *Policy) { p.BaseBackoff = 0 },
		"max":        func(p *Policy) { p.MaxBackoff = p.BaseBackoff - 1 },
		"window":     func(p *Policy) { p.Window = 0 },
		"count":      func(p *Policy) { p.MaxReleasesPerWindow = 0 },
		"transfer":   func(p *Policy) { p.MaxTransferBytesPerWindow = 0 },
		"compute":    func(p *Policy) { p.MaxComputeMillisPerWindow = 0 },
		"permit ttl": func(p *Policy) { p.PermitTTL = 0 },
	}
	for name, mutate := range cases {
		broken := policy
		mutate(&broken)
		if err := broken.Validate(); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("%s: Validate() error = %v, want %v", name, err, ErrInvalidPolicy)
		}
	}

	if _, err := Classify(time.Time{}, policy, nil); !errors.Is(err, ErrInvalidInterruption) {
		t.Fatalf("zero now error = %v, want %v", err, ErrInvalidInterruption)
	}
	if _, err := Release(baseTime, policy, []Pending{{}}, Budget{}, 1); !errors.Is(err, ErrInvalidPending) {
		t.Fatalf("invalid pending error = %v, want %v", err, ErrInvalidPending)
	}
	if _, err := Release(baseTime, policy, nil, Budget{}, 0); !errors.Is(err, ErrInvalidPending) {
		t.Fatalf("zero sequence error = %v, want %v", err, ErrInvalidPending)
	}
}
