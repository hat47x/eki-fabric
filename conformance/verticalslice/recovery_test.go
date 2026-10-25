package verticalslice_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/recoveryadmission"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// reclaimed places the next Attempt of the Task on a Worker and has the Local
// Governor stop it, so the Attempt closes as reclaimed.
func (w *world) reclaimed(task ids.TaskID, worker string) placement {
	w.t.Helper()
	p := w.place(task, worker)
	claim, _ := w.execute(p, runnerFunc(runsUntilStopped), sourceFunc(userReturns))
	w.report(worker, claim)
	if got := w.attemptRecord(p.attempt); got.CloseReason != attemptstate.CloseReasonReclaimed {
		w.t.Fatalf("Attempt = %+v, want it closed as reclaimed", got)
	}
	return p
}

// complete finishes the Task on the Worker from the Attempt that waits to be placed.
func (w *world) complete(task ids.TaskID, worker string) {
	w.t.Helper()
	p := w.place(task, worker)
	claim, _ := w.execute(p, producesOutput("hello"), sourceFunc(quiet))
	w.report(worker, claim)
	if result := w.verifyAndAccept(p, "hello"); result.Outcome() != resultaccept.OutcomeAccepted {
		w.t.Fatalf("result = %+v", result)
	}
}

// The retry waits in the State Store. A restart before its start time must not
// move the time, and must not lose the retry.
func TestRecoveryQueueSurvivesARestartWithItsStartTime(t *testing.T) {
	w := newWorld(t)
	task := w.submit("rec-queue-restart")
	first := w.reclaimed(task, "worker-a")

	queued, err := w.recovery().Intake(w.ctx, first.attempt)
	if err != nil || queued.Hold != nil {
		t.Fatalf("Intake() = %+v, %v", queued, err)
	}
	w.restart()

	pending, err := w.state.LoadRecoveryPending(w.ctx)
	if err != nil || len(pending) != 1 || pending[0] != queued.Pending {
		t.Fatalf("pending after restart = %+v, %v, want the retry unchanged (start time included)", pending, err)
	}
	// Asking again after the restart, much later, does not recompute the start time.
	w.clock.advance(time.Hour)
	again, err := w.recovery().Intake(w.ctx, first.attempt)
	if err != nil || !again.Replayed || again.Pending.NotBefore != queued.Pending.NotBefore {
		t.Fatalf("Intake() after a restart = %+v, %v, want the stored retry", again, err)
	}

	// A retry that has not been released yet has no Permit to spend.
	if _, err := w.recovery().Admit(w.ctx, first.attempt); !errors.Is(err, recoveryadmission.ErrNoPermit) {
		t.Fatalf("Admit() before the release error = %v, want %v", err, recoveryadmission.ErrNoPermit)
	}
	_ = task
}

func TestRecoveryRestartBetweenReleaseAndAdmission(t *testing.T) {
	w := newWorld(t)
	task := w.submit("rec-release-restart")
	first := w.reclaimed(task, "worker-a")

	queued, err := w.recovery().Intake(w.ctx, first.attempt)
	if err != nil {
		t.Fatal(err)
	}
	w.clock.now = queued.Pending.NotBefore
	released, err := w.recovery().Release(w.ctx)
	if err != nil || len(released.Permits) != 1 {
		t.Fatalf("Release() = %+v, %v", released, err)
	}

	// The process stops after the Permit is committed and before the Attempt exists.
	w.restart()
	if attempts, _ := w.state.LoadRecoveryPending(w.ctx); len(attempts) != 0 {
		t.Fatalf("the released retry is still pending: %+v", attempts)
	}

	next := w.admitRecovery(task, first.attempt)
	if next.Number != 2 || next.Phase != attemptstate.AttemptPhaseCreated {
		t.Fatalf("next Attempt = %+v", next)
	}
	w.complete(task, "worker-b")
	w.evidenceOf(task).assertExplainable(t)
}

// A wave of reclaims is released a few at a time, and a restart does not give the
// window a fresh budget.
func TestMassReclaimIsReleasedWithinTheWindowAcrossRestarts(t *testing.T) {
	w := newWorld(t)
	w.recoveryPolicy.MaxReleasesPerWindow = 1
	tasks := make([]ids.TaskID, 3)
	for i := range tasks {
		tasks[i] = w.submit("rec-mass-reclaim-" + string(rune('a'+i)))
	}
	previous := make([]ids.AttemptID, len(tasks))
	for i, task := range tasks {
		previous[i] = w.reclaimed(task, "worker-a").attempt
	}
	var start time.Time
	for _, p := range previous {
		queued, err := w.recovery().Intake(w.ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if queued.Pending.NotBefore.After(start) {
			start = queued.Pending.NotBefore
		}
	}
	w.clock.now = start

	released := 0
	for window := 1; window <= 3; window++ {
		got, err := w.recovery().Release(w.ctx)
		if err != nil {
			t.Fatalf("window %d: Release() error = %v", window, err)
		}
		if len(got.Permits) != 1 {
			t.Fatalf("window %d released %d retries, want 1 (the budget is one per window)", window, len(got.Permits))
		}
		released++

		// A restart and another pass in the same window release nothing more.
		w.restart()
		w.clock.advance(5 * time.Second)
		more, err := w.recovery().Release(w.ctx)
		if err != nil || len(more.Permits) != 0 {
			t.Fatalf("window %d after a restart released %d more, err %v, want none", window, len(more.Permits), err)
		}
		w.clock.advance(2 * time.Minute)
	}
	if released != len(tasks) {
		t.Fatalf("released %d, want %d", released, len(tasks))
	}
	if pending, _ := w.state.LoadRecoveryPending(w.ctx); len(pending) != 0 {
		t.Fatalf("pending = %+v, want none", pending)
	}
}

// A Task that may not be retried automatically is held, not failed.
func TestRecoveryHoldsATaskThatMayNotBeRetried(t *testing.T) {
	w := newWorld(t)
	w.terms.Retryable = false
	task := w.submit("rec-hold")
	first := w.reclaimed(task, "worker-a")

	got, err := w.recovery().Intake(w.ctx, first.attempt)
	if err != nil || got.Hold == nil || got.Hold.Reason != recovery.HoldNotRetryable {
		t.Fatalf("Intake() = %+v, %v, want a hold", got, err)
	}
	if pending, _ := w.state.LoadRecoveryPending(w.ctx); len(pending) != 0 {
		t.Fatalf("a held Task was queued: %+v", pending)
	}
	if after := w.task(task); after.Phase != taskstate.TaskPhaseReady {
		t.Fatalf("holding changed the Task: %+v", after)
	}
}

// A Worker can stop every Attempt it is given, and say so. The reclaim is
// authoritative for that Attempt, but the Task's attempt budget is not the
// Worker's to widen: after the last allowed Attempt the Task is held.
func TestAWorkerThatReclaimsEveryAttemptCannotMakeATaskRunForever(t *testing.T) {
	w := newWorld(t)
	task := w.submit("rec-forever")

	var last ids.AttemptID
	for number := 1; number <= 3; number++ {
		if number > 1 {
			w.recoverAttempt(task, last)
		}
		last = w.reclaimed(task, "worker-a").attempt
	}
	got, err := w.recovery().Intake(w.ctx, last)
	if err != nil || got.Hold == nil || got.Hold.Reason != recovery.HoldRetryBudgetExhausted {
		t.Fatalf("Intake() after the last allowed Attempt = %+v, %v, want the attempt budget exhausted", got, err)
	}
	if _, found, _ := w.state.LoadRecoveryPermit(w.ctx, last); found {
		t.Fatal("a Permit exists for an Attempt beyond the budget")
	}
}

// A decided result is not an interruption: nobody can queue a retry for it.
func TestRecoveryRefusesAnAttemptWhoseResultWasDecided(t *testing.T) {
	w := newWorld(t)
	task := w.submit("rec-decided")
	p := w.place(task, "worker-a")
	claim, _ := w.execute(p, producesOutput("hello"), sourceFunc(quiet))
	w.report("worker-a", claim)
	w.verifyAndAccept(p, "hello")

	if _, err := w.recovery().Intake(w.ctx, p.attempt); !errors.Is(err, recoveryadmission.ErrNotInterrupted) {
		t.Fatalf("Intake() error = %v, want %v", err, recoveryadmission.ErrNotInterrupted)
	}
}
