package claimqueue_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	memorystore "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/memory"
	sqlitestore "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/sqlite"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
)

// Exercise persisted retry scheduling through the public queue interface.
// Distinct claims must retain jitter after a prolonged outage reaches the cap.
func TestRetryScheduleKeepsJitterAtTheCap(t *testing.T) {
	const largest = time.Duration(1<<63 - 1)
	for _, tc := range []struct {
		name     string
		base     time.Duration
		maximum  time.Duration
		attempts uint32
		ceiling  time.Duration
	}{
		{"before-cap", time.Second, time.Minute, 3, 4 * time.Second},
		{"first-capped-retry", time.Second, time.Minute, 7, time.Minute},
		{"prolonged-outage", time.Second, time.Minute, 31, time.Minute},
		{"last-counter-value", time.Second, time.Minute, ^uint32(0), time.Minute},
		{"base-equals-cap", time.Second, time.Second, 2, time.Second},
		{"odd-cap-not-reached", 2, 5, 2, 4},
		{"duration-overflow-boundary", largest/2 + 1, largest, 2, largest},
		{"one-nanosecond", 1, 1, 31, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(map[time.Duration]bool)
			for n := 1; n <= 32; n++ {
				first := scheduledRetryDelay(t, tc.base, tc.maximum, tc.attempts, n)
				second := scheduledRetryDelay(t, tc.base, tc.maximum, tc.attempts, n)
				if first != second {
					t.Fatalf("claim %d: retry changed across queue reconstruction: %v != %v", n, first, second)
				}
				minimum := tc.ceiling / 2
				if minimum == 0 {
					minimum = 1
				}
				if first < minimum || first > tc.ceiling {
					t.Fatalf("claim %d: delay %v outside [%v, %v]", n, first, minimum, tc.ceiling)
				}
				seen[first] = true
			}
			if tc.ceiling > 1 && len(seen) < 2 {
				t.Fatalf("all 32 claims scheduled the same delay %v; jitter was lost", tc.ceiling)
			}
		})
	}
}

func TestCappedRetryScheduleSurvivesSQLiteReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "claims.db")
	store, err := sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clock := &fakeClock{now: baseTime}
	sender := &recordingSender{err: errors.New("control plane unavailable")}
	makeQueue := func() *claimqueue.Queue {
		t.Helper()
		q, err := claimqueue.New(claimqueue.Config{
			Store: store, Sender: sender, Clock: clock,
			BaseDelay: time.Second, MaxDelay: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	for n := 1; n <= 32; n++ {
		if err := store.Save(ctx, claimqueue.Entry{
			Claim: claim(t, n), QueuedAt: baseTime, NextAt: baseTime, Attempts: 6,
		}); err != nil {
			t.Fatal(err)
		}
	}
	report, err := makeQueue().DeliverDue(ctx, 32)
	if err != nil || report != (claimqueue.Report{Retried: 32}) {
		t.Fatalf("schedule = %+v, %v", report, err)
	}
	saved, err := store.Due(ctx, baseTime.Add(time.Minute), 32)
	if err != nil || len(saved) != 32 {
		t.Fatalf("saved = %d, %v", len(saved), err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlitestore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, before := range saved {
		after, found, err := store.Load(ctx, before.Claim.AttemptID())
		if err != nil || !found || !after.Claim.Equal(before.Claim) ||
			after.Attempts != 7 || !after.NextAt.Equal(before.NextAt) ||
			!after.QueuedAt.Equal(before.QueuedAt) || after.LastError != before.LastError {
			t.Fatalf("reopen changed retry: %+v, %v, %v", after, found, err)
		}
	}
	q := makeQueue()
	clock.Advance(30*time.Second - time.Nanosecond)
	if report, err := q.DeliverDue(ctx, 32); err != nil || report != (claimqueue.Report{}) {
		t.Fatalf("early retry = %+v, %v", report, err)
	}
	clock.now = baseTime.Add(time.Minute)
	sender.err = nil
	if report, err := q.DeliverDue(ctx, 32); err != nil || report != (claimqueue.Report{Delivered: 32}) {
		t.Fatalf("recovered delivery = %+v, %v", report, err)
	}
	if remaining, err := q.Len(ctx); err != nil || remaining != 0 {
		t.Fatalf("remaining = %d, %v", remaining, err)
	}
}

func scheduledRetryDelay(t *testing.T, base, maximum time.Duration, attempts uint32, n int) time.Duration {
	t.Helper()
	ctx := context.Background()
	store := memorystore.New()
	c := claim(t, n)
	if err := store.Save(ctx, claimqueue.Entry{
		Claim: c, QueuedAt: baseTime, NextAt: baseTime, Attempts: attempts - 1,
	}); err != nil {
		t.Fatal(err)
	}
	q, err := claimqueue.New(claimqueue.Config{
		Store: store, Clock: &fakeClock{now: baseTime}, BaseDelay: base, MaxDelay: maximum,
		Sender: &recordingSender{err: errors.New("control plane unavailable")},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := q.DeliverDue(ctx, 1)
	if err != nil || report != (claimqueue.Report{Retried: 1}) {
		t.Fatalf("retry = %+v, %v", report, err)
	}
	entry, found, err := store.Load(ctx, c.AttemptID())
	if err != nil || !found || !entry.Claim.Equal(c) || entry.Attempts != attempts || !entry.QueuedAt.Equal(baseTime) {
		t.Fatalf("retry did not preserve claim and metadata: %+v, %v, %v", entry, found, err)
	}
	return entry.NextAt.Sub(baseTime)
}
