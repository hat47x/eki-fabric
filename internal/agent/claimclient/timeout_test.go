package claimclient

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	queuesqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/sqlite"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type deadlineAPI struct {
	block     bool
	calls     int
	ctx       context.Context
	calledAt  time.Time
	onFailure func()
}

func (a *deadlineAPI) SubmitClaim(ctx context.Context, _ workerplane.Principal, _ resultclaim.Claim) (workerplane.ClaimAck, error) {
	a.calls++
	a.ctx, a.calledAt = ctx, time.Now()
	if a.block {
		<-ctx.Done()
		if a.onFailure != nil {
			a.onFailure()
		}
		return workerplane.ClaimAck{}, ctx.Err()
	}
	return workerplane.ClaimAck{Decision: "new", AttemptPhase: "closed"}, nil
}

func TestDefaultSendDeadlineAndCleanup(t *testing.T) {
	api := &deadlineAPI{}
	c, err := New(api, workerplane.Principal{WorkerIdentity: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if err := c.Send(context.Background(), claim(t, "worker-a")); err != nil {
		t.Fatal(err)
	}
	d, ok := api.ctx.Deadline()
	if !ok || d.Before(before.Add(DefaultSendTimeout)) || d.After(api.calledAt.Add(DefaultSendTimeout)) {
		t.Fatalf("transport deadline = %s, %v", d, ok)
	}
	if api.ctx.Err() != context.Canceled {
		t.Fatalf("completed call context not released: %v", api.ctx.Err())
	}
}

func TestSendKeepsTheEarlierCallerDeadline(t *testing.T) {
	api := &deadlineAPI{}
	c, err := NewWithTimeout(api, workerplane.Principal{WorkerIdentity: "worker-a"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), want)
	defer cancel()
	if err := c.Send(ctx, claim(t, "worker-a")); err != nil {
		t.Fatal(err)
	}
	got, ok := api.ctx.Deadline()
	if !ok || !got.Equal(want) {
		t.Fatalf("deadline = %s, want %s", got, want)
	}
	if ctx.Err() != nil {
		t.Fatalf("Send cancelled the caller context: %v", ctx.Err())
	}
}

func TestSendTimesOutACooperativeTransport(t *testing.T) {
	api := &deadlineAPI{block: true}
	timeout := 50 * time.Millisecond
	c, err := NewWithTimeout(api, workerplane.Principal{WorkerIdentity: "worker-a"}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(context.Background(), claim(t, "worker-a")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout = %v", err)
	}
	d, ok := api.ctx.Deadline()
	if !ok || d.Sub(api.calledAt) > timeout {
		t.Fatalf("transport did not get the configured deadline: %s", d)
	}
}

func TestCancelledSendDoesNotCallTheTransport(t *testing.T) {
	api := &deadlineAPI{}
	c, err := New(api, workerplane.Principal{WorkerIdentity: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Send(ctx, claim(t, "worker-a")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled send = %v", err)
	}
	if api.calls != 0 {
		t.Fatal("cancelled send called the transport")
	}
}

func TestNewWithTimeoutRejectsNonPositiveDurations(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, err := NewWithTimeout(&deadlineAPI{}, workerplane.Principal{WorkerIdentity: "worker-a"}, timeout); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("NewWithTimeout(%s) = %v", timeout, err)
		}
	}
}

type deadlineQueueClock struct{ now time.Time }

func (c *deadlineQueueClock) Now() time.Time { return c.now }

func TestTimedOutClaimSurvivesQueueReopenAndRetry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pending.db")
	open := func() *queuesqlite.Store {
		s, err := queuesqlite.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	api := &deadlineAPI{block: true}
	client, err := NewWithTimeout(api, workerplane.Principal{WorkerIdentity: "worker-a"}, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	clock := &deadlineQueueClock{now: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)}
	// Model a slow transport independently of the wall-clock timer, so the
	// persisted backoff must start after failure, not at the delivery pass.
	api.onFailure = func() { clock.now = clock.now.Add(2 * time.Minute) }
	queue := func(store *queuesqlite.Store) *claimqueue.Queue {
		q, err := claimqueue.New(claimqueue.Config{Store: store, Sender: client, Clock: clock, BaseDelay: time.Second, MaxDelay: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	s := open()
	c := claim(t, "worker-a")
	q := queue(s)
	if _, err := q.Enqueue(ctx, c); err != nil {
		t.Fatal(err)
	}
	report, err := q.DeliverDue(ctx, 1)
	if err != nil || report.Retried != 1 || report.Delivered != 0 {
		t.Fatalf("timeout report = %+v, %v", report, err)
	}
	pending, found, err := s.Load(ctx, c.AttemptID())
	if err != nil || !found || !pending.Claim.Equal(c) || pending.Attempts != 1 || pending.LastError != context.DeadlineExceeded.Error() || !pending.NextAt.After(clock.now) {
		t.Fatalf("timeout lost retry state: %+v, %v, %v", pending, found, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	q = queue(s)
	api.block = false
	clock.now = pending.NextAt.Add(-time.Nanosecond)
	report, err = q.DeliverDue(ctx, 1)
	if err != nil || report != (claimqueue.Report{}) || api.calls != 1 {
		t.Fatalf("early retry = %+v, %v, calls=%d", report, err, api.calls)
	}
	clock.now = pending.NextAt
	report, err = q.DeliverDue(ctx, 1)
	if err != nil || report.Delivered != 1 || api.calls != 2 {
		t.Fatalf("retry = %+v, %v, calls=%d", report, err, api.calls)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	if _, found, err := s.Load(ctx, c.AttemptID()); err != nil || found {
		t.Fatalf("successful receipt did not persist queue deletion: %v, %v", found, err)
	}
}
