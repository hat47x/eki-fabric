package claimqueue_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	memorystore "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/memory"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var (
	baseTime = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	taskID   = ids.TaskID("tsk_00000000000000000000000000000001")
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

type recordingSender struct {
	mu    sync.Mutex
	sent  []resultclaim.Claim
	err   error
	calls int
}

func (s *recordingSender) Send(_ context.Context, claim resultclaim.Claim) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.sent = append(s.sent, claim)
	return s.err
}

func (s *recordingSender) Sent() []resultclaim.Claim {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]resultclaim.Claim(nil), s.sent...)
}

func newQueue(t *testing.T, clock *fakeClock, sender claimqueue.Sender) *claimqueue.Queue {
	t.Helper()
	queue, err := claimqueue.New(claimqueue.Config{
		Store: memorystore.New(), Sender: sender, Clock: clock,
		BaseDelay: time.Second, MaxDelay: time.Minute,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return queue
}

func claim(t *testing.T, n int) resultclaim.Claim {
	t.Helper()
	attemptID, err := ids.ParseAttemptID(fmt.Sprintf("atm_%032d", n))
	if err != nil {
		t.Fatal(err)
	}
	return claimForAttempt(t, attemptID, 10)
}

func claimForAttempt(t *testing.T, attemptID ids.AttemptID, output uint64) resultclaim.Claim {
	t.Helper()
	record, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: taskID, TaskIRDigest: contract.SHA256([]byte("taskir")),
		AttemptID: attemptID, WorkerIdentity: "worker-a",
		LeaseID:    ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch: 7, WorkOrderDecision: "wor-001",
		ExecutorDigest: contract.SHA256([]byte("executable")),
		Nonce:          []byte("0123456789abcdef"), Status: resultclaim.StatusReclaimed,
		ExitCode: 0, OutputBytes: output,
		StartedAt: baseTime.Add(-time.Minute), EndedAt: baseTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestEnqueueIsIdempotentForTheSameClaim(t *testing.T) {
	clock := &fakeClock{now: baseTime}
	queue := newQueue(t, clock, &recordingSender{})
	first := claim(t, 1)

	already, err := queue.Enqueue(context.Background(), first)
	if err != nil || already {
		t.Fatalf("Enqueue() = %v, %v; want false, nil", already, err)
	}
	already, err = queue.Enqueue(context.Background(), first)
	if err != nil || !already {
		t.Fatalf("Enqueue(replay) = %v, %v; want true, nil", already, err)
	}
	count, err := queue.Len(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("Len() = %d, %v; want 1", count, err)
	}
}

func TestEnqueueRejectsADifferentClaimForTheSameAttempt(t *testing.T) {
	clock := &fakeClock{now: baseTime}
	queue := newQueue(t, clock, &recordingSender{})
	first := claim(t, 1)
	second := claimForAttempt(t, first.AttemptID(), 11) // same Attempt, different observation

	if _, err := queue.Enqueue(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), second); !errors.Is(err, claimqueue.ErrConflict) {
		t.Fatalf("Enqueue(conflict) error = %v, want %v", err, claimqueue.ErrConflict)
	}
}

func TestDeliverDueRemovesADeliveredClaim(t *testing.T) {
	clock := &fakeClock{now: baseTime}
	sender := &recordingSender{}
	queue := newQueue(t, clock, sender)
	c := claim(t, 1)
	if _, err := queue.Enqueue(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	report, err := queue.DeliverDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DeliverDue() error = %v", err)
	}
	if report.Delivered != 1 || report.Retried != 0 {
		t.Fatalf("report = %+v, want one delivery", report)
	}
	if got := sender.Sent(); len(got) != 1 || !got[0].Equal(c) {
		t.Fatalf("sent = %+v, want the queued claim", got)
	}
	count, err := queue.Len(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("Len() = %d, %v; want 0", count, err)
	}
}

func TestDeliveryFailureReschedulesWithBackoff(t *testing.T) {
	clock := &fakeClock{now: baseTime}
	sender := &recordingSender{err: errors.New("control plane unreachable")}
	queue := newQueue(t, clock, sender)
	c := claim(t, 1)
	if _, err := queue.Enqueue(context.Background(), c); err != nil {
		t.Fatal(err)
	}

	report, err := queue.DeliverDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DeliverDue() error = %v", err)
	}
	if report.Retried != 1 || report.Delivered != 0 {
		t.Fatalf("report = %+v, want one retry", report)
	}
	// Not due immediately.
	report, err = queue.DeliverDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DeliverDue() error = %v", err)
	}
	if report.Retried != 0 || report.Delivered != 0 {
		t.Fatalf("report = %+v, want nothing due", report)
	}
	// After the backoff, the same claim is offered again.
	clock.Advance(time.Minute)
	report, err = queue.DeliverDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DeliverDue() error = %v", err)
	}
	if report.Retried != 1 {
		t.Fatalf("report = %+v, want the retry after the backoff", report)
	}
	if sender.calls != 2 {
		t.Fatalf("sender calls = %d, want 2", sender.calls)
	}
}

func TestDeliverDueHonoursTheLimit(t *testing.T) {
	clock := &fakeClock{now: baseTime}
	sender := &recordingSender{}
	queue := newQueue(t, clock, sender)
	for i := 1; i <= 3; i++ {
		if _, err := queue.Enqueue(context.Background(), claim(t, i)); err != nil {
			t.Fatal(err)
		}
	}
	report, err := queue.DeliverDue(context.Background(), 2)
	if err != nil {
		t.Fatalf("DeliverDue() error = %v", err)
	}
	if report.Delivered != 2 {
		t.Fatalf("report = %+v, want 2 deliveries", report)
	}
	count, err := queue.Len(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("Len() = %d, %v; want 1 left", count, err)
	}
}

func TestDependsOnItsConfiguration(t *testing.T) {
	clock := &fakeClock{now: baseTime}
	if _, err := claimqueue.New(claimqueue.Config{}); !errors.Is(err, claimqueue.ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want %v", err, claimqueue.ErrInvalidConfig)
	}
	queue := newQueue(t, clock, &recordingSender{})
	if _, err := queue.Enqueue(context.Background(), resultclaim.Claim{}); !errors.Is(err, resultclaim.ErrInvalidClaim) {
		t.Fatalf("invalid claim error = %v, want %v", err, resultclaim.ErrInvalidClaim)
	}
	if _, err := queue.DeliverDue(context.Background(), 0); !errors.Is(err, claimqueue.ErrInvalidConfig) {
		t.Fatalf("zero limit error = %v, want %v", err, claimqueue.ErrInvalidConfig)
	}
}
