package statestoretest

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type TaskAttemptStore interface {
	statestore.TaskStore
	statestore.AttemptStore
}

type AttemptFactory func(t testing.TB) TaskAttemptStore

type DurableAttemptFactory struct {
	Open func(t testing.TB, location string) (store TaskAttemptStore, close func() error)
}

func RunAttemptStoreContract(t *testing.T, newStore AttemptFactory) {
	t.Helper()

	runAttemptListingContract(t, newStore)

	t.Run("create-load", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-create-load")
		attempt := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), attempt); err != nil {
			t.Fatalf("CreateAttempt() error = %v", err)
		}
		got, err := store.LoadAttempt(context.Background(), attempt.ID)
		if err != nil {
			t.Fatalf("LoadAttempt() error = %v", err)
		}
		assertAttemptEqual(t, got, attempt)
	})

	t.Run("missing", func(t *testing.T) {
		store := newStore(t)
		if _, err := store.LoadAttempt(context.Background(), mustAttemptID(t)); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("LoadAttempt() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("parent-task-required", func(t *testing.T) {
		store := newStore(t)
		task := attemptParentTask(t, "missing-attempt-parent")
		attempt := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), attempt); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("CreateAttempt() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("task-ir-must-match-parent", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-parent-digest")
		attempt := newAttempt(t, task, 1)
		attempt.TaskIRDigest = contract.SHA256([]byte("different"))
		if err := store.CreateAttempt(context.Background(), attempt); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("CreateAttempt() error = %v, want ErrConflict", err)
		}
	})

	t.Run("initial-state-is-required", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-initial-state")
		attempt := newAttempt(t, task, 1)
		attempt.Phase = attemptstate.AttemptPhasePlacing
		attempt.WorkerIdentity = "worker-a"
		if err := store.CreateAttempt(context.Background(), attempt); !errors.Is(err, attemptstate.ErrInvalidAttemptRecord) {
			t.Fatalf("CreateAttempt() error = %v, want ErrInvalidAttemptRecord", err)
		}
	})

	t.Run("parent-task-must-be-progressable", func(t *testing.T) {
		for _, phase := range []taskstate.TaskPhase{taskstate.TaskPhaseAdmitted, taskstate.TaskPhaseHeld, taskstate.TaskPhaseTerminal} {
			t.Run(string(phase), func(t *testing.T) {
				store := newStore(t)
				task := attemptParentTask(t, "attempt-parent-"+string(phase))
				if err := store.CreateTask(context.Background(), task); err != nil {
					t.Fatal(err)
				}
				// Creation is admitted-only now (#365); the phase under test
				// is a separate write.
				task.Phase = phase
				if phase == taskstate.TaskPhaseTerminal {
					task.TerminalOutcome = taskstate.TerminalOutcomeCancelled
				}
				task.Revision++
				task.UpdatedAt = task.CreatedAt.Add(time.Second)
				if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision-1, task); err != nil {
					t.Fatalf("move Task to %s: %v", phase, err)
				}
				attempt := newAttempt(t, task, 1)
				if err := store.CreateAttempt(context.Background(), attempt); !errors.Is(err, statestore.ErrConflict) {
					t.Fatalf("CreateAttempt() error = %v, want ErrConflict", err)
				}
			})
		}

		store := newStore(t)
		task := attemptParentTask(t, "attempt-parent-cancelled")
		task.CancellationRequested = true
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		attempt := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), attempt); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("CreateAttempt() on cancelled Task error = %v, want ErrConflict", err)
		}
	})

	t.Run("duplicate-id", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-duplicate-id")
		attempt := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), attempt); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateAttempt(context.Background(), attempt); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("duplicate CreateAttempt() error = %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("attempt-number-is-unique-per-task", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-number")
		first := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		second := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), second); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("duplicate attempt number error = %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("compare-and-swap", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-cas")
		current := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), current); err != nil {
			t.Fatal(err)
		}
		next := placingAttempt(current, "worker-a")
		if err := store.CompareAndSwapAttempt(context.Background(), current.ID, current.Revision, next); err != nil {
			t.Fatalf("CompareAndSwapAttempt() error = %v", err)
		}
		got, err := store.LoadAttempt(context.Background(), current.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertAttemptEqual(t, got, next)
	})

	t.Run("stale-revision", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-stale")
		current := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), current); err != nil {
			t.Fatal(err)
		}
		next := placingAttempt(current, "worker-a")
		if err := store.CompareAndSwapAttempt(context.Background(), current.ID, current.Revision, next); err != nil {
			t.Fatal(err)
		}
		stale := placingAttempt(current, "worker-b")
		if err := store.CompareAndSwapAttempt(context.Background(), current.ID, current.Revision, stale); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("stale CompareAndSwapAttempt() error = %v, want ErrConflict", err)
		}
	})

	t.Run("closed-attempt-cannot-reopen", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-closed")
		current := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), current); err != nil {
			t.Fatal(err)
		}
		closed := current
		closed.Phase = attemptstate.AttemptPhaseClosed
		closed.CloseReason = attemptstate.CloseReasonCancelled
		closed.Revision++
		closed.UpdatedAt = closed.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(context.Background(), current.ID, current.Revision, closed); err != nil {
			t.Fatal(err)
		}

		reopen := closed
		reopen.Phase = attemptstate.AttemptPhasePlacing
		reopen.CloseReason = attemptstate.CloseReasonNone
		reopen.WorkerIdentity = "worker-a"
		reopen.Revision++
		reopen.UpdatedAt = reopen.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(context.Background(), current.ID, closed.Revision, reopen); !errors.Is(err, attemptstate.ErrInvalidAttemptTransition) {
			t.Fatalf("reopen error = %v, want ErrInvalidAttemptTransition", err)
		}
	})

	t.Run("concurrent-cas-has-single-winner", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-concurrent")
		current := newAttempt(t, task, 1)
		if err := store.CreateAttempt(context.Background(), current); err != nil {
			t.Fatal(err)
		}
		a := placingAttempt(current, "worker-a")
		b := placingAttempt(current, "worker-b")

		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, next := range []attemptstate.AttemptRecord{a, b} {
			next := next
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results <- store.CompareAndSwapAttempt(context.Background(), current.ID, current.Revision, next)
			}()
		}
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
				t.Fatalf("unexpected concurrent CAS error = %v", err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("concurrent CAS success=%d conflict=%d, want 1/1", success, conflict)
		}
	})

	t.Run("cancelled-context", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-cancelled-context")
		attempt := newAttempt(t, task, 1)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := store.CreateAttempt(ctx, attempt); !errors.Is(err, context.Canceled) {
			t.Fatalf("CreateAttempt() error = %v, want context.Canceled", err)
		}
		if _, err := store.LoadAttempt(ctx, attempt.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("LoadAttempt() error = %v, want context.Canceled", err)
		}
		if err := store.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, placingAttempt(attempt, "worker-a")); !errors.Is(err, context.Canceled) {
			t.Fatalf("CompareAndSwapAttempt() error = %v, want context.Canceled", err)
		}
	})
}

func RunAttemptDurabilityContract(t *testing.T, factory DurableAttemptFactory) {
	t.Helper()
	location := filepath.Join(t.TempDir(), "state.db")
	store, closeStore := factory.Open(t, location)
	task := readyParentTask(t, store, "attempt-durable")
	attempt := newAttempt(t, task, 1)
	if err := store.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	placing := placingAttempt(attempt, "worker-a")
	if err := store.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, placing); err != nil {
		t.Fatal(err)
	}
	if err := closeStore(); err != nil {
		t.Fatal(err)
	}

	reopened, closeReopened := factory.Open(t, location)
	defer closeReopened()
	got, err := reopened.LoadAttempt(context.Background(), attempt.ID)
	if err != nil {
		t.Fatalf("LoadAttempt() after restart error = %v", err)
	}
	assertAttemptEqual(t, got, placing)
}

func attemptParentTask(t testing.TB, seed string) taskstate.TaskRecord {
	t.Helper()
	return newTask(t, seed)
}

// readyParentTask moves a created Task to ready, the way dispatch preparation
// would — a Task is created admitted (#365), so the ready phase needs its own
// write.
func readyParentTask(t testing.TB, store interface {
	CreateTask(ctx context.Context, record taskstate.TaskRecord) error
	LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error)
	CompareAndSwapTask(ctx context.Context, id ids.TaskID, expectedRevision uint64, next taskstate.TaskRecord) error
}, seed string) taskstate.TaskRecord {
	t.Helper()
	task := newTask(t, seed)
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.CreatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}
	return ready
}

func newAttempt(t testing.TB, task taskstate.TaskRecord, number uint64) attemptstate.AttemptRecord {
	t.Helper()
	record, err := attemptstate.New(mustAttemptID(t), task.ID, task.TaskIRDigest, number, task.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func placingAttempt(current attemptstate.AttemptRecord, worker string) attemptstate.AttemptRecord {
	next := current
	next.Phase = attemptstate.AttemptPhasePlacing
	next.WorkerIdentity = worker
	next.Revision++
	next.UpdatedAt = current.UpdatedAt.Add(time.Second)
	return next
}

func assertAttemptEqual(t testing.TB, got, want attemptstate.AttemptRecord) {
	t.Helper()
	if got.ID != want.ID ||
		got.TaskID != want.TaskID ||
		got.TaskIRDigest != want.TaskIRDigest ||
		got.Number != want.Number ||
		got.Phase != want.Phase ||
		got.WorkerIdentity != want.WorkerIdentity ||
		got.LeaseID != want.LeaseID ||
		got.CloseReason != want.CloseReason ||
		got.Revision != want.Revision ||
		!got.CreatedAt.Equal(want.CreatedAt) ||
		!got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("AttemptRecord mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
