package verticalslice_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	"github.com/hat47x/eki-fabric/internal/control/resultacceptance"
	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// V4: a Client that can submit work but means harm. Each request is refused at
// the door, none of them takes the Client's quota, and a valid request still goes
// through.
func TestV4MaliciousClientCannotWidenItsAuthority(t *testing.T) {
	w := newWorld(t)
	svc := w.admissionService(1)
	alice := admission.Principal{ID: "alice"}

	nested := func(b map[string]any, key string) map[string]any { return b[key].(map[string]any) }
	cases := map[string]func(map[string]any){
		"names a Worker directly":            func(b map[string]any) { b["worker_id"] = "worker-secret-01" },
		"names an arbitrary executable path": func(b map[string]any) { nested(b, "executor")["id"] = "/bin/sh" },
		"gives an arbitrary URL as input": func(b map[string]any) {
			b["inputs"] = []any{map[string]any{"name": "source", "artifact_id": "art_01", "url": "http://169.254.169.254/latest/meta-data"}}
		},
		"asks for a lower data class":          func(b map[string]any) { b["data_class"] = "public" },
		"widens the retry budget":              func(b map[string]any) { nested(b, "reliability")["max_attempts"] = 99 },
		"asks for an unknown effect mode":      func(b map[string]any) { nested(b, "reliability")["effect_mode"] = "exotic" },
		"asks for more CPU than the ceiling":   func(b map[string]any) { nested(b, "resources")["cpu_millis"] = 1_000_000_000 },
		"asks for no ceiling on output bytes":  func(b map[string]any) { nested(b, "resources")["output_bytes_max"] = 1 << 60 },
		"asks for the schema it was not given": func(b map[string]any) { b["schema"] = "eki.task-request/v99" },
	}
	n := 0
	for name, mutate := range cases {
		n++
		_, err := svc.Submit(w.ctx, alice, fmt.Sprintf("malicious-request-%02d", n), taskBody(t, mutate))
		var refused *admission.Error
		if !errors.As(err, &refused) {
			t.Fatalf("%s: Submit() error = %v, want the request to be refused", name, err)
		}
	}

	// None of the refusals took the quota.
	first, err := svc.Submit(w.ctx, alice, "valid-request-01", taskBody(t, nil))
	if err != nil {
		t.Fatalf("a valid request was refused after the malicious ones: %v", err)
	}
	if _, err := svc.Submit(w.ctx, alice, "valid-request-02", taskBody(t, nil)); err == nil {
		t.Fatal("the outstanding Task quota was not enforced")
	}

	// The same key with the same request returns the same Task. The same key with
	// a different request is refused instead of silently reusing the first.
	again, err := svc.Submit(w.ctx, alice, "valid-request-01", taskBody(t, nil))
	if err != nil || !again.Replayed || again.TaskID != first.TaskID {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	_, err = svc.Submit(w.ctx, alice, "valid-request-01", taskBody(t, func(b map[string]any) {
		nested(b, "resources")["cpu_millis"] = 2000
	}))
	var conflict *admission.Error
	if !errors.As(err, &conflict) || conflict.Code != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Fatalf("a changed request under the same key error = %v", err)
	}
}

// V5: a Worker that means harm. A Worker is authenticated but never trusted.
func TestV5MaliciousWorker(t *testing.T) {
	t.Run("cannot report as another Worker", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v5-impersonation")
		p := w.place(task, "worker-a")
		claim, _ := w.execute(p, producesOutput("hello"), sourceFunc(quiet))

		_, err := w.workerPlane().SubmitClaim(w.ctx, w.principal("worker-b"), claim)
		if !errors.Is(err, claimplane.ErrNotClaimOwner) {
			t.Fatalf("SubmitClaim() error = %v, want %v", err, claimplane.ErrNotClaimOwner)
		}
		if _, found, _ := w.state.LoadResultClaim(w.ctx, p.attempt); found {
			t.Fatal("a claim sent under another Worker's name was recorded")
		}
		if got := w.attemptRecord(p.attempt); got.Phase != attemptstate.AttemptPhaseDispatched {
			t.Fatalf("the Attempt moved: %+v", got)
		}
	})

	t.Run("a claim under a stale fence is kept but moves nothing", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v5-stale-fence")
		first := w.place(task, "worker-a")
		claim, _ := w.execute(first, runnerFunc(runsUntilStopped), sourceFunc(userReturns))
		w.report("worker-a", claim)
		w.recoverAttempt(task, first.attempt, 1)
		second := w.place(task, "worker-b")
		genuine, _ := w.execute(second, producesOutput("hello"), sourceFunc(quiet))

		stale := withFence(t, genuine, first.fence)
		ack, err := w.workerPlane().SubmitClaim(w.ctx, w.principal("worker-b"), stale)
		if err != nil {
			t.Fatalf("SubmitClaim() error = %v", err)
		}
		if ack.Decision != string(resultclaim.KindStale) || ack.VerificationRequired {
			t.Fatalf("ack = %+v, want a stale claim that needs no verification", ack)
		}
		if got := w.attemptRecord(second.attempt); got.Phase != attemptstate.AttemptPhaseDispatched {
			t.Fatalf("a stale claim advanced the Attempt: %+v", got)
		}
		_, err = w.acceptance().Accept(w.ctx, resultacceptance.Request{AttemptID: second.attempt})
		if !errors.Is(err, resultacceptance.ErrWrongPhase) {
			t.Fatalf("accepting after a stale claim error = %v, want %v", err, resultacceptance.ErrWrongPhase)
		}
		if got := w.task(task); got.Phase == taskstate.TaskPhaseTerminal {
			t.Fatalf("a stale claim finished the Task: %+v", got)
		}
	})

	t.Run("a claim ahead of its Lease is refused", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v5-future-fence")
		p := w.place(task, "worker-a")
		genuine, _ := w.execute(p, producesOutput("hello"), sourceFunc(quiet))

		_, err := w.workerPlane().SubmitClaim(w.ctx, w.principal("worker-a"), withFence(t, genuine, p.fence+90))
		if !errors.Is(err, claimaccept.ErrBindingMismatch) {
			t.Fatalf("SubmitClaim() error = %v, want %v", err, claimaccept.ErrBindingMismatch)
		}
		if _, found, _ := w.state.LoadResultClaim(w.ctx, p.attempt); found {
			t.Fatal("a claim from the future was recorded")
		}
	})

	t.Run("a second, different report loses to the first", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v5-conflicting-report")
		p := w.place(task, "worker-a")
		genuine, _ := w.execute(p, producesOutput("hello"), sourceFunc(quiet))
		if ack := w.report("worker-a", genuine); ack.Decision != string(resultclaim.KindNew) {
			t.Fatalf("ack = %+v", ack)
		}
		before := w.attemptRecord(p.attempt)

		// The same report again is a duplicate: it changes nothing.
		ack, err := w.workerPlane().SubmitClaim(w.ctx, w.principal("worker-a"), genuine)
		if err != nil || ack.Decision != string(resultclaim.KindDuplicate) {
			t.Fatalf("duplicate ack = %+v, %v", ack, err)
		}
		// A different report for the same Attempt is refused.
		other := withOutput(t, genuine, "something else")
		if _, err := w.workerPlane().SubmitClaim(w.ctx, w.principal("worker-a"), other); !errors.Is(err, resultclaim.ErrConflict) {
			t.Fatalf("conflicting report error = %v, want %v", err, resultclaim.ErrConflict)
		}
		stored, _, _ := w.state.LoadResultClaim(w.ctx, p.attempt)
		if !stored.Equal(genuine) {
			t.Fatal("the first report was replaced")
		}
		if after := w.attemptRecord(p.attempt); after.Revision != before.Revision {
			t.Fatalf("a repeat changed the Attempt: %+v -> %+v", before, after)
		}
	})

	t.Run("many identities behind one principal cannot accept a result", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v5-sybil")
		p := w.place(task, "worker-a")
		claim, _ := w.execute(p, producesOutput("hello"), sourceFunc(quiet))
		w.report("worker-a", claim)

		swarm := make([]resultaccept.Verification, 0, 6)
		for i := 0; i < 6; i++ {
			v := reproduced(p, fmt.Sprintf("sybil-%02d", i), "hello")
			v.Dimensions[resultaccept.DimensionPrincipal] = "principal-sybil"
			swarm = append(swarm, v)
		}
		result, err := w.acceptance().Accept(w.ctx, resultacceptance.Request{AttemptID: p.attempt, Verifications: swarm})
		if err != nil || result.Outcome() != resultaccept.OutcomeHeld {
			t.Fatalf("result = %+v, %v, want the swarm to count as one voice and be held", result, err)
		}
		if got := w.task(task); got.Phase == taskstate.TaskPhaseTerminal {
			t.Fatalf("a Sybil swarm completed the Task: %+v", got)
		}
	})

	t.Run("a corrupted output is not accepted", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v5-corrupt-output")
		p := w.place(task, "worker-a")
		claim, _ := w.execute(p, producesOutput("corrupted"), sourceFunc(quiet))
		w.report("worker-a", claim)

		// Independent verifiers re-derive the output and get something else.
		differs := func(id string) resultaccept.Verification {
			v := reproduced(p, id, "hello")
			v.Check = resultaccept.CheckDiffers
			return v
		}
		result, err := w.acceptance().Accept(w.ctx, resultacceptance.Request{
			AttemptID: p.attempt, Verifications: []resultaccept.Verification{differs("verifier-1"), differs("verifier-2")},
		})
		if err != nil || result.Outcome() == resultaccept.OutcomeAccepted {
			t.Fatalf("result = %+v, %v, want the corrupted output not accepted", result, err)
		}
		if got := w.task(task); got.Phase == taskstate.TaskPhaseTerminal {
			t.Fatalf("a corrupted output completed the Task: %+v", got)
		}
	})

	// A mutated WorkOrder is covered by the signed chain (TestSignedLeaseAndWorkOrder
	// OnTheWorker). The executable whose bytes changed needs the native Runner, which
	// the slice does not start from a WorkOrder; it is covered by the native Runner
	// tests and listed here so that the gap stays visible.
	t.Run("an executable whose bytes changed is not run", func(t *testing.T) {
		t.Skip("not wired into the slice: covered by the native Runner tests (ErrExecutorChanged)")
	})
}

// V6: the Control Plane and the Agents restart. What was durable carries on; what
// was not durable is not invented.
func TestV6Restart(t *testing.T) {
	t.Run("between the first Attempt closing and the second being placed", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v6-between-attempts")
		first := w.place(task, "worker-a")
		claim, _ := w.execute(first, runnerFunc(runsUntilStopped), sourceFunc(userReturns))
		w.report("worker-a", claim)

		w.restart()

		// Everything the next step needs is read back from the durable state.
		permit := w.recoverAttempt(task, first.attempt, 1)
		if permit.NextAttemptNumber() != 2 {
			t.Fatalf("permit = %+v", permit)
		}
		second := w.place(task, "worker-b")
		claim2, _ := w.execute(second, producesOutput("hello"), sourceFunc(quiet))
		w.report("worker-b", claim2)
		if result := w.verifyAndAccept(second, "hello"); result.Outcome() != resultaccept.OutcomeAccepted {
			t.Fatalf("result = %+v", result)
		}
		w.evidenceOf(task).assertExplainable(t)
	})

	t.Run("a restart never makes two Attempts active at once", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v6-single-authority")
		first := w.place(task, "worker-a")

		w.restart()

		if _, err := w.tryPlace(task, "worker-b"); !errors.Is(err, statestore.ErrLeaseActive) {
			t.Fatalf("second placement error = %v, want %v", err, statestore.ErrLeaseActive)
		}
		if got := w.leaseRecord(first.lease); got.FenceEpoch != first.fence {
			t.Fatalf("the first Lease changed across the restart: %+v", got)
		}
	})

	t.Run("while the report waits in the Worker's queue", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v6-queued-report")
		p := w.place(task, "worker-a")
		claim, _ := w.execute(p, producesOutput("hello"), sourceFunc(quiet))
		w.enqueue("worker-a", claim)

		w.restart()

		acks := w.deliver("worker-a")
		if len(acks) != 1 || acks[0].Decision != string(resultclaim.KindNew) {
			t.Fatalf("acks = %+v, want the queued report delivered once", acks)
		}
		if result := w.verifyAndAccept(p, "hello"); result.Outcome() != resultaccept.OutcomeAccepted {
			t.Fatalf("result = %+v", result)
		}
		w.evidenceOf(task).assertExplainable(t)
	})

	t.Run("after the result was accepted", func(t *testing.T) {
		w := newWorld(t)
		task := w.submit("v6-after-acceptance")
		p := w.place(task, "worker-a")
		claim, outcome := w.execute(p, producesOutput("hello"), sourceFunc(quiet))
		if outcome.Status != executor.StatusCompleted {
			t.Fatalf("outcome = %+v", outcome)
		}
		w.report("worker-a", claim)
		first := w.verifyAndAccept(p, "hello")
		before := w.evidenceOf(task)

		w.restart()

		after := w.evidenceOf(task)
		after.assertExplainable(t)
		if after.AcceptedClaim != before.AcceptedClaim || after.AcceptedAt != before.AcceptedAt {
			t.Fatalf("the accepted result changed across the restart: %+v -> %+v", before, after)
		}
		again, err := w.acceptance().Accept(w.ctx, resultacceptance.Request{AttemptID: p.attempt})
		if err != nil || !again.Replayed || !again.Accepted.Equal(first.Accepted) {
			t.Fatalf("Accept() after restart = %+v, %v, want the stored result replayed", again, err)
		}
	})
}

// withFence returns the same report under another fence epoch.
func withFence(t *testing.T, c resultclaim.Claim, fence uint64) resultclaim.Claim {
	t.Helper()
	return rebuild(t, c, func(cfg *resultclaim.ClaimConfig) { cfg.FenceEpoch = fence })
}

// withOutput returns the same report claiming a different output.
func withOutput(t *testing.T, c resultclaim.Claim, output string) resultclaim.Claim {
	t.Helper()
	return rebuild(t, c, func(cfg *resultclaim.ClaimConfig) { cfg.OutputDigest = contract.SHA256([]byte(output)) })
}

func rebuild(t *testing.T, c resultclaim.Claim, mutate func(*resultclaim.ClaimConfig)) resultclaim.Claim {
	t.Helper()
	cfg := resultclaim.ClaimConfig{
		TaskID: c.TaskID(), TaskIRDigest: c.TaskIRDigest(), AttemptID: c.AttemptID(),
		WorkerIdentity: c.WorkerIdentity(), LeaseID: c.LeaseID(), FenceEpoch: c.FenceEpoch(),
		WorkOrderDecision: c.WorkOrderDecision(), ExecutorDigest: c.ExecutorDigest(), Nonce: c.Nonce(),
		Status: c.Status(), ExitCode: c.ExitCode(), OutputBytes: c.OutputBytes(), OutputDigest: c.OutputDigest(),
		StartedAt: c.StartedAt(), EndedAt: c.EndedAt(),
	}
	mutate(&cfg)
	out, err := resultclaim.NewClaim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
