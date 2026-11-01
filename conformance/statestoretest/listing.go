package statestoretest

import (
	"context"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

// runTaskListingContract fixes what the dispatch loop reads: the admitted
// Tasks in arrival order, oldest creation first, at most limit of them. A
// Task that left the phase stops being listed the moment its CAS lands, so a
// dispatch tick cannot see the same work twice through this query.
func runTaskListingContract(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("list-by-phase-oldest-first", func(t *testing.T) {
		store := newStore(t)
		first := newTask(t, "list-admitted-1")
		second := newTask(t, "list-admitted-2")
		// Same creation time as first: the ID breaks the tie.
		second.CreatedAt = first.CreatedAt
		second.UpdatedAt = first.UpdatedAt
		for _, record := range []taskstate.TaskRecord{second, first} {
			if err := store.CreateTask(context.Background(), record); err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
		}

		got, err := store.ListTasksByPhase(context.Background(), taskstate.TaskPhaseAdmitted, 10)
		if err != nil {
			t.Fatalf("ListTasksByPhase() error = %v", err)
		}
		// IDs are random: the tie-break itself is what the contract fixes,
		// so the expected order follows the ID comparison.
		wantOrder := []ids.TaskID{first.ID, second.ID}
		if second.ID < first.ID {
			wantOrder = []ids.TaskID{second.ID, first.ID}
		}
		if len(got) != 2 || got[0].ID != wantOrder[0] || got[1].ID != wantOrder[1] {
			t.Fatalf("ListTasksByPhase() = %v, want [%s %s]", got, wantOrder[0], wantOrder[1])
		}
	})

	t.Run("list-by-phase-leaves-the-phase-on-cas", func(t *testing.T) {
		store := newStore(t)
		record := newTask(t, "list-admitted-departs")
		if err := store.CreateTask(context.Background(), record); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		next := nextRevision(record, taskstate.TaskPhaseReady)
		if err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, next); err != nil {
			t.Fatalf("CompareAndSwapTask() error = %v", err)
		}

		got, err := store.ListTasksByPhase(context.Background(), taskstate.TaskPhaseAdmitted, 10)
		if err != nil {
			t.Fatalf("ListTasksByPhase() error = %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("ListTasksByPhase() = %v, want no admitted task", got)
		}
		ready, err := store.ListTasksByPhase(context.Background(), taskstate.TaskPhaseReady, 10)
		if err != nil {
			t.Fatalf("ListTasksByPhase() error = %v", err)
		}
		if len(ready) != 1 || ready[0].ID != record.ID {
			t.Fatalf("ready listing = %v, want [%s]", ready, record.ID)
		}
	})

	t.Run("list-by-phase-limit", func(t *testing.T) {
		store := newStore(t)
		for range 3 {
			if err := store.CreateTask(context.Background(), newTask(t, "list-admitted-limit")); err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
		}
		got, err := store.ListTasksByPhase(context.Background(), taskstate.TaskPhaseAdmitted, 2)
		if err != nil {
			t.Fatalf("ListTasksByPhase() error = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("ListTasksByPhase(limit 2) = %d records, want 2", len(got))
		}
	})

	t.Run("list-by-phase-invalid-input", func(t *testing.T) {
		store := newStore(t)
		if _, err := store.ListTasksByPhase(context.Background(), taskstate.TaskPhaseAdmitted, 0); err == nil {
			t.Fatal("ListTasksByPhase(limit 0) succeeded, want an error")
		}
		if _, err := store.ListTasksByPhase(context.Background(), taskstate.TaskPhaseTerminal, 1); err == nil {
			t.Fatal("ListTasksByPhase(terminal) succeeded, want an error")
		}
	})
}

// runAttemptListingContract fixes what dispatch reads to take the next free
// Attempt number: every Attempt of one Task in number order, closed included.
func runAttemptListingContract(t *testing.T, newStore AttemptFactory) {
	t.Helper()

	t.Run("list-by-task-in-number-order", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-listing")
		second := newAttempt(t, task, 2)
		first := newAttempt(t, task, 1)
		for _, attempt := range []attemptstate.AttemptRecord{second, first} {
			if err := store.CreateAttempt(context.Background(), attempt); err != nil {
				t.Fatalf("CreateAttempt(%s) error = %v", attempt.ID, err)
			}
		}

		got, err := store.ListAttemptsByTask(context.Background(), task.ID)
		if err != nil {
			t.Fatalf("ListAttemptsByTask() error = %v", err)
		}
		if len(got) != 2 || got[0].Number != 1 || got[1].Number != 2 {
			t.Fatalf("ListAttemptsByTask() = %d records, want attempts 1 then 2", len(got))
		}
	})

	t.Run("list-by-task-empty-and-other-tasks", func(t *testing.T) {
		store := newStore(t)
		task := readyParentTask(t, store, "attempt-listing-empty")
		other := readyParentTask(t, store, "attempt-listing-other")
		if err := store.CreateAttempt(context.Background(), newAttempt(t, other, 1)); err != nil {
			t.Fatalf("CreateAttempt() error = %v", err)
		}

		got, err := store.ListAttemptsByTask(context.Background(), task.ID)
		if err != nil {
			t.Fatalf("ListAttemptsByTask() error = %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("ListAttemptsByTask() = %d records, want none", len(got))
		}
	})
}

// runWorkerListingContract fixes what dispatch plans over: every registered
// Worker in identity order, revoked included, so the caller filters with
// Usable() instead of the store deciding for it.
func runWorkerListingContract(t *testing.T, newStore WorkerRegistryFactory) {
	t.Helper()

	t.Run("list-workers-in-identity-order", func(t *testing.T) {
		f := newRegistryFixture(t, newStore(t))
		ctx := context.Background()
		for range 2 {
			cred := f.issue(t, registryDomain, 0)
			if _, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain); err != nil {
				t.Fatalf("ConsumeEnrollmentCredential() error = %v", err)
			}
		}
		got, err := f.store.ListWorkers(ctx)
		if err != nil {
			t.Fatalf("ListWorkers() error = %v", err)
		}
		if len(got) != 2 || got[0].Identity >= got[1].Identity {
			t.Fatalf("ListWorkers() = %v, want two workers in identity order", got)
		}
		for _, w := range got {
			if !w.Usable() {
				t.Fatalf("new worker %s is not usable", w.Identity)
			}
		}

		// Revoking one keeps it listed: dispatch must see what it must not use.
		revoked := got[0].Identity
		if _, err := f.svc.SetWorkerState(ctx, f.admin, revoked, workerregistry.StateRevoked); err != nil {
			t.Fatalf("SetWorkerState() error = %v", err)
		}
		got, err = f.store.ListWorkers(ctx)
		if err != nil {
			t.Fatalf("ListWorkers() after revoke error = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("ListWorkers() after revoke = %d workers, want 2", len(got))
		}
		for _, w := range got {
			if w.Identity == revoked && w.Usable() {
				t.Fatalf("revoked worker %s is usable", w.Identity)
			}
		}
	})
}
