package leaserecall

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

type countingLister struct {
	calls atomic.Int32
	err   error
}

func (c *countingLister) OpenLeases(context.Context) ([]leasestate.LeaseRecord, error) {
	c.calls.Add(1)
	return nil, c.err
}

func TestRunSweepsUntilTheContextEnds(t *testing.T) {
	lister := &countingLister{}
	svc, err := New(Config{Lister: lister, Reclaimer: &fakeReclaimer{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		Run(ctx, svc, 5*time.Millisecond, nil)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for lister.calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done
	if got := lister.calls.Load(); got < 3 {
		t.Fatalf("sweeps = %d, want at least 3", got)
	}
}

func TestRunKeepsGoingAfterAFailedSweep(t *testing.T) {
	lister := &countingLister{err: errors.New("boom")}
	svc, err := New(Config{Lister: lister, Reclaimer: &fakeReclaimer{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		Run(ctx, svc, 5*time.Millisecond, nil)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for lister.calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done
	if got := lister.calls.Load(); got < 3 {
		t.Fatalf("sweeps = %d: a failed sweep stopped the loop", got)
	}
}

func TestRunDoesNothingWithoutAService(t *testing.T) {
	Run(t.Context(), nil, time.Second, nil)
	Run(t.Context(), &Service{}, 0, nil)
}
