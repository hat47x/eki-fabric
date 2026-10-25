package verticalslice_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/control/resultacceptance"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// V1: two Workers are registered. The first Attempt completes and independent
// verifiers confirm the output.
func TestV1HappyPath(t *testing.T) {
	w := newWorld(t)
	task := w.submit("v1-happy-path")

	p := w.place(task, "worker-a")
	claim, outcome := w.execute(p, producesOutput("hello"), sourceFunc(quiet))
	if outcome.Status != executor.StatusCompleted {
		t.Fatalf("outcome = %+v", outcome)
	}
	ack := w.report("worker-a", claim)
	if ack.Decision != string(resultclaim.KindNew) || !ack.VerificationRequired {
		t.Fatalf("ack = %+v, want a new claim that needs verification", ack)
	}
	// A completed report is a claim. It is not a result.
	if got := w.task(task); got.Phase == taskstate.TaskPhaseTerminal {
		t.Fatalf("the Task finished on the Worker's word alone: %+v", got)
	}

	result := w.verifyAndAccept(p, "hello")
	if result.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result = %+v", result)
	}
	e := w.evidenceOf(task)
	e.assertExplainable(t)
	if len(e.Attempts) != 1 || e.Task.TerminalOutcome != taskstate.TerminalOutcomeCompleted {
		t.Fatalf("evidence = %+v", e)
	}
}

// V2: the user returns to the device. The Local Governor stops the work without
// asking the Control Plane, the Attempt is closed as reclaimed, and a new Attempt
// on another Worker completes.
func TestV2UserReclaimRetriesOnAnotherWorker(t *testing.T) {
	w := newWorld(t)
	task := w.submit("v2-user-reclaim")

	first := w.place(task, "worker-a")
	claim, outcome := w.execute(first, runnerFunc(runsUntilStopped), sourceFunc(userReturns))
	if outcome.Status != executor.StatusReclaimed {
		t.Fatalf("outcome = %+v, want reclaimed", outcome)
	}
	ack := w.report("worker-a", claim)
	if ack.Decision != string(resultclaim.KindNew) || ack.VerificationRequired {
		t.Fatalf("ack = %+v", ack)
	}
	a1 := w.attemptRecord(first.attempt)
	if a1.Phase != attemptstate.AttemptPhaseClosed || a1.CloseReason != attemptstate.CloseReasonReclaimed {
		t.Fatalf("A1 = %+v, want closed as reclaimed", a1)
	}
	// A reclaim is not a failure of the Task.
	if got := w.task(task); got.Phase != taskstate.TaskPhaseReady {
		t.Fatalf("a reclaim changed the Task: %+v", got)
	}

	next := w.recoverAttempt(task, first.attempt)
	if next.Number != 2 {
		t.Fatalf("next Attempt = %+v", next)
	}
	second := w.place(task, "worker-b")
	if second.fence <= first.fence {
		t.Fatalf("A2 fence %d is not above A1 fence %d", second.fence, first.fence)
	}
	claim2, outcome2 := w.execute(second, producesOutput("hello"), sourceFunc(quiet))
	if outcome2.Status != executor.StatusCompleted {
		t.Fatalf("outcome = %+v", outcome2)
	}
	w.report("worker-b", claim2)
	if result := w.verifyAndAccept(second, "hello"); result.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result = %+v", result)
	}

	e := w.evidenceOf(task)
	e.assertExplainable(t)
	if len(e.Attempts) != 2 || e.AcceptedAttempt != second.attempt ||
		e.Attempts[0].CloseReason != attemptstate.CloseReasonReclaimed {
		t.Fatalf("evidence = %+v", e)
	}
}

// V3: Worker 1 stops answering and its Attempt is closed. Attempt 2 on Worker 2
// completes the Task. Worker 1 then returns a late completed report for Attempt 1.
// The report is kept as evidence and changes nothing.
func TestV3LateResultFromASupersededAttemptChangesNothing(t *testing.T) {
	w := newWorld(t)
	task := w.submit("v3-late-result")

	first := w.place(task, "worker-a")
	// Worker 1 does the work but its report is delayed. Meanwhile the Lease
	// expires and the Control Plane closes the Attempt as lost.
	lateClaim, outcome := w.execute(first, producesOutput("hello"), sourceFunc(quiet))
	if outcome.Status != executor.StatusCompleted {
		t.Fatalf("outcome = %+v", outcome)
	}
	w.clock.advance(10 * time.Minute)
	a1 := w.attemptRecord(first.attempt)
	lost := a1
	lost.Phase, lost.CloseReason = attemptstate.AttemptPhaseClosed, attemptstate.CloseReasonWorkerLost
	lost.Revision++
	lost.UpdatedAt = w.clock.now
	if err := w.state.CompareAndSwapAttempt(w.ctx, a1.ID, a1.Revision, lost); err != nil {
		t.Fatal(err)
	}

	w.recoverAttempt(task, first.attempt)
	second := w.place(task, "worker-b")
	claim2, _ := w.execute(second, producesOutput("hello"), sourceFunc(quiet))
	w.report("worker-b", claim2)
	if result := w.verifyAndAccept(second, "hello"); result.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result = %+v", result)
	}
	before := w.evidenceOf(task)

	ack := w.report("worker-a", lateClaim)
	if ack.Decision != string(resultclaim.KindLate) || ack.VerificationRequired {
		t.Fatalf("late ack = %+v, want a late claim that needs no verification", ack)
	}
	after := w.evidenceOf(task)
	if after.Attempts[0].Claim == nil {
		t.Fatal("the late claim was not kept as evidence")
	}
	if after.Attempts[0].Phase != attemptstate.AttemptPhaseClosed ||
		after.Attempts[0].CloseReason != attemptstate.CloseReasonWorkerLost {
		t.Fatalf("a late claim reopened or relabelled A1: %+v", after.Attempts[0])
	}
	if after.Task.Revision != before.Task.Revision || after.AcceptedAttempt != second.attempt {
		t.Fatalf("a late claim changed the outcome: before %+v, after %+v", before.Task, after.Task)
	}
	_, err := w.acceptance().Accept(w.ctx, resultacceptance.Request{AttemptID: first.attempt})
	if !errors.Is(err, resultacceptance.ErrWrongPhase) {
		t.Fatalf("accepting the superseded Attempt error = %v, want %v", err, resultacceptance.ErrWrongPhase)
	}
	after.assertExplainable(t)
}
