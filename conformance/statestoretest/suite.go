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
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type Factory func(t testing.TB) statestore.TaskStore

type DurableFactory struct {
	Open func(t testing.TB, location string) (store statestore.TaskStore, close func() error)
}

func RunTaskStoreContract(t *testing.T, newStore Factory) {
	t.Helper()

	runTaskListingContract(t, newStore)

	t.Run("create-load", func(t *testing.T) {
		store := newStore(t)
		record := newTask(t, "create-load")

		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}

		got, err := store.LoadTask(context.Background(), record.ID)
		if err != nil {
			t.Fatalf("LoadTask() error = %v", err)
		}
		assertTaskRecordEqual(t, got, record)
	})

	t.Run("missing", func(t *testing.T) {
		store := newStore(t)
		id := mustTaskID(t)

		_, err := store.LoadTask(context.Background(), id)
		if !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("LoadTask() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("duplicate-create", func(t *testing.T) {
		store := newStore(t)
		record := newTask(t, "duplicate-create")

		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("first CreateTask() error = %v", err)
		}
		err := store.CreateTask(context.Background(), record)
		if !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("second CreateTask() error = %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("compare-and-swap", func(t *testing.T) {
		store := newStore(t)
		record := newTask(t, "compare-and-swap")
		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}

		next := nextRevision(record, taskstate.TaskPhaseReady)
		if err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, next); err != nil {
			t.Fatalf("CompareAndSwapTask() error = %v", err)
		}

		got, err := store.LoadTask(context.Background(), record.ID)
		if err != nil {
			t.Fatalf("LoadTask() error = %v", err)
		}
		assertTaskRecordEqual(t, got, next)
	})

	t.Run("stale-revision", func(t *testing.T) {
		store := newStore(t)
		record := newTask(t, "stale-revision")
		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}

		next := nextRevision(record, taskstate.TaskPhaseReady)
		if err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, next); err != nil {
			t.Fatalf("first CompareAndSwapTask() error = %v", err)
		}

		stale := nextRevision(record, taskstate.TaskPhaseActive)
		err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, stale)
		if !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("stale CompareAndSwapTask() error = %v, want ErrConflict", err)
		}
	})

	t.Run("wrong-next-id", func(t *testing.T) {
		store := newStore(t)
		record := newTask(t, "wrong-next-id")
		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}

		next := nextRevision(record, taskstate.TaskPhaseReady)
		next.ID = mustTaskID(t)
		err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, next)
		if !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("CompareAndSwapTask() error = %v, want ErrConflict", err)
		}
	})

	t.Run("taskir-digest-is-immutable", func(t *testing.T) {
		// The TaskIR digest is the content binding that Attempts and Leases
		// copy at creation. A Task must not be rebound to another TaskIR by a
		// state transition, or previously created Attempts would silently
		// point at a different workload.
		store := newStore(t)
		record := newTask(t, "taskir-immutable")
		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}

		rebound := nextRevision(record, taskstate.TaskPhaseReady)
		rebound.TaskIRDigest = contract.SHA256([]byte("taskir-immutable-rebound"))
		err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, rebound)
		if !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("CompareAndSwapTask() rebinding TaskIR error = %v, want ErrConflict", err)
		}

		got, err := store.LoadTask(context.Background(), record.ID)
		if err != nil {
			t.Fatalf("LoadTask() error = %v", err)
		}
		assertTaskRecordEqual(t, got, record)
	})

	t.Run("revision-must-increment-once", func(t *testing.T) {
		store := newStore(t)
		record := newTask(t, "revision-must-increment-once")
		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}

		next := record
		next.Phase = taskstate.TaskPhaseReady
		next.Revision = record.Revision + 2
		next.UpdatedAt = record.UpdatedAt.Add(time.Second)

		err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, next)
		if !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("CompareAndSwapTask() error = %v, want ErrConflict", err)
		}
	})

	t.Run("concurrent-cas-has-single-winner", func(t *testing.T) {
		store := newStore(t)
		record := newTask(t, "concurrent-cas")
		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}

		ready := nextRevision(record, taskstate.TaskPhaseReady)
		active := nextRevision(record, taskstate.TaskPhaseActive)

		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, next := range []taskstate.TaskRecord{ready, active} {
			next := next
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results <- store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, next)
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		var successes, conflicts int
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, statestore.ErrConflict):
				conflicts++
			default:
				t.Fatalf("unexpected CAS error = %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("CAS results: successes=%d conflicts=%d, want 1/1", successes, conflicts)
		}

		got, err := store.LoadTask(context.Background(), record.ID)
		if err != nil {
			t.Fatalf("LoadTask() error = %v", err)
		}
		if got.Revision != record.Revision+1 {
			t.Fatalf("stored revision = %d, want %d", got.Revision, record.Revision+1)
		}
	})

	t.Run("invalid-record", func(t *testing.T) {
		store := newStore(t)
		record := newTask(t, "invalid-record")
		record.TaskIRDigest = contract.Digest{}

		err := store.CreateTask(context.Background(), record)
		if !errors.Is(err, taskstate.ErrInvalidTaskRecord) {
			t.Fatalf("CreateTask() error = %v, want ErrInvalidTaskRecord", err)
		}
	})

	t.Run("cancelled-context", func(t *testing.T) {
		store := newStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		record := newTask(t, "cancelled-context")
		if err := store.CreateTask(ctx, record); !errors.Is(err, context.Canceled) {
			t.Fatalf("CreateTask() error = %v, want context.Canceled", err)
		}
		if _, err := store.LoadTask(ctx, record.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("LoadTask() error = %v, want context.Canceled", err)
		}
		next := nextRevision(record, taskstate.TaskPhaseReady)
		if err := store.CompareAndSwapTask(ctx, record.ID, record.Revision, next); !errors.Is(err, context.Canceled) {
			t.Fatalf("CompareAndSwapTask() error = %v, want context.Canceled", err)
		}
	})
}

func RunDurabilityContract(t *testing.T, factory DurableFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("DurableFactory.Open is nil")
	}

	t.Run("survives-close-and-reopen", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		record := newTask(t, "durable-reopen")

		store, closeStore := factory.Open(t, location)
		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		if err := closeStore(); err != nil {
			t.Fatalf("close first store: %v", err)
		}

		reopened, closeReopened := factory.Open(t, location)
		t.Cleanup(func() {
			if err := closeReopened(); err != nil {
				t.Errorf("close reopened store: %v", err)
			}
		})

		got, err := reopened.LoadTask(context.Background(), record.ID)
		if err != nil {
			t.Fatalf("LoadTask() after reopen error = %v", err)
		}
		assertTaskRecordEqual(t, got, record)
	})

	t.Run("revision-survives-reopen", func(t *testing.T) {
		location := t.TempDir() + "/state.db"
		record := newTask(t, "revision-reopen")

		store, closeStore := factory.Open(t, location)
		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		next := nextRevision(record, taskstate.TaskPhaseReady)
		if err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, next); err != nil {
			t.Fatalf("CompareAndSwapTask() error = %v", err)
		}
		if err := closeStore(); err != nil {
			t.Fatalf("close first store: %v", err)
		}

		reopened, closeReopened := factory.Open(t, location)
		t.Cleanup(func() {
			if err := closeReopened(); err != nil {
				t.Errorf("close reopened store: %v", err)
			}
		})

		got, err := reopened.LoadTask(context.Background(), record.ID)
		if err != nil {
			t.Fatalf("LoadTask() after reopen error = %v", err)
		}
		assertTaskRecordEqual(t, got, next)

		stale := nextRevision(record, taskstate.TaskPhaseActive)
		err = reopened.CompareAndSwapTask(context.Background(), record.ID, record.Revision, stale)
		if !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("stale CAS after reopen error = %v, want ErrConflict", err)
		}
	})
}

func newTask(t testing.TB, seed string) taskstate.TaskRecord {
	t.Helper()
	id := mustTaskID(t)
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	record, err := taskstate.NewTask(id, contract.SHA256([]byte(seed)), now)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	return record
}

func mustTaskID(t testing.TB) ids.TaskID {
	t.Helper()
	id, err := ids.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() error = %v", err)
	}
	return id
}

func nextRevision(record taskstate.TaskRecord, phase taskstate.TaskPhase) taskstate.TaskRecord {
	next := record
	next.Phase = phase
	next.Revision = record.Revision + 1
	next.UpdatedAt = record.UpdatedAt.Add(time.Second)
	return next
}

func assertTaskRecordEqual(t testing.TB, got, want taskstate.TaskRecord) {
	t.Helper()
	if got.ID != want.ID ||
		got.TaskIRDigest != want.TaskIRDigest ||
		got.Phase != want.Phase ||
		got.TerminalOutcome != want.TerminalOutcome ||
		got.CancellationRequested != want.CancellationRequested ||
		got.Revision != want.Revision ||
		!got.CreatedAt.Equal(want.CreatedAt) ||
		!got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("TaskRecord mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
