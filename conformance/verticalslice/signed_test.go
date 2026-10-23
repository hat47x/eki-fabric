package verticalslice_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// flipped returns a copy of the bytes with one byte of the middle changed.
func flipped(b []byte) []byte {
	out := append([]byte(nil), b...)
	out[len(out)/2] ^= 0xff
	return out
}

// The same flow as V1 and V2, but the Placement, Lease and WorkOrder are signed by
// their Authorities and verified by the Worker, and the Worker starts only from the
// plan its own start gate produces.
func TestSignedV1HappyPath(t *testing.T) {
	w := newWorld(t)
	c := newChain(w)
	task := w.submit("sv1-happy-path")

	s := c.place(task, "worker-a", throughStart)
	if s.admission.FenceEpoch() != s.placement.fence || s.admission.WorkerIdentity() != "worker-a" {
		t.Fatalf("admission = %+v", s.admission)
	}
	// The effective limits are the smaller of the WorkOrder grant and what the
	// Local Governor leaves on this machine.
	if got := s.plan.Limits(); got.CPUMillis != workOrderLimits.CPUMillis || got.MemoryBytes != workOrderLimits.MemoryBytes {
		t.Fatalf("limits = %+v", got)
	}

	claim, outcome := c.run(s, producesOutput("hello"), sourceFunc(quiet))
	if outcome.Status != executor.StatusCompleted {
		t.Fatalf("outcome = %+v", outcome)
	}
	// The claim carries the WorkOrder's own decision, executor and nonce.
	if claim.WorkOrderDecision() != s.admission.OrderDecisionID() || claim.ExecutorDigest() != executorDigest ||
		string(claim.Nonce()) != string(s.admission.Nonce()) {
		t.Fatalf("claim does not carry the WorkOrder binding: %+v", claim)
	}
	if ack := w.report("worker-a", claim); !ack.VerificationRequired {
		t.Fatalf("ack = %+v", ack)
	}
	if result := w.verifyAndAccept(s.placement, "hello"); result.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result = %+v", result)
	}
	w.evidenceOf(task).assertExplainable(t)
}

func TestSignedV2ReclaimNeedsANewApprovalForTheNextWorker(t *testing.T) {
	w := newWorld(t)
	c := newChain(w)
	task := w.submit("sv2-reclaim")

	first := c.place(task, "worker-a", throughStart)
	claim, outcome := c.run(first, runnerFunc(runsUntilStopped), sourceFunc(userReturns))
	if outcome.Status != executor.StatusReclaimed {
		t.Fatalf("outcome = %+v", outcome)
	}
	w.report("worker-a", claim)
	w.recoverAttempt(task, first.placement.attempt, 1)

	// The old Placement Approval does not place the next Attempt. A new Attempt
	// needs a new approval for its own Worker.
	second := c.place(task, "worker-b", throughStart)
	if second.placement.fence <= first.placement.fence {
		t.Fatalf("fence %d is not above %d", second.placement.fence, first.placement.fence)
	}
	if string(second.signedPlacement) == string(first.signedPlacement) {
		t.Fatal("the second Attempt reused the first Placement Approval")
	}
	claim2, _ := c.run(second, producesOutput("hello"), sourceFunc(quiet))
	w.report("worker-b", claim2)
	if result := w.verifyAndAccept(second.placement, "hello"); result.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result = %+v", result)
	}
	w.evidenceOf(task).assertExplainable(t)
}

// What a hostile Client or Worker, or a forger, can try against the Placement
// Approval. Each attempt leaves no Lease behind.
func TestSignedPlacementApproval(t *testing.T) {
	// prepare creates an Attempt bound to worker-a and returns what a correct
	// approval would be signed over.
	type prepared struct {
		w       *world
		c       *chain
		attempt attemptstate.AttemptRecord
	}
	prepare := func(t *testing.T, seed string) prepared {
		w := newWorld(t)
		c := newChain(w)
		task := w.submit(seed)
		return prepared{w: w, c: c, attempt: c.startAttempt(task, "worker-a")}
	}
	reserve := func(p prepared, raw []byte) error {
		_, err := p.c.leaseService().Reserve(p.w.ctx, leaseissue.Request{SignedPlacement: raw, RequestedLifetime: 3 * time.Minute})
		return err
	}
	untouched := func(t *testing.T, p prepared) {
		t.Helper()
		got := p.w.attemptRecord(p.attempt.ID)
		if got.Phase != attemptstate.AttemptPhasePlacing || got.LeaseID != "" {
			t.Fatalf("a refused approval changed the Attempt: %+v", got)
		}
	}

	t.Run("signed by a key the Control Plane does not trust", func(t *testing.T) {
		p := prepare(t, "sp-forged")
		forger := newAuthorities(t, p.w.clock.now, 99)
		raw := forger.signPlacement(placementSpec{
			decisionID: "placement-forged", task: p.attempt.TaskID, taskIR: p.attempt.TaskIRDigest, attempt: p.attempt.ID,
			worker: "worker-a", policy: p.w.policy.Revision, issuedAt: p.w.clock.now.Add(-time.Second), expiresAt: p.w.clock.now.Add(5 * time.Minute),
		})
		if err := reserve(p, raw); err == nil {
			t.Fatal("a forged Placement Approval was accepted")
		}
		untouched(t, p)
	})

	t.Run("altered after signing", func(t *testing.T) {
		p := prepare(t, "sp-altered")
		if err := reserve(p, flipped(p.c.approve(p.attempt, "worker-a"))); err == nil {
			t.Fatal("an altered Placement Approval was accepted")
		}
		untouched(t, p)
	})

	t.Run("approves another Worker than the Attempt is bound to", func(t *testing.T) {
		p := prepare(t, "sp-other-worker")
		err := reserve(p, p.c.approve(p.attempt, "worker-b"))
		if !errors.Is(err, leaseissue.ErrCurrentState) {
			t.Fatalf("Reserve() error = %v, want %v", err, leaseissue.ErrCurrentState)
		}
		untouched(t, p)
	})

	t.Run("already expired", func(t *testing.T) {
		p := prepare(t, "sp-expired")
		now := p.w.clock.now
		raw := p.c.auth.signPlacement(placementSpec{
			decisionID: "placement-expired", task: p.attempt.TaskID, taskIR: p.attempt.TaskIRDigest, attempt: p.attempt.ID,
			worker: "worker-a", policy: p.w.policy.Revision, issuedAt: now.Add(-9 * time.Minute), expiresAt: now.Add(-time.Minute),
		})
		if err := reserve(p, raw); err == nil {
			t.Fatal("an expired Placement Approval was accepted")
		}
		untouched(t, p)
	})

	t.Run("names a policy revision that is not current", func(t *testing.T) {
		p := prepare(t, "sp-stale-policy")
		now := p.w.clock.now
		raw := p.c.auth.signPlacement(placementSpec{
			decisionID: "placement-stale-policy", task: p.attempt.TaskID, taskIR: p.attempt.TaskIRDigest, attempt: p.attempt.ID,
			worker: "worker-a", policy: "policy-0", issuedAt: now.Add(-time.Second), expiresAt: now.Add(5 * time.Minute),
		})
		if err := reserve(p, raw); err == nil {
			t.Fatal("a Placement Approval for an old policy was accepted")
		}
		untouched(t, p)
	})

	t.Run("a replay converges on the same Lease, and cannot revive an expired one", func(t *testing.T) {
		p := prepare(t, "sp-replay")
		raw := p.c.approve(p.attempt, "worker-a")
		req := leaseissue.Request{SignedPlacement: raw, RequestedLifetime: 3 * time.Minute}
		first, err := p.c.leaseService().Reserve(p.w.ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		again, err := p.c.leaseService().Reserve(p.w.ctx, req)
		if err != nil || !again.Replayed || again.Lease.ID != first.Lease.ID || again.Lease.FenceEpoch != first.Lease.FenceEpoch {
			t.Fatalf("replay = %+v, %v, want the same Lease and fence", again, err)
		}
		p.w.clock.advance(4 * time.Minute) // the Lease (3 minutes) has expired; the approval (5 minutes) has not
		if _, err := p.c.leaseService().Reserve(p.w.ctx, req); !errors.Is(err, leaseissue.ErrPlacementConsumed) {
			t.Fatalf("replay after the Lease expired error = %v, want %v", err, leaseissue.ErrPlacementConsumed)
		}
	})
}

// What can be done to the signed Lease and WorkOrder on their way to the Worker, or
// by a Worker that tries to use bytes meant for another.
func TestSignedLeaseAndWorkOrderOnTheWorker(t *testing.T) {
	setup := func(t *testing.T, seed string) (*world, *chain, *signed) {
		w := newWorld(t)
		c := newChain(w)
		return w, c, c.place(w.submit(seed), "worker-a", throughWorkOrder)
	}

	t.Run("a Lease altered in transit is not accepted", func(t *testing.T) {
		_, c, s := setup(t, "sl-altered-lease")
		s.signedLease = flipped(s.signedLease)
		if _, err := c.tryStartOnWorker(s, "worker-a"); err == nil {
			t.Fatal("an altered Lease was accepted")
		}
	})

	t.Run("a WorkOrder altered in transit is not accepted, and does not use up its nonce", func(t *testing.T) {
		_, c, s := setup(t, "sl-altered-order")
		genuine := s.signedWorkOrder
		s.signedWorkOrder = flipped(genuine)
		if _, err := c.tryStartOnWorker(s, "worker-a"); err == nil {
			t.Fatal("an altered WorkOrder was accepted")
		}
		s.signedWorkOrder = genuine
		if _, err := c.tryStartOnWorker(s, "worker-a"); err != nil {
			t.Fatalf("the genuine WorkOrder was refused after a rejected one: %v", err)
		}
	})

	t.Run("a Lease and WorkOrder for one Worker are not accepted by another", func(t *testing.T) {
		_, c, s := setup(t, "sl-other-worker")
		if _, err := c.tryStartOnWorker(s, "worker-b"); err == nil {
			t.Fatal("worker-b accepted what was issued to worker-a")
		}
	})

	t.Run("a WorkOrder cannot be used twice", func(t *testing.T) {
		_, c, s := setup(t, "sl-replay")
		if _, err := c.tryStartOnWorker(s, "worker-a"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.tryStartOnWorker(s, "worker-a"); err == nil {
			t.Fatal("the same WorkOrder started twice")
		}
	})

	t.Run("a WorkOrder after its Lease has expired is not accepted", func(t *testing.T) {
		w, c, s := setup(t, "sl-expired")
		w.clock.advance(10 * time.Minute)
		if _, err := c.tryStartOnWorker(s, "worker-a"); err == nil {
			t.Fatal("a WorkOrder was accepted after its Lease expired")
		}
	})

	t.Run("the Local Governor can refuse to start whatever the Control Plane allowed", func(t *testing.T) {
		_, c, s := setup(t, "sl-governor")
		admission, err := c.tryStartOnWorker(s, "worker-a")
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.worker("worker-a").gate.Check(startgate.Request{
			Admission: admission,
			Governor: governor.Decision{
				AcceptNewLeases: true, StopRunning: true, Reasons: []governor.Reason{governor.ReasonUserPresent},
				Ceilings: map[governor.Resource]int{governor.ResourceCPU: 50, governor.ResourceMemory: 50, governor.ResourceDiskIO: 50, governor.ResourceNetwork: 50},
			},
			Machine: startgate.MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30},
		})
		if err == nil {
			t.Fatal("the start gate started work while the user was present")
		}
	})
}

// The Policy is revoked after the Lease was reserved and before it was signed. No
// signed Lease leaves LSE-1, the Attempt is closed, and the fence is not reused.
func TestSignedPolicyRevokedBeforeTheLeaseIsSigned(t *testing.T) {
	w := newWorld(t)
	c := newChain(w)
	task := w.submit("sp-revoked")
	reserved := c.place(task, "worker-a", throughReserve)

	current, err := w.state.LoadCurrentPolicy(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := policystate.Revoke(current, current.Epoch+1, w.clock.now, "emergency")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.state.CompareAndSwapPolicy(w.ctx, current.Epoch, revoked); err != nil {
		t.Fatal(err)
	}

	emitted, err := c.emitter().Emit(w.ctx, leaseissue.EmitRequest{LeaseID: reserved.placement.lease})
	if !errors.Is(err, leaseissue.ErrLeaseAborted) {
		t.Fatalf("Emit() error = %v, want %v", err, leaseissue.ErrLeaseAborted)
	}
	if emitted.Record.Status() == leaseemission.StatusEmitted || len(emitted.SignedLease) != 0 {
		t.Fatalf("a Lease was signed after the policy was revoked: %+v", emitted)
	}
	if got := w.attemptRecord(reserved.placement.attempt); got.Phase != attemptstate.AttemptPhaseClosed ||
		got.CloseReason != attemptstate.CloseReasonPolicyRevoked {
		t.Fatalf("Attempt = %+v, want closed as policy_revoked", got)
	}

	// A new policy lets the Task be placed again, under a fence above the one that
	// was never signed.
	next, err := policystate.NewActive(revoked.Epoch+1, "policy-2", w.clock.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.state.CompareAndSwapPolicy(w.ctx, revoked.Epoch, next); err != nil {
		t.Fatal(err)
	}
	w.policy = next
	w.clock.advance(2 * time.Second)
	retry := c.place(task, "worker-b", throughStart)
	if retry.placement.fence <= reserved.placement.fence {
		t.Fatalf("fence %d reuses or lowers %d", retry.placement.fence, reserved.placement.fence)
	}
	claim, outcome := c.run(retry, producesOutput("hello"), sourceFunc(quiet))
	if outcome.Status != executor.StatusCompleted {
		t.Fatalf("outcome = %+v", outcome)
	}
	w.report("worker-b", claim)
	if result := w.verifyAndAccept(retry.placement, "hello"); result.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result = %+v", result)
	}
	if got := w.task(task); got.Phase != taskstate.TaskPhaseTerminal {
		t.Fatalf("Task = %+v", got)
	}
	w.evidenceOf(task).assertExplainable(t)
}
