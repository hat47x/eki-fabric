package claimclient_test

import (
	"context"
	"testing"
	"time"

	memoryqueue "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/memory"
	"github.com/hat47x/eki-fabric/internal/agent/claimclient"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	controlplane "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type acceptor struct {
	calls int
	claim resultclaim.Claim
	err   error
}

func (a *acceptor) Accept(_ context.Context, claim resultclaim.Claim) (claimaccept.Result, error) {
	a.calls++
	a.claim = claim
	if a.err != nil {
		return claimaccept.Result{}, a.err
	}
	return claimaccept.Result{
		Decision:             resultclaim.Decision{Kind: resultclaim.KindNew},
		Attempt:              attemptstate.AttemptRecord{Phase: attemptstate.AttemptPhaseVerifying},
		VerificationRequired: true,
	}, nil
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func claim(t *testing.T) resultclaim.Claim {
	t.Helper()
	record, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID:         ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest:   contract.SHA256([]byte("taskir")),
		AttemptID:      ids.AttemptID("atm_00000000000000000000000000000001"),
		WorkerIdentity: "worker-a", LeaseID: ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch: 7, WorkOrderDecision: "wor-001",
		ExecutorDigest: contract.SHA256([]byte("executable")),
		Nonce:          []byte("0123456789abcdef"), Status: resultclaim.StatusCompleted,
		ExitCode: 0, OutputBytes: 10, OutputDigest: contract.SHA256([]byte("output")),
		StartedAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 10, 21, 0, 0, 1, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func newQueue(t *testing.T, clock *fakeClock, acc *acceptor) *claimqueue.Queue {
	t.Helper()
	plane, err := claimplane.New(acc)
	if err != nil {
		t.Fatal(err)
	}
	server, err := controlplane.New(controlplane.Config{Claims: plane})
	if err != nil {
		t.Fatal(err)
	}
	client, err := claimclient.New(server, workerplane.Principal{WorkerIdentity: "worker-a", TrustDomain: "example.org"})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := claimqueue.New(claimqueue.Config{
		Store: memoryqueue.New(), Sender: client, Clock: clock,
		BaseDelay: time.Second, MaxDelay: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func TestQueuedClaimReachesTheClaimPlane(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 21, 0, 1, 0, 0, time.UTC)}
	acc := &acceptor{}
	queue := newQueue(t, clock, acc)
	c := claim(t)
	if _, err := queue.Enqueue(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	report, err := queue.DeliverDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DeliverDue() error = %v", err)
	}
	if report.Delivered != 1 || report.Retried != 0 {
		t.Fatalf("report = %+v", report)
	}
	if acc.calls != 1 || !acc.claim.Equal(c) {
		t.Fatalf("acceptor = %+v", acc)
	}
	count, err := queue.Len(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("Len() = %d, %v; want 0", count, err)
	}
}

func TestFailedDeliveryStaysQueuedAndIsRetried(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 21, 0, 1, 0, 0, time.UTC)}
	acc := &acceptor{err: claimaccept.ErrBindingMismatch}
	queue := newQueue(t, clock, acc)
	if _, err := queue.Enqueue(context.Background(), claim(t)); err != nil {
		t.Fatal(err)
	}
	report, err := queue.DeliverDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DeliverDue() error = %v", err)
	}
	if report.Retried != 1 || report.Delivered != 0 {
		t.Fatalf("report = %+v, want one retry", report)
	}
	// The same claim is not due immediately; after the backoff it is delivered.
	acc.err = nil
	clock.now = clock.now.Add(time.Minute)
	report, err = queue.DeliverDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DeliverDue() error = %v", err)
	}
	if report.Delivered != 1 {
		t.Fatalf("report = %+v, want the retry delivered", report)
	}
	if acc.calls != 2 {
		t.Fatalf("acceptor calls = %d, want 2", acc.calls)
	}
}
