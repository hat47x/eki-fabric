package statestoretest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// StoreFactory returns a fresh store that implements both Task and Admission
// operations.
type StoreFactory func(t testing.TB) statestore.Store

// DurableStoreFactory opens a Store at a location so a test can close and
// reopen it.
type DurableStoreFactory struct {
	Open func(t testing.TB, location string) (store statestore.Store, close func() error)
}

func newAdmission(t testing.TB, principal, key, seed string) statestore.Admission {
	t.Helper()
	return statestore.Admission{
		Task:               newTask(t, seed),
		Principal:          principal,
		IdempotencyKey:     key,
		RequestFingerprint: contract.SHA256([]byte("fingerprint:" + seed)),
		MaxAttempts:        3,
	}
}

func assertAdmissionEqual(t testing.TB, got, want statestore.Admission) {
	t.Helper()
	assertTaskRecordEqual(t, got.Task, want.Task)
	if got.Principal != want.Principal ||
		got.IdempotencyKey != want.IdempotencyKey ||
		got.RequestFingerprint != want.RequestFingerprint ||
		got.MaxAttempts != want.MaxAttempts {
		t.Fatalf("Admission mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

// RunAdmissionContract applies the admission contract: atomic idempotency and
// quota checks, principal-scoped keys, and durable principal binding.
func RunAdmissionContract(t *testing.T, newStore StoreFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("admit-and-load", func(t *testing.T) {
		store := newStore(t)
		a := newAdmission(t, "alice", "key-00000001", "admit-and-load")

		res, err := store.AdmitTask(ctx, a, statestore.AdmitLimit{})
		if err != nil {
			t.Fatalf("AdmitTask() error = %v", err)
		}
		if res.Replayed {
			t.Fatal("first admission reported as a replay")
		}
		assertAdmissionEqual(t, res.Admission, a)

		got, err := store.LoadAdmission(ctx, a.Task.ID)
		if err != nil {
			t.Fatalf("LoadAdmission() error = %v", err)
		}
		assertAdmissionEqual(t, got, a)

		task, err := store.LoadTask(ctx, a.Task.ID)
		if err != nil {
			t.Fatalf("LoadTask() error = %v", err)
		}
		assertTaskRecordEqual(t, task, a.Task)
	})

	t.Run("load-missing", func(t *testing.T) {
		store := newStore(t)
		_, err := store.LoadAdmission(ctx, mustTaskID(t))
		if !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("LoadAdmission() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("replay-converges-on-one-task", func(t *testing.T) {
		store := newStore(t)
		first := newAdmission(t, "alice", "key-00000001", "replay")
		if _, err := store.AdmitTask(ctx, first, statestore.AdmitLimit{}); err != nil {
			t.Fatalf("first AdmitTask() error = %v", err)
		}

		// Same principal, key and fingerprint, but a fresh Task ID: the
		// original Task must win and the new one must not exist.
		again := first
		again.Task = newTask(t, "replay-second-id")
		res, err := store.AdmitTask(ctx, again, statestore.AdmitLimit{})
		if err != nil {
			t.Fatalf("replay AdmitTask() error = %v", err)
		}
		if !res.Replayed {
			t.Fatal("replay was not reported")
		}
		if res.Admission.Task.ID != first.Task.ID {
			t.Fatalf("replay returned Task %s, want the original %s", res.Admission.Task.ID, first.Task.ID)
		}
		if _, err := store.LoadTask(ctx, again.Task.ID); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("replay created a second Task: LoadTask() error = %v", err)
		}
	})

	t.Run("replay-returns-current-task-state", func(t *testing.T) {
		store := newStore(t)
		a := newAdmission(t, "alice", "key-00000001", "replay-current")
		if _, err := store.AdmitTask(ctx, a, statestore.AdmitLimit{}); err != nil {
			t.Fatalf("AdmitTask() error = %v", err)
		}
		next := nextRevision(a.Task, taskstate.TaskPhaseReady)
		if err := store.CompareAndSwapTask(ctx, a.Task.ID, a.Task.Revision, next); err != nil {
			t.Fatalf("CompareAndSwapTask() error = %v", err)
		}

		res, err := store.AdmitTask(ctx, a, statestore.AdmitLimit{})
		if err != nil {
			t.Fatalf("replay AdmitTask() error = %v", err)
		}
		assertTaskRecordEqual(t, res.Admission.Task, next)
	})

	t.Run("same-key-different-fingerprint-conflicts", func(t *testing.T) {
		store := newStore(t)
		first := newAdmission(t, "alice", "key-00000001", "conflict-a")
		if _, err := store.AdmitTask(ctx, first, statestore.AdmitLimit{}); err != nil {
			t.Fatalf("first AdmitTask() error = %v", err)
		}
		other := newAdmission(t, "alice", "key-00000001", "conflict-b")
		_, err := store.AdmitTask(ctx, other, statestore.AdmitLimit{})
		if !errors.Is(err, statestore.ErrIdempotencyConflict) {
			t.Fatalf("AdmitTask() error = %v, want ErrIdempotencyConflict", err)
		}
		if _, err := store.LoadTask(ctx, other.Task.ID); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("a conflicting admission created a Task: %v", err)
		}
	})

	t.Run("keys-are-scoped-to-the-principal", func(t *testing.T) {
		store := newStore(t)
		alice := newAdmission(t, "alice", "key-00000001", "scope-alice")
		bob := newAdmission(t, "bob", "key-00000001", "scope-bob")
		for _, a := range []statestore.Admission{alice, bob} {
			res, err := store.AdmitTask(ctx, a, statestore.AdmitLimit{})
			if err != nil {
				t.Fatalf("AdmitTask(%s) error = %v", a.Principal, err)
			}
			if res.Replayed {
				t.Fatalf("principal %s collided with another principal's key", a.Principal)
			}
		}
		for _, a := range []statestore.Admission{alice, bob} {
			got, err := store.LoadAdmission(ctx, a.Task.ID)
			if err != nil {
				t.Fatalf("LoadAdmission() error = %v", err)
			}
			if got.Principal != a.Principal {
				t.Fatalf("Task %s bound to %q, want %q", a.Task.ID, got.Principal, a.Principal)
			}
		}
	})

	t.Run("quota-caps-outstanding-tasks", func(t *testing.T) {
		store := newStore(t)
		limit := statestore.AdmitLimit{MaxOutstandingTasks: 2}
		for i := 0; i < 2; i++ {
			a := newAdmission(t, "alice", fmt.Sprintf("key-quota-%03d", i), fmt.Sprintf("quota-%d", i))
			if _, err := store.AdmitTask(ctx, a, limit); err != nil {
				t.Fatalf("AdmitTask(%d) error = %v", i, err)
			}
		}
		over := newAdmission(t, "alice", "key-quota-999", "quota-over")
		_, err := store.AdmitTask(ctx, over, limit)
		if !errors.Is(err, statestore.ErrQuotaExceeded) {
			t.Fatalf("AdmitTask() error = %v, want ErrQuotaExceeded", err)
		}
		if _, err := store.LoadTask(ctx, over.Task.ID); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("a rejected admission created a Task: %v", err)
		}

		other := newAdmission(t, "bob", "key-quota-000", "quota-bob")
		if _, err := store.AdmitTask(ctx, other, limit); err != nil {
			t.Fatalf("another principal was limited by alice's quota: %v", err)
		}
	})

	t.Run("replay-is-not-limited-by-quota", func(t *testing.T) {
		store := newStore(t)
		limit := statestore.AdmitLimit{MaxOutstandingTasks: 1}
		a := newAdmission(t, "alice", "key-00000001", "replay-quota")
		if _, err := store.AdmitTask(ctx, a, limit); err != nil {
			t.Fatalf("AdmitTask() error = %v", err)
		}
		res, err := store.AdmitTask(ctx, a, limit)
		if err != nil {
			t.Fatalf("replay at the quota limit error = %v", err)
		}
		if !res.Replayed {
			t.Fatal("replay was not reported")
		}
	})

	t.Run("terminal-tasks-free-quota", func(t *testing.T) {
		store := newStore(t)
		limit := statestore.AdmitLimit{MaxOutstandingTasks: 1}
		first := newAdmission(t, "alice", "key-00000001", "terminal-first")
		if _, err := store.AdmitTask(ctx, first, limit); err != nil {
			t.Fatalf("AdmitTask() error = %v", err)
		}
		second := newAdmission(t, "alice", "key-00000002", "terminal-second")
		if _, err := store.AdmitTask(ctx, second, limit); !errors.Is(err, statestore.ErrQuotaExceeded) {
			t.Fatalf("AdmitTask() error = %v, want ErrQuotaExceeded", err)
		}

		done := nextRevision(first.Task, taskstate.TaskPhaseTerminal)
		done.TerminalOutcome = taskstate.TerminalOutcomeCompleted
		if err := store.CompareAndSwapTask(ctx, first.Task.ID, first.Task.Revision, done); err != nil {
			t.Fatalf("CompareAndSwapTask() error = %v", err)
		}
		if _, err := store.AdmitTask(ctx, second, limit); err != nil {
			t.Fatalf("AdmitTask() after the first Task finished error = %v", err)
		}
	})

	t.Run("invalid-admissions-are-rejected", func(t *testing.T) {
		store := newStore(t)
		cases := map[string]func(*statestore.Admission){
			"empty principal":     func(a *statestore.Admission) { a.Principal = "" },
			"oversized principal": func(a *statestore.Admission) { a.Principal = string(make([]byte, statestore.MaxPrincipalBytes+1)) },
			"short key":           func(a *statestore.Admission) { a.IdempotencyKey = "short" },
			"long key": func(a *statestore.Admission) {
				a.IdempotencyKey = string(make([]byte, statestore.MaxIdempotencyKeyBytes+1))
			},
			"key with a space":  func(a *statestore.Admission) { a.IdempotencyKey = "key with space" },
			"zero fingerprint":  func(a *statestore.Admission) { a.RequestFingerprint = contract.Digest{} },
			"zero attempts":     func(a *statestore.Admission) { a.MaxAttempts = 0 },
			"too many attempts": func(a *statestore.Admission) { a.MaxAttempts = statestore.MaxAdmissionAttempts + 1 },
			"invalid task":      func(a *statestore.Admission) { a.Task.Revision = 0 },
		}
		for name, mutate := range cases {
			a := newAdmission(t, "alice", "key-00000001", "invalid-"+name)
			mutate(&a)
			if _, err := store.AdmitTask(ctx, a, statestore.AdmitLimit{}); err == nil {
				t.Errorf("%s: admission was accepted", name)
			}
		}
	})

	t.Run("duplicate-task-id-with-a-new-key-is-rejected", func(t *testing.T) {
		store := newStore(t)
		first := newAdmission(t, "alice", "key-00000001", "dup-id")
		if _, err := store.AdmitTask(ctx, first, statestore.AdmitLimit{}); err != nil {
			t.Fatalf("AdmitTask() error = %v", err)
		}
		clash := newAdmission(t, "alice", "key-00000002", "dup-id-2")
		clash.Task = first.Task
		_, err := store.AdmitTask(ctx, clash, statestore.AdmitLimit{})
		if !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("AdmitTask() error = %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("concurrent-same-key-creates-one-task", func(t *testing.T) {
		store := newStore(t)
		const workers = 12
		fingerprint := contract.SHA256([]byte("concurrent-same-key"))

		var wg sync.WaitGroup
		start := make(chan struct{})
		type outcome struct {
			res statestore.AdmitResult
			err error
		}
		outcomes := make(chan outcome, workers)
		for i := 0; i < workers; i++ {
			a := newAdmission(t, "alice", "key-concurrent", fmt.Sprintf("concurrent-same-%d", i))
			a.RequestFingerprint = fingerprint
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				res, err := store.AdmitTask(ctx, a, statestore.AdmitLimit{})
				outcomes <- outcome{res, err}
			}()
		}
		close(start)
		wg.Wait()
		close(outcomes)

		ids := map[string]int{}
		replays := 0
		for o := range outcomes {
			if o.err != nil {
				t.Fatalf("AdmitTask() error = %v", o.err)
			}
			ids[o.res.Admission.Task.ID.String()]++
			if o.res.Replayed {
				replays++
			}
		}
		if len(ids) != 1 {
			t.Fatalf("concurrent submissions created %d Tasks, want 1: %v", len(ids), ids)
		}
		if replays != workers-1 {
			t.Fatalf("replays = %d, want %d", replays, workers-1)
		}
	})

	t.Run("concurrent-admissions-cannot-overshoot-quota", func(t *testing.T) {
		store := newStore(t)
		const workers = 12
		limit := statestore.AdmitLimit{MaxOutstandingTasks: 3}

		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan error, workers)
		for i := 0; i < workers; i++ {
			a := newAdmission(t, "alice", fmt.Sprintf("key-race-%04d", i), fmt.Sprintf("race-%d", i))
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := store.AdmitTask(ctx, a, limit)
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		admitted, rejected := 0, 0
		for err := range results {
			switch {
			case err == nil:
				admitted++
			case errors.Is(err, statestore.ErrQuotaExceeded):
				rejected++
			default:
				t.Fatalf("unexpected error = %v", err)
			}
		}
		if admitted != int(limit.MaxOutstandingTasks) || rejected != workers-admitted {
			t.Fatalf("admitted=%d rejected=%d, want exactly %d admitted", admitted, rejected, limit.MaxOutstandingTasks)
		}
	})

	t.Run("cancelled-context", func(t *testing.T) {
		store := newStore(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		a := newAdmission(t, "alice", "key-00000001", "cancelled")
		if _, err := store.AdmitTask(cctx, a, statestore.AdmitLimit{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("AdmitTask() error = %v, want context.Canceled", err)
		}
		if _, err := store.LoadAdmission(cctx, a.Task.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("LoadAdmission() error = %v, want context.Canceled", err)
		}
	})
}

// RunAdmissionDurabilityContract checks that admissions, idempotency keys and
// quota usage survive closing and reopening the store.
func RunAdmissionDurabilityContract(t *testing.T, factory DurableStoreFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("DurableStoreFactory.Open is nil")
	}
	ctx := context.Background()

	t.Run("admission-and-idempotency-survive-reopen", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		a := newAdmission(t, "alice", "key-00000001", "durable-admission")

		store, closeStore := factory.Open(t, location)
		if _, err := store.AdmitTask(ctx, a, statestore.AdmitLimit{}); err != nil {
			t.Fatalf("AdmitTask() error = %v", err)
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

		got, err := reopened.LoadAdmission(ctx, a.Task.ID)
		if err != nil {
			t.Fatalf("LoadAdmission() after reopen error = %v", err)
		}
		assertAdmissionEqual(t, got, a)

		// A client retrying after the control plane restarted must converge on
		// the same Task, not create a second one.
		retry := a
		retry.Task = newTask(t, "durable-admission-retry")
		res, err := reopened.AdmitTask(ctx, retry, statestore.AdmitLimit{})
		if err != nil {
			t.Fatalf("retry after reopen error = %v", err)
		}
		if !res.Replayed || res.Admission.Task.ID != a.Task.ID {
			t.Fatalf("retry after reopen created Task %s (replayed=%v), want %s", res.Admission.Task.ID, res.Replayed, a.Task.ID)
		}
	})

	t.Run("quota-usage-survives-reopen", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		limit := statestore.AdmitLimit{MaxOutstandingTasks: 1}

		store, closeStore := factory.Open(t, location)
		if _, err := store.AdmitTask(ctx, newAdmission(t, "alice", "key-00000001", "durable-quota-1"), limit); err != nil {
			t.Fatalf("AdmitTask() error = %v", err)
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
		_, err := reopened.AdmitTask(ctx, newAdmission(t, "alice", "key-00000002", "durable-quota-2"), limit)
		if !errors.Is(err, statestore.ErrQuotaExceeded) {
			t.Fatalf("AdmitTask() after reopen error = %v, want ErrQuotaExceeded", err)
		}
	})
}
