package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/claimreconcile"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// recallAttempts is a state store of Attempts held in memory.
type recallAttempts struct {
	record attemptstate.AttemptRecord
	err    error
}

func (f *recallAttempts) CreateAttempt(context.Context, attemptstate.AttemptRecord) error { return nil }

func (f *recallAttempts) LoadAttempt(context.Context, ids.AttemptID) (attemptstate.AttemptRecord, error) {
	if f.err != nil {
		return attemptstate.AttemptRecord{}, f.err
	}
	return f.record, nil
}

func (f *recallAttempts) CompareAndSwapAttempt(_ context.Context, id ids.AttemptID, expected uint64, next attemptstate.AttemptRecord) error {
	if f.err != nil {
		return f.err
	}
	if f.record.Revision != expected {
		return errors.New("stale revision")
	}
	f.record = next
	return nil
}

func (f *recallAttempts) ListAttemptsByTask(context.Context, ids.TaskID) ([]attemptstate.AttemptRecord, error) {
	return nil, nil
}

// recallClaims answers whether a ResultClaim exists.
type recallClaims struct {
	has bool
	err error
}

func (f recallClaims) LoadResultClaim(context.Context, ids.AttemptID) (resultclaim.Claim, bool, error) {
	return resultclaim.Claim{}, f.has, f.err
}

func (f recallClaims) ApplyResultClaim(context.Context, statestore.ResultClaimAcceptance) error {
	return nil
}

// recallRegistry remembers the Leases it took back.
type recallRegistry struct {
	taken []ids.LeaseID
	err   error
}

func (f *recallRegistry) Reclaim(_ context.Context, lease leasestate.LeaseRecord, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.taken = append(f.taken, lease.ID)
	return nil
}

func dispatchedAttempt(t *testing.T) attemptstate.AttemptRecord {
	t.Helper()
	attemptID, err := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	record, err := attemptstate.New(attemptID, taskID, contract.SHA256([]byte("task-ir")), 1, time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	record.Phase = attemptstate.AttemptPhaseDispatched
	return record
}

func expiredCloser(t *testing.T, attempts *recallAttempts, claims recallClaims) (recallCloser, *recallRegistry) {
	t.Helper()
	registry := &recallRegistry{}
	return recallCloser{
		attempts: attempts, claims: claims, reclaimer: registry,
		grace: RecallGrace, now: func() time.Time { return time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC) },
	}, registry
}

// An Attempt whose Lease expired without a claim is closed, and only then is the
// Lease taken back (#745).
func TestAnExpiredLeaseWithoutAClaimClosesTheAttempt(t *testing.T) {
	attempts := &recallAttempts{record: dispatchedAttempt(t)}
	closer, registry := expiredCloser(t, attempts, recallClaims{})
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err != nil {
		t.Fatal(err)
	}
	if attempts.record.Phase != attemptstate.AttemptPhaseClosed {
		t.Fatalf("phase = %s, want closed", attempts.record.Phase)
	}
	if attempts.record.CloseReason != attemptstate.CloseReasonLeaseExpired {
		t.Fatalf("close reason = %s, want lease_expired", attempts.record.CloseReason)
	}
	if len(registry.taken) != 1 {
		t.Fatalf("reclaim calls = %d, want 1", len(registry.taken))
	}
}

// A claim that arrived is not thrown away: the Lease is taken back, the Attempt
// is left to result acceptance.
func TestAnAttemptWithAClaimIsNotClosed(t *testing.T) {
	attempts := &recallAttempts{record: dispatchedAttempt(t)}
	closer, registry := expiredCloser(t, attempts, recallClaims{has: true})
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err != nil {
		t.Fatal(err)
	}
	if attempts.record.Phase == attemptstate.AttemptPhaseClosed {
		t.Fatal("the Attempt was closed even though a claim exists")
	}
	if len(registry.taken) != 0 {
		t.Fatalf("reclaim calls = %d, want none before the claim is decided", len(registry.taken))
	}
}

// During the grace period the Lease stays in the registry, so the next sweep
// looks again.
func TestTheLeaseStaysDuringTheGracePeriod(t *testing.T) {
	attempts := &recallAttempts{record: dispatchedAttempt(t)}
	closer, registry := expiredCloser(t, attempts, recallClaims{})
	// The Lease expired at 12:01; 12:05 is past the grace of 30s. Move the clock
	// back inside it.
	closer.now = func() time.Time { return time.Date(2026, 11, 6, 12, 1, 10, 0, time.UTC) }
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err != nil {
		t.Fatal(err)
	}
	if len(registry.taken) != 0 || attempts.record.Phase == attemptstate.AttemptPhaseClosed {
		t.Fatalf("grace was not respected: taken=%d phase=%s", len(registry.taken), attempts.record.Phase)
	}
}

// A failed close leaves the registry entry alone, so the next sweep tries again.
func TestAFailedCloseKeepsTheRegistryEntry(t *testing.T) {
	attempts := &recallAttempts{record: dispatchedAttempt(t), err: errors.New("store down")}
	closer, registry := expiredCloser(t, attempts, recallClaims{})
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err == nil {
		t.Fatal("a failed close was reported as success")
	}
	if len(registry.taken) != 0 {
		t.Fatal("the Lease was removed from the registry although the Attempt was not closed")
	}
}

// An Attempt that is already closed is not closed twice.
func TestAClosedAttemptIsNotClosedAgain(t *testing.T) {
	record := dispatchedAttempt(t)
	record.Phase = attemptstate.AttemptPhaseClosed
	record.CloseReason = attemptstate.CloseReasonResultAccepted
	attempts := &recallAttempts{record: record}
	closer, _ := expiredCloser(t, attempts, recallClaims{})
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err != nil {
		t.Fatal(err)
	}
	if attempts.record.CloseReason != attemptstate.CloseReasonResultAccepted {
		t.Fatalf("close reason = %s, want the existing result_accepted", attempts.record.CloseReason)
	}
}

// The decision itself is readable without storage.
func TestTheCloseDecisionFollowsTheClaimReconcileRules(t *testing.T) {
	lease := testLease(t)
	now := lease.ExpiresAt.Add(RecallGrace + time.Second)
	cases := []struct {
		name    string
		attempt attemptstate.AttemptRecord
		claim   bool
		want    claimreconcile.Decision
	}{
		{"closed", attemptstate.AttemptRecord{Phase: attemptstate.AttemptPhaseClosed}, false, claimreconcile.NoAction},
		{"claim", dispatchedAttempt(t), true, claimreconcile.NoAction},
		{"open, no claim", dispatchedAttempt(t), false, claimreconcile.CloseLeaseExpired},
	}
	for _, c := range cases {
		got, err := CloseDecision(c.attempt, c.claim, lease, now, RecallGrace)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: decision = %s, want %s", c.name, got, c.want)
		}
	}
}
