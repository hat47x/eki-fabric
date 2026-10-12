package statestoretest

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type TaskLeaseStore interface {
	statestore.TaskStore
	statestore.AttemptStore
	statestore.LeaseStore
	statestore.PolicyStore
}

type LeaseFactory func(t testing.TB) TaskLeaseStore

type DurableLeaseFactory struct {
	Open func(t testing.TB, location string) (store TaskLeaseStore, close func() error)
}

func RunLeaseStoreContract(t *testing.T, newStore LeaseFactory) {
	t.Helper()

	t.Run("reserve-load-replay", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "lease-replay")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))

		first, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatalf("ReserveLease() error = %v", err)
		}
		if first.Replayed || first.Lease.FenceEpoch != 1 || first.Lease.RenewalSequence != 0 {
			t.Fatalf("first reservation = %#v", first)
		}
		assigned, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
		if err != nil {
			t.Fatalf("LoadAttempt() after reservation error = %v", err)
		}
		if assigned.Phase != attemptstate.AttemptPhaseAssigned ||
			assigned.LeaseID != first.Lease.ID ||
			assigned.WorkerIdentity != first.Lease.WorkerIdentity ||
			assigned.Revision != reservation.ExpectedAttemptRevision+1 {
			t.Fatalf("Attempt was not assigned atomically with Lease: %#v", assigned)
		}
		loaded, err := store.LoadLease(context.Background(), first.Lease.ID)
		if err != nil {
			t.Fatalf("LoadLease() error = %v", err)
		}
		assertLeaseEqual(t, loaded, first.Lease)

		replay := reservation
		replay.LeaseID = mustLeaseID(t)
		again, err := store.ReserveLease(context.Background(), replay)
		if err != nil {
			t.Fatalf("replayed ReserveLease() error = %v", err)
		}
		if !again.Replayed || again.Lease.ID != first.Lease.ID || again.Lease.FenceEpoch != 1 {
			t.Fatalf("replay created new authority state: first=%#v again=%#v", first, again)
		}
	})

	t.Run("exact-replay-survives-benign-task-revision", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "replay-revision")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		first, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatal(err)
		}

		active := task
		active.Phase = taskstate.TaskPhaseActive
		active.Revision++
		active.UpdatedAt = task.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, active); err != nil {
			t.Fatal(err)
		}

		replay := reservation
		replay.LeaseID = mustLeaseID(t)
		again, err := store.ReserveLease(context.Background(), replay)
		if err != nil {
			t.Fatalf("exact replay after ready->active revision error = %v", err)
		}
		if !again.Replayed || again.Lease.ID != first.Lease.ID || again.Lease.FenceEpoch != first.Lease.FenceEpoch {
			t.Fatalf("benign Task revision minted or lost Lease: first=%#v replay=%#v", first, again)
		}
	})

	t.Run("replay-after-cancellation-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "replay-cancel")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), reservation); err != nil {
			t.Fatal(err)
		}

		cancelled := task
		cancelled.CancellationRequested = true
		cancelled.Revision++
		cancelled.UpdatedAt = task.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, cancelled); err != nil {
			t.Fatal(err)
		}

		replay := reservation
		replay.LeaseID = mustLeaseID(t)
		replay.ExpectedTaskRevision = cancelled.Revision
		if _, err := store.ReserveLease(context.Background(), replay); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("cancelled replay error = %v, want ErrConflict", err)
		}
	})

	t.Run("replay-after-unschedulable-phase-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "replay-held")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), reservation); err != nil {
			t.Fatal(err)
		}

		held := task
		held.Phase = taskstate.TaskPhaseHeld
		held.Revision++
		held.UpdatedAt = task.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, held); err != nil {
			t.Fatal(err)
		}

		replay := reservation
		replay.LeaseID = mustLeaseID(t)
		replay.ExpectedTaskRevision = held.Revision
		if _, err := store.ReserveLease(context.Background(), replay); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("held replay error = %v, want ErrConflict", err)
		}
	})

	t.Run("replay-cannot-change-placement-subject", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "replay-subject")
		first := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		otherTask := readyTask(t, store, "replay-subject-other-task")

		// The policy row is rejected by the policy-currentness check, which runs
		// before the placement subject comparison, so it reports ErrPolicyChanged.
		tests := map[string]struct {
			mutate func(*statestore.LeaseReservation)
			want   error
		}{
			"task": {want: statestore.ErrPlacementDecisionConflict, mutate: func(r *statestore.LeaseReservation) {
				r.TaskID = otherTask.ID
				r.TaskIRDigest = otherTask.TaskIRDigest
				r.ExpectedTaskRevision = otherTask.Revision
			}},
			"attempt": {want: statestore.ErrPlacementDecisionConflict, mutate: func(r *statestore.LeaseReservation) { r.AttemptID = mustAttemptID(t) }},
			"worker": {want: statestore.ErrPlacementDecisionConflict, mutate: func(r *statestore.LeaseReservation) { r.WorkerIdentity = "worker-b" }},
			"policy": {want: statestore.ErrPolicyChanged, mutate: func(r *statestore.LeaseReservation) { r.PolicyRevision = "policy-43" }},
			"placement-expiry": {want: statestore.ErrPlacementDecisionConflict, mutate: func(r *statestore.LeaseReservation) { r.PlacementExpiresAt = r.PlacementExpiresAt.Add(time.Second) }},
		}
		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				replay := first
				replay.LeaseID = mustLeaseID(t)
				tc.mutate(&replay)
				if _, err := store.ReserveLease(context.Background(), replay); !errors.Is(err, tc.want) {
					t.Fatalf("ReserveLease() error = %v, want %v", err, tc.want)
				}
			})
		}
	})

	t.Run("same-decision-and-principal-from-different-trust-domain-is-distinct", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "domain-scope")
		first := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), first); err != nil {
			t.Fatal(err)
		}

		secondNow := first.ExpiresAt.Add(time.Nanosecond)
		second := leaseReservation(t, store, task, "placement-1", secondNow, secondNow.Add(time.Minute))
		second.PlacementTrustDomain = "federated.example"
		second.PlacementPayloadDigest = contract.SHA256([]byte("federated payload"))
		got, err := store.ReserveLease(context.Background(), second)
		if err != nil {
			t.Fatalf("different trust domain collided on Decision identity: %v", err)
		}
		if got.Replayed || got.Lease.FenceEpoch != 2 {
			t.Fatalf("different trust domain was treated as replay: %#v", got)
		}
	})

	t.Run("same-decision-id-from-different-plc-principal-is-distinct", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "principal-scope")
		first := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), first); err != nil {
			t.Fatal(err)
		}

		secondNow := first.ExpiresAt.Add(time.Nanosecond)
		second := leaseReservation(t, store, task, "placement-1", secondNow, secondNow.Add(time.Minute))
		second.PlacementAuthorityPrincipal = "control-plane/plc-2"
		second.PlacementPayloadDigest = contract.SHA256([]byte("plc-2 payload"))
		got, err := store.ReserveLease(context.Background(), second)
		if err != nil {
			t.Fatalf("different PLC principal collided on Decision ID: %v", err)
		}
		if got.Replayed || got.Lease.FenceEpoch != 2 {
			t.Fatalf("different PLC principal was treated as replay: %#v", got)
		}
	})

	t.Run("decision-id-cannot-be-rebound", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "decision-rebind")
		first := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), first); err != nil {
			t.Fatal(err)
		}

		conflict := first
		conflict.LeaseID = mustLeaseID(t)
		conflict.PlacementPayloadDigest = contract.SHA256([]byte("different signed payload"))
		if _, err := store.ReserveLease(context.Background(), conflict); !errors.Is(err, statestore.ErrPlacementDecisionConflict) {
			t.Fatalf("ReserveLease() error = %v, want ErrPlacementDecisionConflict", err)
		}
	})

	t.Run("active-lease-blocks-second-placement", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "active-block")
		first := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), first); err != nil {
			t.Fatal(err)
		}

		second := leaseReservation(t, store, task, "placement-2", leaseNow.Add(10*time.Second), leaseNow.Add(70*time.Second))
		if _, err := store.ReserveLease(context.Background(), second); !errors.Is(err, statestore.ErrLeaseActive) {
			t.Fatalf("ReserveLease() error = %v, want ErrLeaseActive", err)
		}
	})

	t.Run("expired-placement-replay-does-not-mint-a-new-fence", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "expired-replay")
		firstReq := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		first, err := store.ReserveLease(context.Background(), firstReq)
		if err != nil {
			t.Fatal(err)
		}

		replay := firstReq
		replay.LeaseID = mustLeaseID(t)
		replay.IssuedAt = first.Lease.ExpiresAt.Add(time.Second)
		replay.ExpiresAt = firstReq.PlacementExpiresAt
		again, err := store.ReserveLease(context.Background(), replay)
		if err != nil {
			t.Fatalf("expired placement replay error = %v", err)
		}
		if !again.Replayed || again.Lease.ID != first.Lease.ID || again.Lease.FenceEpoch != first.Lease.FenceEpoch {
			t.Fatalf("expired placement replay minted new lease: first=%#v again=%#v", first, again)
		}
		if again.Lease.ActiveAt(replay.IssuedAt) {
			t.Fatal("expired placement replay revived the old lease")
		}
	})

	t.Run("future-issued-lease-still-reserves-the-fence", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "future-fence")
		future := leaseReservation(t, store, task, "placement-future", leaseNow.Add(30*time.Second), leaseNow.Add(90*time.Second))
		if _, err := store.ReserveLease(context.Background(), future); err != nil {
			t.Fatal(err)
		}

		second := leaseReservation(t, store, task, "placement-now", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), second); !errors.Is(err, statestore.ErrLeaseActive) {
			t.Fatalf("ReserveLease() error = %v, want ErrLeaseActive", err)
		}
	})

	t.Run("expired-lease-allows-next-monotonic-fence", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "fence")
		first := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		a, err := store.ReserveLease(context.Background(), first)
		if err != nil {
			t.Fatal(err)
		}

		secondNow := first.ExpiresAt.Add(time.Nanosecond)
		second := leaseReservation(t, store, task, "placement-2", secondNow, secondNow.Add(time.Minute))
		b, err := store.ReserveLease(context.Background(), second)
		if err != nil {
			t.Fatalf("second ReserveLease() error = %v", err)
		}
		if a.Lease.FenceEpoch != 1 || b.Lease.FenceEpoch != 2 {
			t.Fatalf("fence epochs = %d -> %d, want 1 -> 2", a.Lease.FenceEpoch, b.Lease.FenceEpoch)
		}
		if a.Lease.ID == b.Lease.ID {
			t.Fatal("new placement reused old Lease ID")
		}
	})

	t.Run("task-revision-drift-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "revision-drift")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))

		next := task
		next.Revision++
		next.UpdatedAt = task.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, next); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("ReserveLease() error = %v, want ErrConflict", err)
		}
	})

	t.Run("attempt-revision-drift-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "attempt-revision-drift")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))

		current, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		replanned := current
		replanned.Revision++
		replanned.UpdatedAt = current.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(context.Background(), current.ID, current.Revision, replanned); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("ReserveLease() error = %v, want ErrConflict for stale Attempt revision", err)
		}
	})

	t.Run("worker-replan-invalidates-stale-placement-binding", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "attempt-worker-replan")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))

		current, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		replanned := current
		replanned.WorkerIdentity = "worker-b"
		replanned.Revision++
		replanned.UpdatedAt = current.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(context.Background(), current.ID, current.Revision, replanned); err != nil {
			t.Fatal(err)
		}

		reservation.ExpectedAttemptRevision = replanned.Revision
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("ReserveLease() error = %v, want ErrConflict for stale Worker binding", err)
		}
	})

	t.Run("exact-replay-survives-benign-attempt-progress", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "attempt-replay-progress")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		first, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatal(err)
		}

		assigned, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		dispatched := assigned
		dispatched.Phase = attemptstate.AttemptPhaseDispatched
		dispatched.Revision++
		dispatched.UpdatedAt = assigned.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(context.Background(), assigned.ID, assigned.Revision, dispatched); err != nil {
			t.Fatal(err)
		}

		replay := reservation
		replay.LeaseID = mustLeaseID(t)
		again, err := store.ReserveLease(context.Background(), replay)
		if err != nil {
			t.Fatalf("replay after Attempt progress error = %v", err)
		}
		if !again.Replayed || again.Lease.ID != first.Lease.ID || again.Lease.FenceEpoch != first.Lease.FenceEpoch {
			t.Fatalf("Attempt progress changed replay authority: first=%#v replay=%#v", first, again)
		}
	})

	t.Run("exact-replay-keeps-attempt-revision-and-fence", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "replay-no-advance")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		first, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatal(err)
		}
		before, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
		if err != nil {
			t.Fatal(err)
		}

		replay := reservation
		replay.LeaseID = mustLeaseID(t)
		again, err := store.ReserveLease(context.Background(), replay)
		if err != nil {
			t.Fatalf("exact replay error = %v", err)
		}
		if !again.Replayed || again.Lease.ID != first.Lease.ID || again.Lease.FenceEpoch != first.Lease.FenceEpoch {
			t.Fatalf("exact replay = %#v, want stored Lease at FenceEpoch %d", again, first.Lease.FenceEpoch)
		}
		after, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		if after.Revision != before.Revision || after.Phase != before.Phase || after.LeaseID != before.LeaseID {
			t.Fatalf("exact replay advanced Attempt: before=%#v after=%#v", before, after)
		}
		// The replay must not have stored the caller's new Lease ID either.
		assertLeaseNotReserved(t, store, replay.LeaseID)

		// The replay must not consume a FenceEpoch. Once the stored Lease has
		// expired, the next Attempt must receive the next epoch, not the one
		// after it.
		secondNow := first.Lease.ExpiresAt.Add(time.Nanosecond)
		second := leaseReservation(t, store, task, "placement-2", secondNow, secondNow.Add(time.Minute))
		got, err := store.ReserveLease(context.Background(), second)
		if err != nil {
			t.Fatalf("ReserveLease() after exact replay error = %v", err)
		}
		if got.Lease.FenceEpoch != first.Lease.FenceEpoch+1 {
			t.Fatalf("fence after exact replay = %d, want %d", got.Lease.FenceEpoch, first.Lease.FenceEpoch+1)
		}
	})

	t.Run("replay-after-attempt-close-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "attempt-replay-closed")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), reservation); err != nil {
			t.Fatal(err)
		}

		assigned, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		closed := assigned
		closed.Phase = attemptstate.AttemptPhaseClosed
		closed.CloseReason = attemptstate.CloseReasonReclaimed
		closed.Revision++
		closed.UpdatedAt = assigned.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(context.Background(), assigned.ID, assigned.Revision, closed); err != nil {
			t.Fatal(err)
		}

		replay := reservation
		replay.LeaseID = mustLeaseID(t)
		if _, err := store.ReserveLease(context.Background(), replay); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("closed Attempt replay error = %v, want ErrConflict", err)
		}
	})

	t.Run("replay-after-superseded-close-fails-closed", func(t *testing.T) {
		// Supersession closes the Attempt that a newer Attempt replaces. The
		// Lease stored on that closed Attempt must not come back on replay.
		store := newStore(t)
		task := readyTask(t, store, "replay-superseded")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), reservation); err != nil {
			t.Fatal(err)
		}
		closeAttemptWith(t, store, reservation.AttemptID, attemptstate.CloseReasonSuperseded)

		replay := reservation
		replay.LeaseID = mustLeaseID(t)
		if _, err := store.ReserveLease(context.Background(), replay); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("superseded Attempt replay error = %v, want ErrConflict", err)
		}
		assertLeaseNotReserved(t, store, replay.LeaseID)
	})

	t.Run("exact-replay-succeeds-in-each-held-phase", func(t *testing.T) {
		// A Lease is held from A-ASSIGNED through A-VERIFYING. In each of these
		// phases the same Placement Decision must converge to the stored Lease
		// without minting a new FenceEpoch, and only a close may stop it.
		for _, phase := range []attemptstate.AttemptPhase{
			attemptstate.AttemptPhaseAssigned,
			attemptstate.AttemptPhaseDispatched,
			attemptstate.AttemptPhaseClaimed,
			attemptstate.AttemptPhaseVerifying,
		} {
			t.Run(string(phase), func(t *testing.T) {
				store := newStore(t)
				task := readyTask(t, store, "replay-held-"+string(phase))
				reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
				first, err := store.ReserveLease(context.Background(), reservation)
				if err != nil {
					t.Fatal(err)
				}
				advanceAttemptTo(t, store, reservation.AttemptID, phase)

				replay := reservation
				replay.LeaseID = mustLeaseID(t)
				again, err := store.ReserveLease(context.Background(), replay)
				if err != nil {
					t.Fatalf("exact replay in %s error = %v", phase, err)
				}
				if !again.Replayed || again.Lease.ID != first.Lease.ID || again.Lease.FenceEpoch != first.Lease.FenceEpoch {
					t.Fatalf("exact replay in %s = %#v, want stored Lease at FenceEpoch %d", phase, again, first.Lease.FenceEpoch)
				}
				assertLeaseNotReserved(t, store, replay.LeaseID)

				closeAttemptWith(t, store, reservation.AttemptID, attemptstate.CloseReasonReclaimed)

				afterClose := reservation
				afterClose.LeaseID = mustLeaseID(t)
				if _, err := store.ReserveLease(context.Background(), afterClose); !errors.Is(err, statestore.ErrConflict) {
					t.Fatalf("exact replay after close from %s error = %v, want ErrConflict", phase, err)
				}
				assertLeaseNotReserved(t, store, afterClose.LeaseID)
			})
		}
	})

	t.Run("attempt-close-races-lease-reservation-with-one-winner", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "attempt-close-race")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		placing, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		closed := placing
		closed.Phase = attemptstate.AttemptPhaseClosed
		closed.CloseReason = attemptstate.CloseReasonSuperseded
		closed.Revision++
		closed.UpdatedAt = placing.UpdatedAt.Add(time.Second)

		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.ReserveLease(context.Background(), reservation)
			results <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			results <- store.CompareAndSwapAttempt(context.Background(), placing.ID, placing.Revision, closed)
		}()
		close(start)
		wg.Wait()
		close(results)

		var success, conflict int
		for err := range results {
			switch {
			case err == nil:
				success++
			case errors.Is(err, statestore.ErrConflict):
				conflict++
			default:
				t.Fatalf("unexpected race error = %v", err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("Attempt close vs Lease reserve success=%d conflict=%d, want 1/1", success, conflict)
		}
	})

	t.Run("missing-attempt-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "missing-attempt")
		reservation := unbackedLeaseReservation(t, task, "placement-missing-attempt", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("missing Attempt error = %v, want ErrNotFound", err)
		}
	})

	t.Run("created-attempt-rejects-new-reservation", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "attempt-created")
		created, err := attemptstate.New(mustAttemptID(t), task.ID, task.TaskIRDigest, leaseAttemptSequence.Add(1), task.UpdatedAt)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateAttempt(context.Background(), created); err != nil {
			t.Fatal(err)
		}
		reservation := unbackedLeaseReservation(t, task, "placement-created-attempt", leaseNow, leaseNow.Add(time.Minute))
		reservation.AttemptID = created.ID
		reservation.ExpectedAttemptRevision = created.Revision
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("created Attempt reservation error = %v, want ErrConflict", err)
		}
		assertLeaseNotReserved(t, store, reservation.LeaseID)
	})

	t.Run("closed-attempt-rejects-new-reservation-without-consuming-fence", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "attempt-closed-new")
		reservation := leaseReservation(t, store, task, "placement-closed", leaseNow, leaseNow.Add(time.Minute))
		placing, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		closed := placing
		closed.Phase = attemptstate.AttemptPhaseClosed
		closed.CloseReason = attemptstate.CloseReasonSuperseded
		closed.Revision++
		closed.UpdatedAt = placing.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(context.Background(), placing.ID, placing.Revision, closed); err != nil {
			t.Fatal(err)
		}

		// The caller presents the closed Attempt's current revision, so only the
		// phase check may reject this reservation.
		reservation.ExpectedAttemptRevision = closed.Revision
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("closed Attempt reservation error = %v, want ErrConflict", err)
		}
		assertLeaseNotReserved(t, store, reservation.LeaseID)

		// A rejected reservation must not consume authority: the next valid Attempt
		// of the same Task receives the first FenceEpoch.
		next := leaseReservation(t, store, task, "placement-after-closed", leaseNow, leaseNow.Add(time.Minute))
		got, err := store.ReserveLease(context.Background(), next)
		if err != nil {
			t.Fatalf("ReserveLease() after closed Attempt error = %v", err)
		}
		if got.Lease.FenceEpoch != 1 {
			t.Fatalf("fence after rejected reservation = %d, want 1", got.Lease.FenceEpoch)
		}
	})

	t.Run("assigned-attempt-rejects-second-lease", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "attempt-assigned-second")
		first := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		assigned, err := store.LoadAttempt(context.Background(), first.AttemptID)
		if err != nil {
			t.Fatal(err)
		}

		// A-ASSIGNED admits only exact replay of the stored Lease. A different
		// Placement Decision on the same Attempt must not mint a second Lease.
		second := first
		second.LeaseID = mustLeaseID(t)
		second.PlacementDecisionID = "placement-2"
		second.PlacementPayloadDigest = contract.SHA256([]byte("payload:placement-2"))
		second.ExpectedAttemptRevision = assigned.Revision
		if _, err := store.ReserveLease(context.Background(), second); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("second Lease on assigned Attempt error = %v, want ErrConflict", err)
		}
		assertLeaseNotReserved(t, store, second.LeaseID)
	})

	t.Run("attempt-bound-to-other-task-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "attempt-binding-own")
		// The other Task shares the TaskIR digest, so the only difference the
		// reservation can see is the Attempt's Task identity.
		other := readyTask(t, store, "attempt-binding-own")
		foreign := leaseReservation(t, store, other, "placement-foreign-attempt", leaseNow, leaseNow.Add(time.Minute))

		// The Attempt is PLACING, current, and at its expected revision, but it
		// belongs to a different Task.
		reservation := unbackedLeaseReservation(t, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		reservation.AttemptID = foreign.AttemptID
		reservation.ExpectedAttemptRevision = foreign.ExpectedAttemptRevision
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("foreign Attempt reservation error = %v, want ErrConflict", err)
		}
		assertLeaseNotReserved(t, store, reservation.LeaseID)
	})

	t.Run("reservation-taskir-must-match-task", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "reservation-taskir")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		reservation.TaskIRDigest = contract.SHA256([]byte("reservation-taskir-other"))
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("mismatched TaskIR reservation error = %v, want ErrConflict", err)
		}
		assertLeaseNotReserved(t, store, reservation.LeaseID)
	})

	t.Run("cancellation-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "cancel")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))

		cancelled := task
		cancelled.CancellationRequested = true
		cancelled.Revision++
		cancelled.UpdatedAt = task.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, cancelled); err != nil {
			t.Fatal(err)
		}
		reservation.ExpectedTaskRevision = cancelled.Revision
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("ReserveLease() error = %v, want ErrConflict", err)
		}
	})

	t.Run("unschedulable-phase-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "held")
		// The Attempt is created while the Task is schedulable because adapters
		// reject new Attempts for held Tasks. Holding it afterwards must stop the
		// reservation even when the Attempt is current and at its expected revision.
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		held := task
		held.Phase = taskstate.TaskPhaseHeld
		held.Revision++
		held.UpdatedAt = task.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, held); err != nil {
			t.Fatal(err)
		}
		reservation.ExpectedTaskRevision = held.Revision
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("ReserveLease() error = %v, want ErrConflict", err)
		}
		assertLeaseNotReserved(t, store, reservation.LeaseID)
	})

	t.Run("policy-epoch-drift-fails-before-fence-allocation", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "policy-drift")
		stale := leaseReservation(t, store, task, "placement-old-policy", leaseNow, leaseNow.Add(time.Minute))

		current, err := store.LoadCurrentPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		revoked, err := policystate.Revoke(current, current.Epoch+1, leaseNow.Add(time.Second), "emergency revoke")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), current.Epoch, revoked); err != nil {
			t.Fatal(err)
		}

		if _, err := store.ReserveLease(context.Background(), stale); !errors.Is(err, statestore.ErrPolicyChanged) {
			t.Fatalf("stale policy reservation error = %v, want ErrPolicyChanged", err)
		}

		next, err := policystate.NewActive(revoked.Epoch+1, "policy-43", leaseNow.Add(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), revoked.Epoch, next); err != nil {
			t.Fatal(err)
		}
		fresh := stale
		fresh.PolicyRevision = next.Revision
		fresh.ExpectedPolicyEpoch = next.Epoch
		fresh.PlacementDecisionID = "placement-new-policy"
		fresh.PlacementPayloadDigest = contract.SHA256([]byte("payload:placement-new-policy"))
		result, err := store.ReserveLease(context.Background(), fresh)
		if err != nil {
			t.Fatalf("fresh policy reservation error = %v", err)
		}
		if result.Lease.FenceEpoch != 1 {
			t.Fatalf("failed stale reservation consumed fence: got=%d want=1", result.Lease.FenceEpoch)
		}
		if result.Lease.PolicyEpoch != next.Epoch || !result.Lease.PolicyFenced() {
			t.Fatalf("Lease policy binding = revision %q epoch %d", result.Lease.PolicyRevision, result.Lease.PolicyEpoch)
		}
	})

	t.Run("stale-epoch-only-reservation-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "stale-epoch-only")
		stale := leaseReservation(t, store, task, "placement-stale-epoch", leaseNow, leaseNow.Add(time.Minute))

		current, err := store.LoadCurrentPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		revoked, err := policystate.Revoke(current, current.Epoch+1, leaseNow.Add(time.Second), "rotate policy")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), current.Epoch, revoked); err != nil {
			t.Fatal(err)
		}
		next, err := policystate.NewActive(revoked.Epoch+1, "policy-43", leaseNow.Add(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), revoked.Epoch, next); err != nil {
			t.Fatal(err)
		}

		// The revision named here is active at the current epoch, so the only
		// stale field is ExpectedPolicyEpoch. The reservation must still fail.
		stale.PolicyRevision = next.Revision
		if _, err := store.ReserveLease(context.Background(), stale); !errors.Is(err, statestore.ErrPolicyChanged) {
			t.Fatalf("stale-epoch reservation error = %v, want ErrPolicyChanged", err)
		}
		assertLeaseNotReserved(t, store, stale.LeaseID)

		// The same revision presented at the current epoch is accepted, so the
		// refusal above came from the epoch and not from the revision.
		fresh := stale
		fresh.LeaseID = mustLeaseID(t)
		fresh.ExpectedPolicyEpoch = next.Epoch
		fresh.PlacementDecisionID = "placement-current-epoch"
		fresh.PlacementPayloadDigest = contract.SHA256([]byte("payload:placement-current-epoch"))
		result, err := store.ReserveLease(context.Background(), fresh)
		if err != nil {
			t.Fatalf("current-epoch reservation error = %v", err)
		}
		if result.Lease.FenceEpoch != 1 {
			t.Fatalf("refused stale-epoch reservation consumed fence: got=%d want=1", result.Lease.FenceEpoch)
		}
	})

	t.Run("policy-revoke-blocks-exact-replay", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "policy-replay")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), reservation); err != nil {
			t.Fatal(err)
		}
		current, err := store.LoadCurrentPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		revoked, err := policystate.Revoke(current, current.Epoch+1, leaseNow.Add(time.Second), "revoke before replay")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), current.Epoch, revoked); err != nil {
			t.Fatal(err)
		}

		replay := reservation
		replay.LeaseID = mustLeaseID(t)
		if _, err := store.ReserveLease(context.Background(), replay); !errors.Is(err, statestore.ErrPolicyChanged) {
			t.Fatalf("replay after policy revoke error = %v, want ErrPolicyChanged", err)
		}
	})

	t.Run("concurrent-placements-have-one-winner", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "concurrent-lease")
		a := leaseReservation(t, store, task, "placement-a", leaseNow, leaseNow.Add(time.Minute))
		b := leaseReservation(t, store, task, "placement-b", leaseNow, leaseNow.Add(time.Minute))

		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, reservation := range []statestore.LeaseReservation{a, b} {
			reservation := reservation
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := store.ReserveLease(context.Background(), reservation)
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		var success, active int
		for err := range results {
			switch {
			case err == nil:
				success++
			case errors.Is(err, statestore.ErrLeaseActive):
				active++
			default:
				t.Fatalf("unexpected concurrent reservation error = %v", err)
			}
		}
		if success != 1 || active != 1 {
			t.Fatalf("concurrent results success=%d active=%d, want 1/1", success, active)
		}
	})

	t.Run("lease-cannot-outlive-placement-approval", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "placement-expiry")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		reservation.PlacementExpiresAt = leaseNow.Add(30 * time.Second)
		if _, err := store.ReserveLease(context.Background(), reservation); err == nil {
			t.Fatal("ReserveLease() accepted a Lease that outlives Placement Approval")
		}
	})

	t.Run("missing-task-and-cancelled-context", func(t *testing.T) {
		store := newStore(t)
		taskID := mustTaskID(t)
		task := taskstate.TaskRecord{
			ID:           taskID,
			TaskIRDigest: contract.SHA256([]byte("missing")),
			Phase:        taskstate.TaskPhaseReady,
			Revision:     1,
			CreatedAt:    leaseNow,
			UpdatedAt:    leaseNow,
		}
		reservation := unbackedLeaseReservation(t, task, "placement-missing", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("missing Task error = %v, want ErrNotFound", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := store.ReserveLease(ctx, reservation); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled ReserveLease error = %v", err)
		}
		if _, err := store.LoadLease(ctx, reservation.LeaseID); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled LoadLease error = %v", err)
		}
	})
}

func RunLeaseDurabilityContract(t *testing.T, factory DurableLeaseFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("DurableLeaseFactory.Open is nil")
	}
	t.Run("lease-and-fence-survive-restart", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		task := readyTask(t, store, "lease-durable")
		firstReq := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		first, err := store.ReserveLease(context.Background(), firstReq)
		if err != nil {
			t.Fatal(err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		defer closeReopened()
		got, err := reopened.LoadLease(context.Background(), first.Lease.ID)
		if err != nil {
			t.Fatalf("LoadLease() after restart error = %v", err)
		}
		assertLeaseEqual(t, got, first.Lease)

		assigned, err := reopened.LoadAttempt(context.Background(), firstReq.AttemptID)
		if err != nil {
			t.Fatalf("LoadAttempt() after restart error = %v", err)
		}
		if assigned.Phase != attemptstate.AttemptPhaseAssigned ||
			assigned.LeaseID != first.Lease.ID ||
			assigned.WorkerIdentity != first.Lease.WorkerIdentity {
			t.Fatalf("Attempt/Lease binding changed across restart: %#v", assigned)
		}

		replay := firstReq
		replay.LeaseID = mustLeaseID(t)
		replayed, err := reopened.ReserveLease(context.Background(), replay)
		if err != nil {
			t.Fatalf("ReserveLease() replay after restart error = %v", err)
		}
		if !replayed.Replayed || replayed.Lease.ID != first.Lease.ID ||
			replayed.Lease.FenceEpoch != first.Lease.FenceEpoch {
			t.Fatalf("restart replay did not converge: first=%#v replay=%#v", first, replayed)
		}

		closed := assigned
		closed.Phase = attemptstate.AttemptPhaseClosed
		closed.CloseReason = attemptstate.CloseReasonLeaseExpired
		closed.Revision++
		closed.UpdatedAt = first.Lease.ExpiresAt
		if closed.UpdatedAt.Before(assigned.UpdatedAt) {
			closed.UpdatedAt = assigned.UpdatedAt
		}
		if err := reopened.CompareAndSwapAttempt(context.Background(), assigned.ID, assigned.Revision, closed); err != nil {
			t.Fatalf("close first Attempt after expiry: %v", err)
		}

		secondNow := first.Lease.ExpiresAt.Add(time.Nanosecond)
		secondReq := leaseReservation(t, reopened, task, "placement-2", secondNow, secondNow.Add(time.Minute))
		second, err := reopened.ReserveLease(context.Background(), secondReq)
		if err != nil {
			t.Fatalf("ReserveLease() after restart error = %v", err)
		}
		if second.Lease.FenceEpoch != first.Lease.FenceEpoch+1 {
			t.Fatalf("fence after restart = %d, want %d", second.Lease.FenceEpoch, first.Lease.FenceEpoch+1)
		}
	})
}

// LeaseBypassFactory supports checks that must hold even when a stored row has
// been changed outside the SPI, for example by a direct repair of the database.
// The SPI cannot produce that state, so each adapter provides hooks to create
// it and the contract checks that the binding still rejects the reservation.
type LeaseBypassFactory struct {
	New func(t testing.TB) TaskLeaseStore
	// RebindTaskIRDigest overwrites the stored TaskIR digest of one Task without
	// going through CompareAndSwapTask. It must not change the Task revision.
	// An adapter may remove its own schema guard for this write, because the
	// check under test is the Attempt's binding, which must hold without it.
	RebindTaskIRDigest func(t testing.TB, store TaskLeaseStore, id ids.TaskID, digest contract.Digest)
	// OverwriteAttempt writes the binding columns of record (TaskID,
	// TaskIRDigest, WorkerIdentity and LeaseID) over the stored Attempt with the
	// same ID. It bypasses ValidateTransition and must not change the Attempt
	// Revision or Phase.
	OverwriteAttempt func(t testing.TB, store TaskLeaseStore, record attemptstate.AttemptRecord)
}

func RunLeaseBypassContract(t *testing.T, factory LeaseBypassFactory) {
	t.Helper()
	if factory.New == nil || factory.RebindTaskIRDigest == nil || factory.OverwriteAttempt == nil {
		t.Fatal("LeaseBypassFactory requires New, RebindTaskIRDigest and OverwriteAttempt")
	}

	t.Run("attempt-taskir-binding-rejects-when-task-row-is-rebound", func(t *testing.T) {
		store := factory.New(t)
		task := readyTask(t, store, "bypass-taskir")
		reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))

		// The Task keeps its phase and revision, and the reservation presents the
		// rebound digest, so the Task-level check passes. Only the Attempt, which
		// was created for the original TaskIR, can reject this reservation.
		rebound := contract.SHA256([]byte("bypass-taskir-rebound"))
		factory.RebindTaskIRDigest(t, store, task.ID, rebound)
		reservation.TaskIRDigest = rebound
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("ReserveLease() for Attempt bound to another TaskIR error = %v, want ErrConflict", err)
		}
		assertLeaseNotReserved(t, store, reservation.LeaseID)
	})

	// An exact replay matches the stored Lease on every reserved field, so the
	// Lease-level comparison passes. The only thing left to reject it is the
	// Attempt's own binding to that Lease. Each case corrupts one binding field
	// of the Attempt and checks that the replay is refused. Before the
	// corruption, the same replay must succeed, so the refusal comes from the
	// corrupted field and not from the replay request itself.
	mismatches := map[string]func(t testing.TB, store TaskLeaseStore, attempt *attemptstate.AttemptRecord){
		"lease-id": func(t testing.TB, _ TaskLeaseStore, attempt *attemptstate.AttemptRecord) {
			attempt.LeaseID = mustLeaseID(t)
		},
		"worker-identity": func(t testing.TB, _ TaskLeaseStore, attempt *attemptstate.AttemptRecord) {
			attempt.WorkerIdentity = "worker-b"
		},
		"task-id": func(t testing.TB, store TaskLeaseStore, attempt *attemptstate.AttemptRecord) {
			other := readyTask(t, store, "bypass-attempt-task-id-other")
			attempt.TaskID = other.ID
		},
		"taskir-digest": func(t testing.TB, _ TaskLeaseStore, attempt *attemptstate.AttemptRecord) {
			attempt.TaskIRDigest = contract.SHA256([]byte("bypass-attempt-taskir-other"))
		},
	}
	for name, mutate := range mismatches {
		t.Run("exact-replay-rejects-attempt-with-mismatched-"+name, func(t *testing.T) {
			store := factory.New(t)
			task := readyTask(t, store, "bypass-attempt-"+name)
			reservation := leaseReservation(t, store, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
			if _, err := store.ReserveLease(context.Background(), reservation); err != nil {
				t.Fatal(err)
			}

			control := reservation
			control.LeaseID = mustLeaseID(t)
			if got, err := store.ReserveLease(context.Background(), control); err != nil || !got.Replayed {
				t.Fatalf("control exact replay before corruption = %#v, %v; want Replayed", got, err)
			}

			attempt, err := store.LoadAttempt(context.Background(), reservation.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			mutate(t, store, &attempt)
			factory.OverwriteAttempt(t, store, attempt)

			replay := reservation
			replay.LeaseID = mustLeaseID(t)
			if _, err := store.ReserveLease(context.Background(), replay); !errors.Is(err, statestore.ErrConflict) {
				t.Fatalf("exact replay with Attempt %s mismatch error = %v, want ErrConflict", name, err)
			}
			assertLeaseNotReserved(t, store, replay.LeaseID)
		})
	}
}

var leaseNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func readyTask(t testing.TB, store TaskLeaseStore, seed string) taskstate.TaskRecord {
	t.Helper()
	ensurePolicy(t, store)
	id := mustTaskID(t)
	record, err := taskstate.NewTask(id, contract.SHA256([]byte(seed)), leaseNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	ready := record
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = record.UpdatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, ready); err != nil {
		t.Fatal(err)
	}
	return ready
}

var leaseAttemptSequence atomic.Uint64

func leaseReservation(t testing.TB, store TaskLeaseStore, task taskstate.TaskRecord, decisionID string, issued, expires time.Time) statestore.LeaseReservation {
	t.Helper()

	reservation := unbackedLeaseReservation(t, task, decisionID, issued, expires)
	attempt, err := attemptstate.New(
		reservation.AttemptID,
		task.ID,
		task.TaskIRDigest,
		leaseAttemptSequence.Add(1),
		task.UpdatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	placing := attempt
	placing.Phase = attemptstate.AttemptPhasePlacing
	placing.WorkerIdentity = reservation.WorkerIdentity
	placing.Revision++
	placing.UpdatedAt = attempt.UpdatedAt.Add(time.Nanosecond)
	if err := store.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, placing); err != nil {
		t.Fatal(err)
	}
	reservation.ExpectedAttemptRevision = placing.Revision
	return reservation
}

func unbackedLeaseReservation(t testing.TB, task taskstate.TaskRecord, decisionID string, issued, expires time.Time) statestore.LeaseReservation {
	t.Helper()
	return statestore.LeaseReservation{
		LeaseID:                     mustLeaseID(t),
		TaskID:                      task.ID,
		TaskIRDigest:                task.TaskIRDigest,
		ExpectedTaskRevision:        task.Revision,
		AttemptID:                   mustAttemptID(t),
		ExpectedAttemptRevision:     1,
		WorkerIdentity:              "worker-a",
		PlacementTrustDomain:        "local.example",
		PlacementAuthorityPrincipal: "control-plane/plc-1",
		PlacementDecisionID:         decisionID,
		PlacementPayloadDigest:      contract.SHA256([]byte("payload:" + decisionID)),
		PlacementExpiresAt:          expires.Add(time.Minute),
		PolicyRevision:              "policy-42",
		ExpectedPolicyEpoch:         1,
		IssuedAt:                    issued,
		ExpiresAt:                   expires,
	}
}

func ensurePolicy(t testing.TB, store statestore.PolicyStore) {
	t.Helper()
	_, err := store.LoadCurrentPolicy(context.Background())
	if err == nil {
		return
	}
	if !errors.Is(err, statestore.ErrPolicyUnavailable) {
		t.Fatal(err)
	}
	initial, err := policystate.NewActive(1, "policy-42", leaseNow.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), 0, initial); err != nil {
		t.Fatal(err)
	}
}

func mustAttemptID(t testing.TB) ids.AttemptID {
	t.Helper()
	id, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustLeaseID(t testing.TB) ids.LeaseID {
	t.Helper()
	id, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// assertLeaseNotReserved checks that a rejected reservation left no durable
// Lease behind, so no authority or FenceEpoch was produced by the rejection.
func assertLeaseNotReserved(t testing.TB, store TaskLeaseStore, id ids.LeaseID) {
	t.Helper()
	if _, err := store.LoadLease(context.Background(), id); !errors.Is(err, statestore.ErrNotFound) {
		t.Fatalf("rejected reservation left Lease %s: LoadLease() error = %v, want ErrNotFound", id, err)
	}
}

// advanceAttemptTo moves a held Attempt along the dispatch path one legal
// transition at a time until it reaches target. Each step is a compare-and-swap
// on the Attempt revision, the same way a Worker exchange advances it.
func advanceAttemptTo(t testing.TB, store TaskLeaseStore, id ids.AttemptID, target attemptstate.AttemptPhase) {
	t.Helper()
	for {
		current, err := store.LoadAttempt(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if current.Phase == target {
			return
		}
		next := current
		switch current.Phase {
		case attemptstate.AttemptPhaseAssigned:
			next.Phase = attemptstate.AttemptPhaseDispatched
		case attemptstate.AttemptPhaseDispatched:
			next.Phase = attemptstate.AttemptPhaseClaimed
		case attemptstate.AttemptPhaseClaimed:
			next.Phase = attemptstate.AttemptPhaseVerifying
		default:
			t.Fatalf("cannot advance Attempt from %s to %s", current.Phase, target)
		}
		next.Revision++
		next.UpdatedAt = current.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(context.Background(), id, current.Revision, next); err != nil {
			t.Fatalf("advance Attempt %s -> %s: %v", current.Phase, next.Phase, err)
		}
	}
}

// closeAttemptWith closes an Attempt from its current phase. The Attempt keeps
// its Lease binding, which is the state the replay check must reject.
func closeAttemptWith(t testing.TB, store TaskLeaseStore, id ids.AttemptID, reason attemptstate.CloseReason) {
	t.Helper()
	current, err := store.LoadAttempt(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	closed := current
	closed.Phase = attemptstate.AttemptPhaseClosed
	closed.CloseReason = reason
	closed.Revision++
	closed.UpdatedAt = current.UpdatedAt.Add(time.Second)
	if err := store.CompareAndSwapAttempt(context.Background(), id, current.Revision, closed); err != nil {
		t.Fatalf("close Attempt with %s: %v", reason, err)
	}
}

func assertLeaseEqual(t testing.TB, got, want leasestate.LeaseRecord) {
	t.Helper()
	if got.ID != want.ID ||
		got.TaskID != want.TaskID ||
		got.TaskIRDigest != want.TaskIRDigest ||
		got.AttemptID != want.AttemptID ||
		got.WorkerIdentity != want.WorkerIdentity ||
		got.PlacementTrustDomain != want.PlacementTrustDomain ||
		got.PlacementAuthorityPrincipal != want.PlacementAuthorityPrincipal ||
		got.PlacementDecisionID != want.PlacementDecisionID ||
		got.PlacementPayloadDigest != want.PlacementPayloadDigest ||
		!got.PlacementExpiresAt.Equal(want.PlacementExpiresAt) ||
		got.PolicyRevision != want.PolicyRevision ||
		got.PolicyEpoch != want.PolicyEpoch ||
		got.FenceEpoch != want.FenceEpoch ||
		got.RenewalSequence != want.RenewalSequence ||
		!got.IssuedAt.Equal(want.IssuedAt) ||
		!got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("LeaseRecord mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
