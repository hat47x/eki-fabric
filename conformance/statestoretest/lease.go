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
			assigned.WorkerIdentity != first.Lease.WorkerIdentity {
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

		tests := map[string]func(*statestore.LeaseReservation){
			"task": func(r *statestore.LeaseReservation) {
				r.TaskID = otherTask.ID
				r.TaskIRDigest = otherTask.TaskIRDigest
				r.ExpectedTaskRevision = otherTask.Revision
			},
			"attempt":          func(r *statestore.LeaseReservation) { r.AttemptID = mustAttemptID(t) },
			"worker":           func(r *statestore.LeaseReservation) { r.WorkerIdentity = "worker-b" },
			"policy":           func(r *statestore.LeaseReservation) { r.PolicyRevision = "policy-43" },
			"placement-expiry": func(r *statestore.LeaseReservation) { r.PlacementExpiresAt = r.PlacementExpiresAt.Add(time.Second) },
		}
		for name, mutate := range tests {
			t.Run(name, func(t *testing.T) {
				replay := first
				replay.LeaseID = mustLeaseID(t)
				mutate(&replay)
				if _, err := store.ReserveLease(context.Background(), replay); !errors.Is(err, statestore.ErrPlacementDecisionConflict) {
					t.Fatalf("ReserveLease() error = %v, want ErrPlacementDecisionConflict", err)
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
		// Create the Attempt while the Task is still schedulable: the adapters refuse
		// new Attempts for a held Task, so the Hold must come after the Attempt exists.
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
