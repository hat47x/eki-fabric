package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var baseTime = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func newGuard(t *testing.T, now time.Time, max int) *Guard {
	t.Helper()
	guard, err := New(&fakeClock{now: now}, max)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return guard
}

func nonce(seed byte) []byte {
	out := make([]byte, MinNonceBytes)
	for i := range out {
		out[i] = seed + byte(i)
	}
	return out
}

func TestClaimIsOneTime(t *testing.T) {
	guard := newGuard(t, baseTime, 10)
	until := baseTime.Add(time.Minute)

	claimed, err := guard.Claim(context.Background(), nonce(1), until)
	if err != nil || !claimed {
		t.Fatalf("first Claim() = %v, %v; want true, nil", claimed, err)
	}
	claimed, err = guard.Claim(context.Background(), nonce(1), until)
	if err != nil || claimed {
		t.Fatalf("second Claim() = %v, %v; want false, nil", claimed, err)
	}
	claimed, err = guard.Claim(context.Background(), nonce(2), until)
	if err != nil || !claimed {
		t.Fatalf("other nonce Claim() = %v, %v; want true, nil", claimed, err)
	}
}

func TestClaimExpiresAndCanBeReused(t *testing.T) {
	clock := &fakeClock{now: baseTime}
	guard, err := New(clock, 10)
	if err != nil {
		t.Fatal(err)
	}
	firstUntil := baseTime.Add(time.Minute)
	if claimed, err := guard.Claim(context.Background(), nonce(3), firstUntil); err != nil || !claimed {
		t.Fatalf("first Claim() = %v, %v", claimed, err)
	}
	clock.now = firstUntil
	if claimed, err := guard.Claim(context.Background(), nonce(3), baseTime.Add(2*time.Minute)); err != nil || !claimed {
		t.Fatalf("claim at expiry = %v, %v; want the expired nonce to be reusable", claimed, err)
	}
}

func TestGuardFailsClosedWhenFull(t *testing.T) {
	guard := newGuard(t, baseTime, 1)
	until := baseTime.Add(time.Minute)
	if claimed, err := guard.Claim(context.Background(), nonce(4), until); err != nil || !claimed {
		t.Fatalf("first Claim() = %v, %v", claimed, err)
	}
	claimed, err := guard.Claim(context.Background(), nonce(5), until)
	if !errors.Is(err, ErrGuardCapacity) {
		t.Fatalf("full guard error = %v, want %v", err, ErrGuardCapacity)
	}
	if claimed {
		t.Fatal("full guard claimed a nonce")
	}
}

func TestGuardRejectsInvalidClaims(t *testing.T) {
	guard := newGuard(t, baseTime, 10)
	cases := map[string]struct {
		ctx   context.Context
		nonce []byte
		until time.Time
		want  error
	}{
		"nil context": {ctx: nil, nonce: nonce(6), until: baseTime.Add(time.Minute), want: ErrInvalidNonce},
		"short nonce": {ctx: context.Background(), nonce: make([]byte, MinNonceBytes-1), until: baseTime.Add(time.Minute), want: ErrInvalidNonce},
		"long nonce":  {ctx: context.Background(), nonce: make([]byte, MaxNonceBytes+1), until: baseTime.Add(time.Minute), want: ErrInvalidNonce},
		"zero expiry": {ctx: context.Background(), nonce: nonce(7), want: ErrInvalidNonce},
		"expired":     {ctx: context.Background(), nonce: nonce(8), until: baseTime, want: ErrInvalidNonce},
	}
	for name, tc := range cases {
		if _, err := guard.Claim(tc.ctx, tc.nonce, tc.until); !errors.Is(err, tc.want) {
			t.Fatalf("%s: error = %v, want %v", name, err, tc.want)
		}
	}
}

func TestConcurrentClaimsHaveExactlyOneWinner(t *testing.T) {
	guard := newGuard(t, baseTime, 10)
	until := baseTime.Add(time.Minute)
	var wg sync.WaitGroup
	results := make(chan bool, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := guard.Claim(context.Background(), nonce(9), until)
			if err != nil {
				t.Errorf("Claim() error = %v", err)
				results <- false
				return
			}
			results <- claimed
		}()
	}
	wg.Wait()
	close(results)
	claimed := 0
	for ok := range results {
		if ok {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("concurrent claims = %d winners, want exactly 1", claimed)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	if _, err := New(nil, 10); !errors.Is(err, ErrInvalidGuard) {
		t.Fatalf("nil clock error = %v, want %v", err, ErrInvalidGuard)
	}
	if _, err := New(&fakeClock{now: baseTime}, 0); !errors.Is(err, ErrInvalidGuard) {
		t.Fatalf("zero capacity error = %v, want %v", err, ErrInvalidGuard)
	}
}

func TestFenceRefusesStaleEpoch(t *testing.T) {
	guard := newGuard(t, baseTime, 10)
	until := baseTime.Add(time.Minute)
	fence := func(epoch uint64) (bool, error) {
		return guard.Fence(context.Background(), "tsk_1", epoch, until)
	}
	if ok, err := fence(7); err != nil || !ok {
		t.Fatalf("first Fence() = %v, %v; want true, nil", ok, err)
	}
	if ok, err := fence(6); err != nil || ok {
		t.Fatalf("stale Fence() = %v, %v; want false, nil", ok, err)
	}
	// The same epoch is accepted again: a re-issued WorkOrder passes.
	if ok, err := fence(7); err != nil || !ok {
		t.Fatalf("same Fence() = %v, %v; want true, nil", ok, err)
	}
	if ok, err := fence(8); err != nil || !ok {
		t.Fatalf("higher Fence() = %v, %v; want true, nil", ok, err)
	}
	// Another Task keeps its own record.
	if ok, err := guard.Fence(context.Background(), "tsk_2", 1, until); err != nil || !ok {
		t.Fatalf("other task Fence() = %v, %v", ok, err)
	}
}

func TestFenceExpiresAndFailsClosedWhenFull(t *testing.T) {
	clock := &fakeClock{now: baseTime}
	guard, err := New(clock, 1)
	if err != nil {
		t.Fatal(err)
	}
	until := baseTime.Add(time.Minute)
	if ok, err := guard.Fence(context.Background(), "tsk_1", 1, until); err != nil || !ok {
		t.Fatalf("first Fence() = %v, %v", ok, err)
	}
	if ok, err := guard.Fence(context.Background(), "tsk_2", 1, until); ok || !errors.Is(err, ErrGuardCapacity) {
		t.Fatalf("full guard Fence() = %v, %v; want capacity error", ok, err)
	}
	// The expired record releases its capacity.
	clock.now = until
	if ok, err := guard.Fence(context.Background(), "tsk_2", 1, until.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("Fence() after expiry = %v, %v", ok, err)
	}
}

func TestFenceRejectsInvalidClaims(t *testing.T) {
	guard := newGuard(t, baseTime, 10)
	until := baseTime.Add(time.Minute)
	if _, err := guard.Fence(context.Background(), "", 1, until); !errors.Is(err, ErrInvalidFence) {
		t.Fatalf("empty task error = %v, want %v", err, ErrInvalidFence)
	}
	if _, err := guard.Fence(context.Background(), "tsk_1", 0, until); !errors.Is(err, ErrInvalidFence) {
		t.Fatalf("zero epoch error = %v, want %v", err, ErrInvalidFence)
	}
	if _, err := guard.Fence(context.Background(), "tsk_1", 1, baseTime); !errors.Is(err, ErrInvalidFence) {
		t.Fatalf("past expiry error = %v, want %v", err, ErrInvalidFence)
	}
}
