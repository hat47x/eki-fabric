package statestoretest

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type TaskLeaseStore interface {
	statestore.TaskStore
	statestore.LeaseStore
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
		reservation := leaseReservation(t, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))

		first, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatalf("ReserveLease() error = %v", err)
		}
		if first.Replayed || first.Lease.FenceEpoch != 1 || first.Lease.RenewalSequence != 0 {
			t.Fatalf("first reservation = %#v", first)
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

	t.Run("decision-id-cannot-be-rebound", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "decision-rebind")
		first := leaseReservation(t, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
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
		first := leaseReservation(t, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), first); err != nil {
			t.Fatal(err)
		}

		second := leaseReservation(t, task, "placement-2", leaseNow.Add(10*time.Second), leaseNow.Add(70*time.Second))
		if _, err := store.ReserveLease(context.Background(), second); !errors.Is(err, statestore.ErrLeaseActive) {
			t.Fatalf("ReserveLease() error = %v, want ErrLeaseActive", err)
		}
	})

	t.Run("future-issued-lease-still-reserves-the-fence", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "future-fence")
		future := leaseReservation(t, task, "placement-future", leaseNow.Add(30*time.Second), leaseNow.Add(90*time.Second))
		if _, err := store.ReserveLease(context.Background(), future); err != nil {
			t.Fatal(err)
		}

		second := leaseReservation(t, task, "placement-now", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), second); !errors.Is(err, statestore.ErrLeaseActive) {
			t.Fatalf("ReserveLease() error = %v, want ErrLeaseActive", err)
		}
	})

	t.Run("expired-lease-allows-next-monotonic-fence", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "fence")
		first := leaseReservation(t, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		a, err := store.ReserveLease(context.Background(), first)
		if err != nil {
			t.Fatal(err)
		}

		secondNow := first.ExpiresAt.Add(time.Nanosecond)
		second := leaseReservation(t, task, "placement-2", secondNow, secondNow.Add(time.Minute))
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
		reservation := leaseReservation(t, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))

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

	t.Run("cancellation-fails-closed", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "cancel")
		reservation := leaseReservation(t, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))

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
		held := task
		held.Phase = taskstate.TaskPhaseHeld
		held.Revision++
		held.UpdatedAt = task.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, held); err != nil {
			t.Fatal(err)
		}
		reservation := leaseReservation(t, held, "placement-1", leaseNow, leaseNow.Add(time.Minute))
		if _, err := store.ReserveLease(context.Background(), reservation); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("ReserveLease() error = %v, want ErrConflict", err)
		}
	})

	t.Run("concurrent-placements-have-one-winner", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "concurrent-lease")
		a := leaseReservation(t, task, "placement-a", leaseNow, leaseNow.Add(time.Minute))
		b := leaseReservation(t, task, "placement-b", leaseNow, leaseNow.Add(time.Minute))

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
		reservation := leaseReservation(t, task, "placement-missing", leaseNow, leaseNow.Add(time.Minute))
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
		firstReq := leaseReservation(t, task, "placement-1", leaseNow, leaseNow.Add(time.Minute))
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

		secondNow := first.Lease.ExpiresAt.Add(time.Nanosecond)
		secondReq := leaseReservation(t, task, "placement-2", secondNow, secondNow.Add(time.Minute))
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

func readyTask(t testing.TB, store statestore.TaskStore, seed string) taskstate.TaskRecord {
	t.Helper()
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

func leaseReservation(t testing.TB, task taskstate.TaskRecord, decisionID string, issued, expires time.Time) statestore.LeaseReservation {
	t.Helper()
	return statestore.LeaseReservation{
		LeaseID:                mustLeaseID(t),
		TaskID:                 task.ID,
		TaskIRDigest:           task.TaskIRDigest,
		ExpectedTaskRevision:   task.Revision,
		AttemptID:              mustAttemptID(t),
		WorkerIdentity:         "worker-a",
		PlacementDecisionID:    decisionID,
		PlacementPayloadDigest: contract.SHA256([]byte("payload:" + decisionID)),
		PolicyRevision:         "policy-42",
		IssuedAt:               issued,
		ExpiresAt:              expires,
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
		got.PlacementDecisionID != want.PlacementDecisionID ||
		got.PlacementPayloadDigest != want.PlacementPayloadDigest ||
		got.PolicyRevision != want.PolicyRevision ||
		got.FenceEpoch != want.FenceEpoch ||
		got.RenewalSequence != want.RenewalSequence ||
		!got.IssuedAt.Equal(want.IssuedAt) ||
		!got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("LeaseRecord mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
