package statestoretest

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// RunTaskTransitionContract checks that the store refuses Task writes the state
// model does not allow, and reports them apart from a revision conflict so the
// caller cannot retry its way into an impossible state (#280).
func RunTaskTransitionContract(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("task-transitions", func(t *testing.T) {
		t.Run("allowed-path-runs-forward", func(t *testing.T) {
			store := newStore(t)
			ctx := context.Background()
			record := newTask(t, "transition-forward")
			if err := store.CreateTask(ctx, record); err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
			for _, phase := range []taskstate.TaskPhase{
				taskstate.TaskPhaseReady,
				taskstate.TaskPhaseActive,
				taskstate.TaskPhaseVerifying,
			} {
				next := nextRevision(record, phase)
				if err := store.CompareAndSwapTask(ctx, record.ID, record.Revision, next); err != nil {
					t.Fatalf("CompareAndSwapTask(%s) error = %v", phase, err)
				}
				record = next
			}
			done := nextRevision(record, taskstate.TaskPhaseTerminal)
			done.TerminalOutcome = taskstate.TerminalOutcomeFailed
			if err := store.CompareAndSwapTask(ctx, record.ID, record.Revision, done); err != nil {
				t.Fatalf("CompareAndSwapTask(terminal) error = %v", err)
			}
		})

		t.Run("terminal-never-transitions", func(t *testing.T) {
			store := newStore(t)
			ctx := context.Background()
			record := newTask(t, "transition-terminal")
			if err := store.CreateTask(ctx, record); err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
			done := nextRevision(record, taskstate.TaskPhaseTerminal)
			done.TerminalOutcome = taskstate.TerminalOutcomeCancelled
			if err := store.CompareAndSwapTask(ctx, record.ID, record.Revision, done); err != nil {
				t.Fatalf("CompareAndSwapTask(terminal) error = %v", err)
			}
			for _, phase := range []taskstate.TaskPhase{taskstate.TaskPhaseReady, taskstate.TaskPhaseActive} {
				// Reopening a finished Task is the write this contract refuses;
				// the record is otherwise well formed, so the refusal must come
				// from the transition and not from field validation.
				next := nextRevision(done, phase)
				next.TerminalOutcome = taskstate.TerminalOutcomeNone
				err := store.CompareAndSwapTask(ctx, done.ID, done.Revision, next)
				if !errors.Is(err, taskstate.ErrInvalidTaskTransition) {
					t.Fatalf("terminal -> %s error = %v, want %v", phase, err, taskstate.ErrInvalidTaskTransition)
				}
				if errors.Is(err, statestore.ErrConflict) {
					t.Fatalf("terminal -> %s was reported as a revision conflict", phase)
				}
			}
		})

		t.Run("phase-does-not-move-backwards", func(t *testing.T) {
			store := newStore(t)
			ctx := context.Background()
			record := newTask(t, "transition-backwards")
			if err := store.CreateTask(ctx, record); err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
			ready := nextRevision(record, taskstate.TaskPhaseReady)
			if err := store.CompareAndSwapTask(ctx, record.ID, record.Revision, ready); err != nil {
				t.Fatalf("CompareAndSwapTask(ready) error = %v", err)
			}
			err := store.CompareAndSwapTask(ctx, ready.ID, ready.Revision, nextRevision(ready, taskstate.TaskPhaseAdmitted))
			if !errors.Is(err, taskstate.ErrInvalidTaskTransition) {
				t.Fatalf("ready -> admitted error = %v, want %v", err, taskstate.ErrInvalidTaskTransition)
			}
		})

		t.Run("skipping-ahead-is-refused", func(t *testing.T) {
			store := newStore(t)
			ctx := context.Background()
			record := newTask(t, "transition-skip")
			if err := store.CreateTask(ctx, record); err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
			err := store.CompareAndSwapTask(ctx, record.ID, record.Revision, nextRevision(record, taskstate.TaskPhaseVerifying))
			if !errors.Is(err, taskstate.ErrInvalidTaskTransition) {
				t.Fatalf("admitted -> verifying error = %v, want %v", err, taskstate.ErrInvalidTaskTransition)
			}
		})

		t.Run("same-phase-update-keeps-the-cancellation-intent", func(t *testing.T) {
			store := newStore(t)
			ctx := context.Background()
			record := newTask(t, "transition-cancel-intent")
			if err := store.CreateTask(ctx, record); err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
			active := nextRevision(record, taskstate.TaskPhaseActive)
			if err := store.CompareAndSwapTask(ctx, record.ID, record.Revision, active); err != nil {
				t.Fatalf("CompareAndSwapTask(active) error = %v", err)
			}
			requested := nextRevision(active, taskstate.TaskPhaseActive)
			requested.CancellationRequested = true
			if err := store.CompareAndSwapTask(ctx, active.ID, active.Revision, requested); err != nil {
				t.Fatalf("CompareAndSwapTask(active, cancellation requested) error = %v", err)
			}
			got, err := store.LoadTask(ctx, requested.ID)
			if err != nil {
				t.Fatalf("LoadTask() error = %v", err)
			}
			if !got.CancellationRequested || got.Phase != taskstate.TaskPhaseActive {
				t.Fatalf("stored task = %+v, want active with the cancellation intent", got)
			}
		})
	})
}
