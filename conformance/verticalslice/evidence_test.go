package verticalslice_test

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// attemptEvidence is everything the durable state says about one Attempt.
type attemptEvidence struct {
	ID          ids.AttemptID
	Number      uint64
	Worker      string
	Lease       ids.LeaseID
	Fence       uint64
	Phase       attemptstate.AttemptPhase
	CloseReason attemptstate.CloseReason
	UpdatedAt   time.Time
	Claim       *resultclaim.Claim
}

// evidence is the chain the product-value gate asks for: which Attempts ran
// where, under which fence, what each reported, and what was accepted. It is
// read back from the stores alone. Nothing in it is kept in test memory.
type evidence struct {
	Task            taskstate.TaskRecord
	Attempts        []attemptEvidence
	AcceptedAttempt ids.AttemptID
	AcceptedClaim   contract.Digest
	AcceptedOutput  contract.Digest
	AcceptedAt      time.Time
	Verifiers       []string
	PolicyDigest    contract.Digest
	HasAccepted     bool
}

func (w *world) evidenceOf(taskID ids.TaskID) evidence {
	w.t.Helper()
	e := evidence{Task: w.task(taskID)}
	for _, id := range w.attempt[taskID] {
		a := w.attemptRecord(id)
		item := attemptEvidence{
			ID: a.ID, Number: a.Number, Worker: a.WorkerIdentity, Lease: a.LeaseID,
			Phase: a.Phase, CloseReason: a.CloseReason, UpdatedAt: a.UpdatedAt,
		}
		if a.LeaseID != "" {
			item.Fence = w.leaseRecord(a.LeaseID).FenceEpoch
		}
		claim, found, err := w.state.LoadResultClaim(w.ctx, a.ID)
		if err != nil {
			w.t.Fatal(err)
		}
		if found {
			item.Claim = &claim
		}
		e.Attempts = append(e.Attempts, item)
	}
	accepted, ok, err := w.state.LoadAcceptedResult(w.ctx, taskID)
	if err != nil {
		w.t.Fatal(err)
	}
	if ok {
		e.HasAccepted = true
		e.AcceptedAttempt, e.AcceptedClaim, e.AcceptedOutput = accepted.AttemptID(), accepted.ClaimDigest(), accepted.OutputDigest()
		e.AcceptedAt, e.Verifiers, e.PolicyDigest = accepted.AcceptedAt(), accepted.Verifiers(), accepted.PolicyDigest()
	}
	return e
}

// assertExplainable checks that the chain is complete and consistent: a reader
// can say which Worker ran what, why each Attempt ended, and what justified the
// accepted result.
func (e evidence) assertExplainable(t *testing.T) {
	t.Helper()
	if len(e.Attempts) == 0 {
		t.Fatal("the Task has no Attempt")
	}
	var lastFence uint64
	closedAsAccepted := 0
	for i, a := range e.Attempts {
		if a.Number != uint64(i+1) {
			t.Fatalf("Attempt %d has number %d: the history has a gap", i+1, a.Number)
		}
		if a.Worker == "" || a.Lease == "" || a.Fence == 0 {
			t.Fatalf("Attempt %d does not say where it ran: %+v", a.Number, a)
		}
		if a.Fence <= lastFence {
			t.Fatalf("Attempt %d runs under fence %d, not above the previous %d", a.Number, a.Fence, lastFence)
		}
		lastFence = a.Fence
		if a.Phase == attemptstate.AttemptPhaseClosed && a.CloseReason == attemptstate.CloseReasonNone {
			t.Fatalf("Attempt %d is closed without a reason", a.Number)
		}
		if a.CloseReason == attemptstate.CloseReasonResultAccepted {
			closedAsAccepted++
		}
	}
	completed := e.Task.Phase == taskstate.TaskPhaseTerminal && e.Task.TerminalOutcome == taskstate.TerminalOutcomeCompleted
	if completed != e.HasAccepted {
		t.Fatalf("Task completed = %v but accepted result present = %v", completed, e.HasAccepted)
	}
	if !e.HasAccepted {
		if closedAsAccepted != 0 {
			t.Fatal("an Attempt is closed as accepted but the Task has no accepted result")
		}
		return
	}
	if closedAsAccepted != 1 {
		t.Fatalf("%d Attempts are closed as accepted, want exactly one", closedAsAccepted)
	}
	var accepted *attemptEvidence
	for i := range e.Attempts {
		if e.Attempts[i].ID == e.AcceptedAttempt {
			accepted = &e.Attempts[i]
		}
	}
	if accepted == nil || accepted.CloseReason != attemptstate.CloseReasonResultAccepted || accepted.Claim == nil {
		t.Fatalf("the accepted result names an Attempt that is not the accepted one: %+v", accepted)
	}
	if accepted.Claim.Digest() != e.AcceptedClaim || accepted.Claim.OutputDigest() != e.AcceptedOutput {
		t.Fatal("the accepted result does not match the claim the Worker reported")
	}
	if accepted.Claim.Status() != resultclaim.StatusCompleted {
		t.Fatalf("the accepted claim has status %s", accepted.Claim.Status())
	}
	if len(e.Verifiers) < 2 || e.PolicyDigest.IsZero() {
		t.Fatalf("the accepted result does not say who verified it: %v, %s", e.Verifiers, e.PolicyDigest)
	}
	for _, v := range e.Verifiers {
		if v == accepted.Worker {
			t.Fatalf("the producing Worker %s is listed as a verifier", v)
		}
	}
	if e.AcceptedAt.Before(accepted.Claim.EndedAt()) {
		t.Fatal("the result was accepted before the Worker finished")
	}
}
