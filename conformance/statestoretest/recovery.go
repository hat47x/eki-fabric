package statestoretest

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// RecoveryTestStore is a store that can hold Tasks and Attempts and the recovery
// state that connects them.
type RecoveryTestStore interface {
	statestore.TaskStore
	statestore.AttemptStore
	statestore.RecoveryStore
}

type RecoveryFactory func(t testing.TB) RecoveryTestStore

type DurableRecoveryFactory struct {
	Open func(t testing.TB, location string) (store RecoveryTestStore, close func() error)
}

var recoveryNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func recoveryPolicy(revision uint64) recovery.Policy {
	return recovery.Policy{
		Revision: revision, BaseBackoff: time.Second, MaxBackoff: 30 * time.Second,
		Window: time.Minute, MaxReleasesPerWindow: 5,
		MaxTransferBytesPerWindow: 1 << 30, MaxComputeMillisPerWindow: 600_000, PermitTTL: 10 * time.Minute,
	}
}

// interrupted is a Task with one Attempt that was closed for the given reason.
type interrupted struct {
	task    taskstate.TaskRecord
	attempt attemptstate.AttemptRecord
}

func newInterrupted(t testing.TB, store RecoveryTestStore, seed string, reason attemptstate.CloseReason) interrupted {
	t.Helper()
	ctx := context.Background()
	task := newTask(t, seed)
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	ready := nextRevision(task, taskstate.TaskPhaseReady)
	if err := store.CompareAndSwapTask(ctx, task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}
	attempt := newAttemptFor(t, store, ready, 1)
	return interrupted{task: ready, attempt: closeAttempt(t, store, attempt, reason)}
}

func newAttemptFor(t testing.TB, store RecoveryTestStore, task taskstate.TaskRecord, number uint64) attemptstate.AttemptRecord {
	t.Helper()
	id, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptstate.New(id, task.ID, task.TaskIRDigest, number, task.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	return attempt
}

func closeAttempt(t testing.TB, store RecoveryTestStore, attempt attemptstate.AttemptRecord, reason attemptstate.CloseReason) attemptstate.AttemptRecord {
	t.Helper()
	closed := attempt
	closed.Phase, closed.CloseReason = attemptstate.AttemptPhaseClosed, reason
	closed.Revision++
	closed.UpdatedAt = attempt.UpdatedAt.Add(time.Second)
	if err := store.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, closed); err != nil {
		t.Fatalf("close Attempt as %s: %v", reason, err)
	}
	return closed
}

// pendingFor classifies the interruption the way the recovery boundary does.
func (i interrupted) pending(t testing.TB, policy recovery.Policy) recovery.Pending {
	t.Helper()
	intake, err := recovery.Classify(recoveryNow, policy, []recovery.Interruption{{
		AttemptID: i.attempt.ID, AttemptNumber: uint32(i.attempt.Number),
		Contract: recovery.Contract{TaskID: i.task.ID, TaskIRDigest: i.task.TaskIRDigest, Retryable: true, MaxAttempts: 3},
		Priority: recovery.PriorityNormal, EstimatedTransferBytes: 1 << 20, EstimatedComputeMillis: 100,
	}})
	if err != nil || len(intake.Pending) != 1 {
		t.Fatalf("Classify() = %+v, %v", intake, err)
	}
	return intake.Pending[0]
}

func enqueue(t testing.TB, store RecoveryTestStore, p recovery.Pending) {
	t.Helper()
	if _, replayed, err := store.EnqueueRecovery(context.Background(), p); err != nil || replayed {
		t.Fatalf("EnqueueRecovery() = replayed %v, %v", replayed, err)
	}
}

// pass builds one release pass from what the store holds, the way a caller would.
func pass(t testing.TB, store RecoveryTestStore, policy recovery.Policy, now time.Time) statestore.RecoveryRelease {
	t.Helper()
	ctx := context.Background()
	pending, err := store.LoadRecoveryPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var before recovery.Budget
	var last uint64
	if w, ok, err := store.LoadRecoveryWindow(ctx); err != nil {
		t.Fatal(err)
	} else if ok {
		last = w.LastSequence
		if w.PolicyRevision == policy.Revision {
			before = w.Budget
		}
	}
	decision, err := recovery.Release(now, policy, pending, before, last+1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	return statestore.RecoveryRelease{Policy: policy, Now: now, ExpectedBudget: before, Decision: decision}
}

func commit(t testing.TB, store RecoveryTestStore, r statestore.RecoveryRelease) {
	t.Helper()
	if replayed, err := store.CommitRecoveryRelease(context.Background(), r); err != nil || replayed {
		t.Fatalf("CommitRecoveryRelease() = replayed %v, %v", replayed, err)
	}
}

// admission is the new Attempt a Permit admits.
func admissionFor(t testing.TB, i interrupted, permit recovery.Permit, policy recovery.Policy, now time.Time) statestore.RecoveryAdmission {
	t.Helper()
	id, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	next, err := attemptstate.New(id, i.task.ID, i.task.TaskIRDigest, uint64(permit.NextAttemptNumber()), now)
	if err != nil {
		t.Fatal(err)
	}
	return statestore.RecoveryAdmission{PreviousAttemptID: i.attempt.ID, NewAttempt: next, PolicyRevision: policy.Revision, Now: now}
}

func RunRecoveryStoreContract(t *testing.T, newStore RecoveryFactory) {
	t.Helper()
	ctx := context.Background()
	policy := recoveryPolicy(7)

	t.Run("enqueue-keeps-the-retry-and-lists-it", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "enqueue", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)
		got, err := store.LoadRecoveryPending(ctx)
		if err != nil || len(got) != 1 || got[0] != p {
			t.Fatalf("LoadRecoveryPending() = %+v, %v, want %+v", got, err, p)
		}
	})

	t.Run("a-repeat-returns-the-stored-retry-and-cannot-move-its-start-time", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "enqueue-repeat", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)

		later := p
		later.NotBefore = p.NotBefore.Add(time.Hour)
		stored, replayed, err := store.EnqueueRecovery(ctx, later)
		if err != nil || !replayed || stored != p {
			t.Fatalf("repeat = %+v, replayed %v, %v, want the stored retry", stored, replayed, err)
		}
		got, _ := store.LoadRecoveryPending(ctx)
		if len(got) != 1 || got[0].NotBefore != p.NotBefore {
			t.Fatalf("a repeat moved NotBefore: %+v", got)
		}
	})

	t.Run("enqueue-refuses-what-is-not-an-interrupted-attempt", func(t *testing.T) {
		store := newStore(t)
		open := newInterrupted(t, store, "enqueue-open", attemptstate.CloseReasonReclaimed)
		// An Attempt that is still open is not an interruption.
		stillOpen := newAttemptFor(t, store, open.task, 2)
		stillOpenPending := open.pending(t, policy)
		stillOpenPending.PreviousAttemptID, stillOpenPending.PreviousAttemptNumber, stillOpenPending.NextAttemptNumber = stillOpen.ID, 2, 3
		if _, _, err := store.EnqueueRecovery(ctx, stillOpenPending); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("open Attempt error = %v, want %v", err, statestore.ErrConflict)
		}

		for _, reason := range []attemptstate.CloseReason{
			attemptstate.CloseReasonResultAccepted, attemptstate.CloseReasonResultRejected,
			attemptstate.CloseReasonCancelled, attemptstate.CloseReasonSuperseded,
			attemptstate.CloseReasonUnknownExternalEffect, attemptstate.CloseReasonPolicyRevoked,
		} {
			other := newInterrupted(t, store, "enqueue-"+string(reason), reason)
			if _, _, err := store.EnqueueRecovery(ctx, other.pending(t, policy)); !errors.Is(err, statestore.ErrConflict) {
				t.Fatalf("Attempt closed as %s error = %v, want %v", reason, err, statestore.ErrConflict)
			}
		}
		if got, _ := store.LoadRecoveryPending(ctx); len(got) != 0 {
			t.Fatalf("a refused retry was kept: %+v", got)
		}
	})

	t.Run("enqueue-refuses-a-retry-that-does-not-match-the-stored-attempt", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "enqueue-mismatch", attemptstate.CloseReasonReclaimed)
		cases := map[string]func(*recovery.Pending){
			"another TaskIR": func(p *recovery.Pending) { p.TaskIRDigest = newTask(t, "another").TaskIRDigest },
			"another number": func(p *recovery.Pending) {
				p.PreviousAttemptNumber, p.NextAttemptNumber = 2, 3
			},
		}
		for name, mutate := range cases {
			p := i.pending(t, policy)
			mutate(&p)
			if _, _, err := store.EnqueueRecovery(ctx, p); !errors.Is(err, statestore.ErrConflict) {
				t.Fatalf("%s: error = %v, want %v", name, err, statestore.ErrConflict)
			}
		}
		unknown := i.pending(t, policy)
		unknown.PreviousAttemptID = mustAttemptID(t)
		if _, _, err := store.EnqueueRecovery(ctx, unknown); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("unknown Attempt error = %v, want %v", err, statestore.ErrNotFound)
		}
		malformed := i.pending(t, policy)
		malformed.NotBefore = time.Time{}
		if _, _, err := store.EnqueueRecovery(ctx, malformed); !errors.Is(err, statestore.ErrInvalidRecovery) {
			t.Fatalf("malformed retry error = %v, want %v", err, statestore.ErrInvalidRecovery)
		}
	})

	t.Run("enqueue-refuses-a-task-that-cannot-take-a-new-attempt", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "enqueue-cancelled", attemptstate.CloseReasonReclaimed)
		cancelled := i.task
		cancelled.CancellationRequested = true
		cancelled.Revision++
		cancelled.UpdatedAt = cancelled.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(ctx, i.task.ID, i.task.Revision, cancelled); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.EnqueueRecovery(ctx, i.pending(t, policy)); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("error = %v, want %v", err, statestore.ErrConflict)
		}
	})

	t.Run("a-task-waits-for-at-most-one-retry", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "enqueue-one", attemptstate.CloseReasonReclaimed)
		enqueue(t, store, i.pending(t, policy))

		second := closeAttempt(t, store, newAttemptFor(t, store, i.task, 2), attemptstate.CloseReasonReclaimed)
		other := interrupted{task: i.task, attempt: second}
		if _, _, err := store.EnqueueRecovery(ctx, other.pending(t, policy)); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("a second retry for one Task error = %v, want %v", err, statestore.ErrConflict)
		}
	})

	t.Run("release-turns-the-retry-into-a-permit-and-records-the-budget", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "release", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)

		r := pass(t, store, policy, p.NotBefore)
		if len(r.Decision.Permits) != 1 {
			t.Fatalf("decision = %+v", r.Decision)
		}
		commit(t, store, r)

		if got, _ := store.LoadRecoveryPending(ctx); len(got) != 0 {
			t.Fatalf("the released retry is still pending: %+v", got)
		}
		stored, ok, err := store.LoadRecoveryPermit(ctx, i.attempt.ID)
		if err != nil || !ok || stored.Permit.Digest() != r.Decision.Permits[0].Digest() || stored.SpentBy != "" {
			t.Fatalf("LoadRecoveryPermit() = %+v, %v, %v", stored, ok, err)
		}
		w, ok, err := store.LoadRecoveryWindow(ctx)
		if err != nil || !ok || w.LastSequence != 1 || w.PolicyRevision != 7 || w.Budget.Released != 1 {
			t.Fatalf("LoadRecoveryWindow() = %+v, %v, %v", w, ok, err)
		}
	})

	t.Run("a-repeated-pass-changes-nothing", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "release-repeat", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)
		r := pass(t, store, policy, p.NotBefore)
		commit(t, store, r)

		replayed, err := store.CommitRecoveryRelease(ctx, r)
		if err != nil || !replayed {
			t.Fatalf("repeat = replayed %v, %v", replayed, err)
		}
		if w, _, _ := store.LoadRecoveryWindow(ctx); w.LastSequence != 1 || w.Budget.Released != 1 {
			t.Fatalf("a repeat consumed the budget again: %+v", w)
		}
	})

	t.Run("another-pass-under-a-used-sequence-conflicts", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "release-sequence", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)
		first := pass(t, store, policy, p.NotBefore)
		second := pass(t, store, policy, p.NotBefore.Add(5*time.Second))
		commit(t, store, first)
		if _, err := store.CommitRecoveryRelease(ctx, second); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("another pass under sequence 1 error = %v, want %v", err, statestore.ErrConflict)
		}
	})

	t.Run("a-pass-must-follow-the-stored-sequence", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "release-gap", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)
		decision, err := recovery.Release(p.NotBefore, policy, []recovery.Pending{p}, recovery.Budget{}, 2)
		if err != nil {
			t.Fatal(err)
		}
		r := statestore.RecoveryRelease{Policy: policy, Now: p.NotBefore, Decision: decision}
		if _, err := store.CommitRecoveryRelease(ctx, r); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("a pass that skips a sequence error = %v, want %v", err, statestore.ErrConflict)
		}
	})

	t.Run("a-pass-from-a-stale-budget-conflicts", func(t *testing.T) {
		store := newStore(t)
		a := newInterrupted(t, store, "release-stale-a", attemptstate.CloseReasonReclaimed)
		b := newInterrupted(t, store, "release-stale-b", attemptstate.CloseReasonReclaimed)
		pa, pb := a.pending(t, policy), b.pending(t, policy)
		enqueue(t, store, pa)
		enqueue(t, store, pb)
		at := pa.NotBefore
		if pb.NotBefore.After(at) {
			at = pb.NotBefore
		}
		first := pass(t, store, policy, at)
		commit(t, store, first)

		// A caller that forgets what the window consumed asks for sequence 2 from zero.
		decision, err := recovery.Release(at, policy, nil, recovery.Budget{}, 2)
		if err != nil {
			t.Fatal(err)
		}
		stale := statestore.RecoveryRelease{Policy: policy, Now: at, ExpectedBudget: recovery.Budget{}, Decision: decision}
		if _, err := store.CommitRecoveryRelease(ctx, stale); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("a pass from a reset budget error = %v, want %v", err, statestore.ErrConflict)
		}
	})

	t.Run("a-pass-that-understates-what-it-consumed-is-refused", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "release-understated", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)
		r := pass(t, store, policy, p.NotBefore)
		r.Decision.Budget.Released = 0
		if _, err := store.CommitRecoveryRelease(ctx, r); !errors.Is(err, statestore.ErrInvalidRecovery) {
			t.Fatalf("understated budget error = %v, want %v", err, statestore.ErrInvalidRecovery)
		}
		if got, _ := store.LoadRecoveryPending(ctx); len(got) != 1 {
			t.Fatalf("a refused pass released the retry: %+v", got)
		}
	})

	t.Run("a-permit-needs-the-pending-retry-it-was-issued-for", func(t *testing.T) {
		store := newStore(t)
		stored := newInterrupted(t, store, "release-stored", attemptstate.CloseReasonReclaimed)
		other := newInterrupted(t, store, "release-other", attemptstate.CloseReasonReclaimed)
		enqueue(t, store, stored.pending(t, policy))
		foreign := other.pending(t, policy) // never enqueued
		decision, err := recovery.Release(foreign.NotBefore, policy, []recovery.Pending{foreign}, recovery.Budget{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		r := statestore.RecoveryRelease{Policy: policy, Now: foreign.NotBefore, Decision: decision}
		if _, err := store.CommitRecoveryRelease(ctx, r); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("a Permit for a retry that was never stored error = %v, want %v", err, statestore.ErrConflict)
		}
	})

	t.Run("a-permit-for-another-retry-of-the-same-task-is-refused", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "release-same-task", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)

		// The Permit is for a retry of the same Task, but of another Attempt.
		elsewhere := p
		elsewhere.PreviousAttemptID = mustAttemptID(t)
		decision, err := recovery.Release(p.NotBefore, policy, []recovery.Pending{elsewhere}, recovery.Budget{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		r := statestore.RecoveryRelease{Policy: policy, Now: p.NotBefore, Decision: decision}
		if _, err := store.CommitRecoveryRelease(ctx, r); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("a Permit for another Attempt of the Task error = %v, want %v", err, statestore.ErrConflict)
		}
		if got, _ := store.LoadRecoveryPending(ctx); len(got) != 1 || got[0] != p {
			t.Fatalf("a refused pass changed the pending retry: %+v", got)
		}
	})

	t.Run("the-window-carries-its-consumption-between-passes", func(t *testing.T) {
		store := newStore(t)
		tight := recoveryPolicy(7)
		tight.MaxReleasesPerWindow = 1
		a := newInterrupted(t, store, "window-a", attemptstate.CloseReasonReclaimed)
		b := newInterrupted(t, store, "window-b", attemptstate.CloseReasonReclaimed)
		pa, pb := a.pending(t, tight), b.pending(t, tight)
		enqueue(t, store, pa)
		enqueue(t, store, pb)
		at := pa.NotBefore
		if pb.NotBefore.After(at) {
			at = pb.NotBefore
		}

		first := pass(t, store, tight, at)
		if len(first.Decision.Permits) != 1 {
			t.Fatalf("first pass = %+v", first.Decision)
		}
		commit(t, store, first)

		// The same window: the budget is spent, so the other retry waits.
		second := pass(t, store, tight, at.Add(10*time.Second))
		if len(second.Decision.Permits) != 0 {
			t.Fatalf("a spent window released again: %+v", second.Decision)
		}
		commit(t, store, second)
		if got, _ := store.LoadRecoveryPending(ctx); len(got) != 1 {
			t.Fatalf("pending = %+v, want the deferred retry", got)
		}

		// A later window starts again.
		third := pass(t, store, tight, at.Add(2*time.Minute))
		if len(third.Decision.Permits) != 1 {
			t.Fatalf("the next window did not release: %+v", third.Decision)
		}
		commit(t, store, third)
		if got, _ := store.LoadRecoveryPending(ctx); len(got) != 0 {
			t.Fatalf("pending = %+v, want none", got)
		}
	})

	t.Run("a-new-policy-revision-starts-a-new-window", func(t *testing.T) {
		store := newStore(t)
		a := newInterrupted(t, store, "revision-a", attemptstate.CloseReasonReclaimed)
		b := newInterrupted(t, store, "revision-b", attemptstate.CloseReasonReclaimed)
		pa, pb := a.pending(t, policy), b.pending(t, policy)
		enqueue(t, store, pa)
		enqueue(t, store, pb)
		at := pa.NotBefore
		if pb.NotBefore.After(at) {
			at = pb.NotBefore
		}
		commit(t, store, pass(t, store, policy, at))

		next := recoveryPolicy(8)
		// The stored budget belongs to revision 7. Passing it into revision 8 is refused.
		w, _, _ := store.LoadRecoveryWindow(ctx)
		decision, err := recovery.Release(at.Add(time.Second), next, nil, w.Budget, w.LastSequence+1)
		if err != nil {
			t.Fatal(err)
		}
		carried := statestore.RecoveryRelease{Policy: next, Now: at.Add(time.Second), ExpectedBudget: w.Budget, Decision: decision}
		if _, err := store.CommitRecoveryRelease(ctx, carried); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("a budget carried across revisions error = %v, want %v", err, statestore.ErrConflict)
		}
		commit(t, store, pass(t, store, next, at.Add(time.Second)))
		if got, _, _ := store.LoadRecoveryWindow(ctx); got.PolicyRevision != 8 || got.LastSequence != w.LastSequence+1 {
			t.Fatalf("window = %+v", got)
		}
	})

	t.Run("a-retry-whose-deadline-passed-is-dropped", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "expired", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		p.Deadline = p.NotBefore.Add(time.Second)
		enqueue(t, store, p)
		r := pass(t, store, policy, p.Deadline.Add(time.Second))
		if len(r.Decision.Expired) != 1 {
			t.Fatalf("decision = %+v", r.Decision)
		}
		commit(t, store, r)
		if got, _ := store.LoadRecoveryPending(ctx); len(got) != 0 {
			t.Fatalf("an expired retry stayed pending: %+v", got)
		}
		if _, ok, _ := store.LoadRecoveryPermit(ctx, i.attempt.ID); ok {
			t.Fatal("an expired retry got a Permit")
		}
	})

	// released returns an interrupted Task whose retry already holds a Permit.
	released := func(t *testing.T, store RecoveryTestStore, seed string) (interrupted, recovery.Permit, time.Time) {
		t.Helper()
		i := newInterrupted(t, store, seed, attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)
		r := pass(t, store, policy, p.NotBefore)
		commit(t, store, r)
		return i, r.Decision.Permits[0], p.NotBefore
	}

	t.Run("admission-creates-the-attempt-and-spends-the-permit-together", func(t *testing.T) {
		store := newStore(t)
		i, permit, at := released(t, store, "admit")
		a := admissionFor(t, i, permit, policy, at)
		if err := store.AdmitRecoveryAttempt(ctx, a); err != nil {
			t.Fatalf("AdmitRecoveryAttempt() error = %v", err)
		}
		got, err := store.LoadAttempt(ctx, a.NewAttempt.ID)
		if err != nil || got.Number != 2 || got.Phase != attemptstate.AttemptPhaseCreated {
			t.Fatalf("new Attempt = %+v, %v", got, err)
		}
		stored, _, _ := store.LoadRecoveryPermit(ctx, i.attempt.ID)
		if stored.SpentBy != a.NewAttempt.ID {
			t.Fatalf("the Permit is not marked as used: %+v", stored)
		}
		again := admissionFor(t, i, permit, policy, at)
		if err := store.AdmitRecoveryAttempt(ctx, again); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("second admission error = %v, want %v", err, statestore.ErrAlreadyExists)
		}
		if _, err := store.LoadAttempt(ctx, again.NewAttempt.ID); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("a second Attempt was created from one Permit: %v", err)
		}
	})

	t.Run("admission-refuses-a-permit-that-is-not-valid-now", func(t *testing.T) {
		store := newStore(t)
		i, permit, at := released(t, store, "admit-invalid")
		cases := map[string]struct {
			now      time.Time
			revision uint64
			want     error
		}{
			"before it starts":     {now: permit.NotBefore().Add(-time.Second), revision: 7, want: recovery.ErrPermitNotYetValid},
			"after it expires":     {now: permit.ExpiresAt().Add(time.Second), revision: 7, want: recovery.ErrPermitExpired},
			"under a newer policy": {now: at, revision: 8, want: recovery.ErrPermitPolicyRevision},
		}
		for name, tc := range cases {
			a := admissionFor(t, i, permit, policy, tc.now)
			a.PolicyRevision = tc.revision
			if err := store.AdmitRecoveryAttempt(ctx, a); !errors.Is(err, tc.want) {
				t.Fatalf("%s: error = %v, want %v", name, err, tc.want)
			}
			if _, err := store.LoadAttempt(ctx, a.NewAttempt.ID); !errors.Is(err, statestore.ErrNotFound) {
				t.Fatalf("%s: an Attempt was created: %v", name, err)
			}
		}
		if stored, _, _ := store.LoadRecoveryPermit(ctx, i.attempt.ID); stored.SpentBy != "" {
			t.Fatal("a refused admission spent the Permit")
		}
	})

	t.Run("admission-refuses-an-attempt-the-permit-does-not-admit", func(t *testing.T) {
		store := newStore(t)
		i, permit, at := released(t, store, "admit-mismatch")
		wrongNumber := admissionFor(t, i, permit, policy, at)
		wrongNumber.NewAttempt.Number = 3
		if err := store.AdmitRecoveryAttempt(ctx, wrongNumber); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("another attempt number error = %v, want %v", err, statestore.ErrConflict)
		}
		other := newInterrupted(t, store, "admit-other-task", attemptstate.CloseReasonReclaimed)
		wrongTask := admissionFor(t, other, permit, policy, at)
		wrongTask.PreviousAttemptID = i.attempt.ID
		if err := store.AdmitRecoveryAttempt(ctx, wrongTask); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("an Attempt of another Task error = %v, want %v", err, statestore.ErrConflict)
		}
		if stored, _, _ := store.LoadRecoveryPermit(ctx, i.attempt.ID); stored.SpentBy != "" {
			t.Fatal("a refused admission spent the Permit")
		}
	})

	t.Run("admission-without-a-permit-is-refused", func(t *testing.T) {
		store := newStore(t)
		i := newInterrupted(t, store, "admit-none", attemptstate.CloseReasonReclaimed)
		p := i.pending(t, policy)
		enqueue(t, store, p)
		stray, err := recovery.Release(p.NotBefore, policy, []recovery.Pending{p}, recovery.Budget{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		// The Permit exists in memory, but it was never committed.
		a := admissionFor(t, i, stray.Permits[0], policy, p.NotBefore)
		if err := store.AdmitRecoveryAttempt(ctx, a); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("admission of a Permit that was never committed error = %v, want %v", err, statestore.ErrNotFound)
		}
	})

	t.Run("a-task-that-cannot-take-the-attempt-keeps-the-permit-unused", func(t *testing.T) {
		store := newStore(t)
		i, permit, at := released(t, store, "admit-cancelled")
		cancelled := i.task
		cancelled.CancellationRequested = true
		cancelled.Revision++
		cancelled.UpdatedAt = cancelled.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(ctx, i.task.ID, i.task.Revision, cancelled); err != nil {
			t.Fatal(err)
		}
		a := admissionFor(t, i, permit, policy, at)
		if err := store.AdmitRecoveryAttempt(ctx, a); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("error = %v, want %v", err, statestore.ErrConflict)
		}
		if stored, _, _ := store.LoadRecoveryPermit(ctx, i.attempt.ID); stored.SpentBy != "" {
			t.Fatal("the Permit was spent although no Attempt was created")
		}
	})

	t.Run("an-attempt-id-that-exists-keeps-the-permit-unused", func(t *testing.T) {
		store := newStore(t)
		i, permit, at := released(t, store, "admit-duplicate")
		a := admissionFor(t, i, permit, policy, at)
		taken := a.NewAttempt
		taken.Number = 9
		if err := store.CreateAttempt(ctx, taken); err != nil {
			t.Fatal(err)
		}
		if err := store.AdmitRecoveryAttempt(ctx, a); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("error = %v, want %v", err, statestore.ErrAlreadyExists)
		}
		if stored, _, _ := store.LoadRecoveryPermit(ctx, i.attempt.ID); stored.SpentBy != "" {
			t.Fatal("the Permit was spent although the Attempt was not created")
		}
	})
}

// RunRecoveryDurabilityContract fixes that the retries that wait, the budget a
// window consumed, and the Permits and their use survive a restart unchanged.
func RunRecoveryDurabilityContract(t *testing.T, factory DurableRecoveryFactory) {
	t.Helper()
	ctx := context.Background()
	policy := recoveryPolicy(7)

	t.Run("recovery-state-survives-restart", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		waiting := newInterrupted(t, store, "durable-waiting", attemptstate.CloseReasonReclaimed)
		held := newInterrupted(t, store, "durable-released", attemptstate.CloseReasonReclaimed)
		waitingPending := waiting.pending(t, policy)
		releasedPending := held.pending(t, policy)
		enqueue(t, store, releasedPending)
		r := pass(t, store, policy, releasedPending.NotBefore)
		commit(t, store, r)
		enqueue(t, store, waitingPending)
		permit := r.Decision.Permits[0]
		a := admissionFor(t, held, permit, policy, releasedPending.NotBefore)
		if err := store.AdmitRecoveryAttempt(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		defer func() { _ = closeReopened() }()
		pending, err := reopened.LoadRecoveryPending(ctx)
		if err != nil || len(pending) != 1 || pending[0] != waitingPending {
			t.Fatalf("pending after restart = %+v, %v, want the waiting retry unchanged (NotBefore included)", pending, err)
		}
		w, ok, err := reopened.LoadRecoveryWindow(ctx)
		if err != nil || !ok || w.LastSequence != 1 || w.Budget != r.Decision.Budget {
			t.Fatalf("window after restart = %+v, %v, %v", w, ok, err)
		}
		stored, ok, err := reopened.LoadRecoveryPermit(ctx, held.attempt.ID)
		if err != nil || !ok || stored.Permit.Digest() != permit.Digest() || stored.SpentBy != a.NewAttempt.ID {
			t.Fatalf("Permit after restart = %+v, %v, %v", stored, ok, err)
		}
		if replayed, err := reopened.CommitRecoveryRelease(ctx, r); err != nil || !replayed {
			t.Fatalf("repeat of the committed pass after restart = replayed %v, %v", replayed, err)
		}
		if err := reopened.AdmitRecoveryAttempt(ctx, admissionFor(t, held, permit, policy, releasedPending.NotBefore)); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("second admission after restart error = %v, want %v", err, statestore.ErrAlreadyExists)
		}
	})
}
