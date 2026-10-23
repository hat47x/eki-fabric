package verticalslice_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	replaymemory "github.com/hat47x/eki-fabric/internal/adapters/reference/replayguard/memory"
	"github.com/hat47x/eki-fabric/internal/agent/claim"
	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/leaseverify"
	"github.com/hat47x/eki-fabric/internal/core/placementverify"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/workorderaccept"
	"github.com/hat47x/eki-fabric/internal/core/workorderissue"
	"github.com/hat47x/eki-fabric/internal/core/workorderverify"
)

// The signed chain replaces the synthetic Placement, Lease and WorkOrder inputs of
// the first scenarios with the real Authorities: PLC-1 signs a Placement
// Approval, LSE-1 reserves and signs a Lease from it, WOR-1 signs a WorkOrder
// from the Lease, and the Worker verifies each one and decides locally whether to
// start. Keys, signing and verification are the reference implementations. The
// Executor Approval (EXE-1) is still a synthetic reference.

const (
	plcPrincipal = "control-plane/plc-1"
	lsePrincipal = "control-plane/lse-1"
	worPrincipal = "control-plane/wor-1"
	keyPolicy    = "key-policy-1"
)

// authorities holds the three signing keys and everything that verifies them.
type authorities struct {
	t         *testing.T
	registry  *decisiontrust.Registry
	signer    *decisiontrust.ReferenceSoftwareSigner
	codec     *cosecbor.Codec
	payload   *payloadcbor.Decoder
	enc       fxcbor.EncMode
	boundary  *decisiontrust.VerifierBoundary
	placement *placementverify.Verifier
}

// newAuthorities builds a key set. A different seed gives a different set of keys
// under the same key IDs and principals, which is what a forger would have.
func newAuthorities(t *testing.T, now time.Time, seed byte) *authorities {
	t.Helper()
	codec, err := cosecbor.New()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := payloadcbor.New()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := fxcbor.CoreDetEncOptions().EncMode()
	if err != nil {
		t.Fatal(err)
	}
	keyTime := now.Add(-time.Hour)
	registry := decisiontrust.NewRegistry(keyTime)
	signer, err := decisiontrust.NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatal(err)
	}
	for i, k := range []struct {
		id        string
		role      authority.Role
		principal string
	}{
		{"kid-plc-1", authority.RolePlacementAuthorization, plcPrincipal},
		{"kid-lse-1", authority.RoleLease, lsePrincipal},
		{"kid-wor-1", authority.RoleWorkOrder, worPrincipal},
	} {
		keySeed := make([]byte, ed25519.SeedSize)
		keySeed[0], keySeed[1] = seed, byte(i)
		private := ed25519.NewKeyFromSeed(keySeed)
		record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
			ID: decisiontrust.KeyID(k.id), AuthorityRole: k.role, AuthorityPrincipal: k.principal,
			TrustDomain: trustDomain, Algorithm: decisiontrust.AlgorithmEdDSA,
			KeyType: decisiontrust.KeyTypeOKP, Curve: decisiontrust.CurveEd25519, PublicKey: private.Public(),
			SigningNotBefore: keyTime, SigningNotAfter: now.Add(24 * time.Hour), PolicyRevision: keyPolicy,
		}, keyTime)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range []func() error{
			func() error { return registry.Register(record, keyTime) },
			func() error { return registry.Activate(record.ID(), keyTime) },
			func() error { return registry.SetPrimary(record.ID(), keyTime) },
			func() error { return signer.RegisterPrivateKey(record.ID(), private) },
		} {
			if err := step(); err != nil {
				t.Fatal(err)
			}
		}
	}
	policy, err := decisiontrust.NewVerificationPolicy(decisiontrust.VerificationPolicyConfig{
		MaxClockSkew: 5 * time.Second, MaxTrustBundleStaleness: time.Hour,
		AllowedAlgorithms: []decisiontrust.Algorithm{decisiontrust.AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionPlacementApproval: 10 * time.Minute,
			authority.DecisionLease:             10 * time.Minute,
			authority.DecisionWorkOrder:         10 * time.Minute,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := decisiontrust.NewVerifierBoundary(registry, policy)
	if err != nil {
		t.Fatal(err)
	}
	placement, err := placementverify.New(placementverify.Config{
		Codec: codec, Payload: payload, Boundary: boundary, TrustDomain: trustDomain,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &authorities{
		t: t, registry: registry, signer: signer, codec: codec, payload: payload, enc: enc,
		boundary: boundary, placement: placement,
	}
}

// placementSpec is what PLC-1 approves.
type placementSpec struct {
	decisionID string
	task       ids.TaskID
	taskIR     contract.Digest
	attempt    ids.AttemptID
	worker     string
	policy     string
	issuedAt   time.Time
	expiresAt  time.Time
}

// signPlacement produces the Placement Approval a PLC-1 would sign.
func (a *authorities) signPlacement(s placementSpec) []byte {
	a.t.Helper()
	digestBytes, err := hex.DecodeString(s.taskIR.Hex())
	if err != nil {
		a.t.Fatal(err)
	}
	payload := a.must(a.enc.Marshal(map[uint64]any{
		1: uint64(1),
		2: string(authority.DecisionPlacementApproval),
		3: s.decisionID,
		4: map[uint64]any{1: string(authority.RolePlacementAuthorization), 2: plcPrincipal},
		5: uint64(s.issuedAt.UnixMilli()),
		6: uint64(s.expiresAt.UnixMilli()),
		7: s.policy,
		8: map[uint64]any{
			1: s.task.String(),
			2: map[uint64]any{1: uint64(1), 2: digestBytes},
			3: s.attempt.String(),
			4: s.worker,
		},
	}))
	protected := a.must(a.enc.Marshal(map[int64]any{
		1:      int64(-8),
		2:      []int64{decisiontrust.COSEHeaderLabelEKIProfile},
		4:      []byte("kid-plc-1"),
		-70000: decisiontrust.SignedDecisionProfileV1,
	}))
	sigStructure, err := a.codec.EncodeSignature1(protected, payload)
	if err != nil {
		a.t.Fatal(err)
	}
	signed, err := a.signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key: decisiontrust.SigningRequest{
			TrustDomain: trustDomain, AuthorityRole: authority.RolePlacementAuthorization,
			AuthorityPrincipal: plcPrincipal, DecisionKind: authority.DecisionPlacementApproval,
			Algorithm: decisiontrust.AlgorithmEdDSA,
		},
		PolicyRevision: keyPolicy, SigStructure: sigStructure,
	}, s.issuedAt)
	if err != nil {
		a.t.Fatal(err)
	}
	out := []byte{0xd2, 0x84}
	out = append(out, a.must(a.enc.Marshal(protected))...)
	out = append(out, 0xa0)
	out = append(out, a.must(a.enc.Marshal(payload))...)
	out = append(out, a.must(a.enc.Marshal(signed.Signature))...)
	return out
}

func (a *authorities) must(v []byte, err error) []byte {
	a.t.Helper()
	if err != nil {
		a.t.Fatal(err)
	}
	return v
}

// liveBundle is a Trust Bundle that is always freshly validated.
type liveBundle struct{ clock *manualClock }

func (b liveBundle) CurrentTrustBundle(context.Context) (decisiontrust.TrustBundleMetadata, error) {
	return b.metadata(), nil
}

func (b liveBundle) metadata() decisiontrust.TrustBundleMetadata {
	return decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: b.clock.now}
}

// chain is the signed Authority chain over one world.
type chain struct {
	w      *world
	auth   *authorities
	bundle liveBundle
	replay map[string]*replaymemory.Guard
}

func newChain(w *world) *chain {
	return &chain{
		w: w, auth: newAuthorities(w.t, w.clock.now, 17),
		bundle: liveBundle{clock: w.clock}, replay: map[string]*replaymemory.Guard{},
	}
}

func (c *chain) leaseService() *leaseissue.Service {
	c.w.t.Helper()
	svc, err := leaseissue.New(leaseissue.Config{
		Verifier: c.auth.placement, State: c.w.state, TrustBundle: c.bundle,
		Clock: c.w.clock, MaxLifetime: 5 * time.Minute,
	})
	if err != nil {
		c.w.t.Fatal(err)
	}
	return svc
}

func (c *chain) emitter() *leaseissue.Emitter {
	c.w.t.Helper()
	e, err := leaseissue.NewEmitter(leaseissue.EmitterConfig{
		State: c.w.state, Signer: c.auth.signer, Keys: c.auth.registry, Verifier: c.auth.boundary,
		Payload: c.auth.payload, Envelope: c.auth.codec, TrustBundle: c.bundle, Clock: c.w.clock,
		TrustDomain: trustDomain, AuthorityPrincipal: lsePrincipal,
		Algorithm: decisiontrust.AlgorithmEdDSA, KeyPolicyRevision: keyPolicy,
	})
	if err != nil {
		c.w.t.Fatal(err)
	}
	return e
}

func (c *chain) issuer() *workorderissue.Issuer {
	c.w.t.Helper()
	i, err := workorderissue.New(workorderissue.Config{
		State: c.w.state, Signer: c.auth.signer, Keys: c.auth.registry, Verifier: c.auth.boundary,
		Payload: c.auth.payload, Envelope: c.auth.codec, TrustBundle: c.bundle, Clock: c.w.clock,
		Nonce: rand.Reader, TrustDomain: trustDomain, AuthorityPrincipal: worPrincipal,
		Algorithm: decisiontrust.AlgorithmEdDSA, KeyPolicyRevision: keyPolicy, MaxLifetime: 5 * time.Minute,
	})
	if err != nil {
		c.w.t.Fatal(err)
	}
	return i
}

// workerSide is what one Worker runs locally to decide whether to start.
type workerSide struct {
	lease  *leaseverify.Verifier
	accept *workorderaccept.Service
	gate   *startgate.Gate
}

func (c *chain) worker(name string) workerSide {
	c.w.t.Helper()
	leaseVerifier, err := leaseverify.New(leaseverify.Config{
		Codec: c.auth.codec, Payload: c.auth.payload, Boundary: c.auth.boundary,
		TrustDomain: trustDomain, WorkerIdentity: name,
	})
	if err != nil {
		c.w.t.Fatal(err)
	}
	orderVerifier, err := workorderverify.New(workorderverify.Config{
		Codec: c.auth.codec, Payload: c.auth.payload, Boundary: c.auth.boundary,
		TrustDomain: trustDomain, WorkerIdentity: name,
	})
	if err != nil {
		c.w.t.Fatal(err)
	}
	guard, ok := c.replay[name]
	if !ok {
		guard, err = replaymemory.New(c.w.clock, 1024)
		if err != nil {
			c.w.t.Fatal(err)
		}
		c.replay[name] = guard
	}
	accept, err := workorderaccept.New(workorderaccept.Config{Verifier: orderVerifier, Replay: guard, Clock: c.w.clock})
	if err != nil {
		c.w.t.Fatal(err)
	}
	gate, err := startgate.New(startgate.Config{Clock: c.w.clock})
	if err != nil {
		c.w.t.Fatal(err)
	}
	return workerSide{lease: leaseVerifier, accept: accept, gate: gate}
}

// executorApproval is the EXE-1 reference. It is synthetic: nothing signs it yet.
var executorApproval = workorderissue.ExecutorApproval{
	DecisionID:     "executor-approval-1",
	PayloadDigest:  contract.SHA256([]byte("executor approval payload")),
	ExecutorDigest: executorDigest,
}

var workOrderLimits = decisionpayload.ResourceLimits{
	CPUMillis: 1000, MemoryBytes: 1 << 28, TemporaryDiskBytes: 1 << 20,
	WallTimeMillis: 300_000, OutputBytesMaximum: 1 << 20, NetworkBytesMax: 1 << 20,
}

var workOrderSandbox = decisionpayload.SandboxPolicy{Profile: "sandbox/container/v1"}

// signed is one Attempt placed through the whole signed chain.
type signed struct {
	placement placement
	plan      startgate.Plan

	signedPlacement []byte
	signedLease     []byte
	signedWorkOrder []byte
	leaseEmission   leaseemission.Record
	admission       workorderaccept.Admission
}

// stage names how far down the chain an Attempt was taken, so a test can stop
// before a step and attack it.
type stage int

const (
	throughReserve stage = iota
	throughEmit
	throughWorkOrder
	throughStart
)

// startAttempt creates the next Attempt of the Task and binds it to the Worker. This
// is the Scheduler's proposal: it carries no authority. The Placement Approval that
// follows is what authorizes it.
func (c *chain) startAttempt(taskID ids.TaskID, worker string) attemptstate.AttemptRecord {
	c.w.t.Helper()
	w := c.w
	task := w.task(taskID)
	attemptID, err := ids.NewAttemptID()
	if err != nil {
		w.t.Fatal(err)
	}
	attempt, err := attemptstate.New(attemptID, taskID, task.TaskIRDigest, uint64(len(w.attempt[taskID])+1), w.clock.now)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.state.CreateAttempt(w.ctx, attempt); err != nil {
		w.t.Fatal(err)
	}
	w.attempt[taskID] = append(w.attempt[taskID], attemptID)
	placing := attempt
	placing.Phase, placing.WorkerIdentity = attemptstate.AttemptPhasePlacing, worker
	placing.Revision++
	placing.UpdatedAt = w.clock.now
	if err := w.state.CompareAndSwapAttempt(w.ctx, attempt.ID, attempt.Revision, placing); err != nil {
		w.t.Fatal(err)
	}
	return placing
}

// approve signs the Placement Approval for an Attempt as PLC-1 would.
func (c *chain) approve(attempt attemptstate.AttemptRecord, worker string) []byte {
	c.w.t.Helper()
	now := c.w.clock.now
	return c.auth.signPlacement(placementSpec{
		decisionID: "placement-" + attempt.ID.String(), task: attempt.TaskID, taskIR: attempt.TaskIRDigest,
		attempt: attempt.ID, worker: worker, policy: c.w.policy.Revision,
		issuedAt: now.Add(-time.Second), expiresAt: now.Add(5 * time.Minute),
	})
}

// place takes one Attempt of the Task down the signed chain up to the given stage.
func (c *chain) place(taskID ids.TaskID, worker string, upTo stage) *signed {
	c.w.t.Helper()
	w := c.w
	attempt := c.startAttempt(taskID, worker)
	out := &signed{signedPlacement: c.approve(attempt, worker)}

	reserved, err := c.leaseService().Reserve(w.ctx, leaseissue.Request{SignedPlacement: out.signedPlacement, RequestedLifetime: 3 * time.Minute})
	if err != nil {
		w.t.Fatalf("Reserve() error = %v", err)
	}
	out.placement = placement{task: taskID, attempt: attempt.ID, lease: reserved.Lease.ID, worker: worker, fence: reserved.Lease.FenceEpoch}
	if upTo == throughReserve {
		return out
	}

	emitted, err := c.emitter().Emit(w.ctx, leaseissue.EmitRequest{LeaseID: reserved.Lease.ID})
	if err != nil || emitted.Record.Status() != leaseemission.StatusEmitted {
		w.t.Fatalf("Emit() = %+v, %v", emitted.Record, err)
	}
	out.signedLease, out.leaseEmission = emitted.SignedLease, emitted.Record
	if upTo == throughEmit {
		return out
	}

	order, err := c.issuer().Issue(w.ctx, workorderissue.IssueRequest{
		LeaseID: reserved.Lease.ID, WorkerIdentity: worker,
		ExecutorApproval: executorApproval, Limits: workOrderLimits, Sandbox: workOrderSandbox,
	})
	if err != nil {
		w.t.Fatalf("Issue() error = %v", err)
	}
	out.signedWorkOrder = order.SignedWorkOrder
	if upTo == throughWorkOrder {
		return out
	}

	out.admission, out.plan = c.startOnWorker(out)
	c.dispatch(out.placement)
	return out
}

// startOnWorker is everything the Worker does with the bytes it received: verify
// the Lease and the Placement Approval, accept the WorkOrder against them, and ask
// the start gate for an effective plan.
func (c *chain) startOnWorker(s *signed) (workorderaccept.Admission, startgate.Plan) {
	c.w.t.Helper()
	admission, err := c.tryStartOnWorker(s, s.placement.worker)
	if err != nil {
		c.w.t.Fatalf("the Worker did not start: %v", err)
	}
	plan, err := c.plan(admission, s.placement.worker)
	if err != nil {
		c.w.t.Fatalf("the start gate refused: %v", err)
	}
	return admission, plan
}

// tryStartOnWorker returns the first thing that stops a Worker from accepting the
// WorkOrder, so that a test can attack each step.
func (c *chain) tryStartOnWorker(s *signed, worker string) (workorderaccept.Admission, error) {
	c.w.t.Helper()
	side := c.worker(worker)
	now, bundle := c.w.clock.now, c.bundle.metadata()
	leaseVerified, err := side.lease.Verify(leaseverify.Request{Raw: s.signedLease, Now: now, TrustBundle: bundle})
	if err != nil {
		return workorderaccept.Admission{}, err
	}
	placementVerified, err := c.auth.placement.Verify(placementverify.Request{Raw: s.signedPlacement, Now: now, TrustBundle: bundle})
	if err != nil {
		return workorderaccept.Admission{}, err
	}
	lease := leaseVerified.Lease()
	return side.accept.Accept(c.w.ctx, workorderaccept.Request{
		Raw: s.signedWorkOrder, TrustBundle: bundle,
		Lease: workorderaccept.LeaseEvidence{
			DecisionID: lease.DecisionID(), PayloadDigest: s.leaseEmission.PayloadDigest(),
			TaskID: lease.TaskID(), TaskIRDigest: lease.TaskIRDigest(), AttemptID: lease.AttemptID(),
			WorkerIdentity: lease.WorkerIdentity(), FenceEpoch: lease.FenceEpoch(), ExpiresAt: lease.ExpiresAt(),
		},
		Placement: workorderaccept.PlacementEvidence{
			DecisionID: placementVerified.Approval().DecisionID(), PayloadDigest: placementVerified.PayloadDigest(),
			TaskID: lease.TaskID(), TaskIRDigest: lease.TaskIRDigest(), AttemptID: lease.AttemptID(),
			WorkerIdentity: lease.WorkerIdentity(),
		},
		Executor: workorderaccept.ExecutorEvidence{
			DecisionID: executorApproval.DecisionID, PayloadDigest: executorApproval.PayloadDigest,
			ExecutorDigest: executorApproval.ExecutorDigest,
		},
	})
}

// plan asks the Worker's start gate, under a Local Governor that allows half of a
// modest machine.
func (c *chain) plan(admission workorderaccept.Admission, worker string) (startgate.Plan, error) {
	return c.worker(worker).gate.Check(startgate.Request{
		Admission: admission,
		Governor: governor.Decision{AcceptNewLeases: true, Ceilings: map[governor.Resource]int{
			governor.ResourceCPU: 50, governor.ResourceMemory: 50, governor.ResourceDiskIO: 50, governor.ResourceNetwork: 50,
		}},
		Machine: startgate.MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30},
	})
}

// dispatch records that the Lease and WorkOrder were handed to the Worker. The
// delivery itself is not exercised here.
func (c *chain) dispatch(p placement) {
	c.w.t.Helper()
	assigned := c.w.attemptRecord(p.attempt)
	dispatched := assigned
	dispatched.Phase = attemptstate.AttemptPhaseDispatched
	dispatched.Revision++
	dispatched.UpdatedAt = c.w.clock.now
	if err := c.w.state.CompareAndSwapAttempt(c.w.ctx, p.attempt, assigned.Revision, dispatched); err != nil {
		c.w.t.Fatal(err)
	}
}

// run executes the Attempt on its Worker from the plan the start gate produced, and
// returns the claim built from the WorkOrder's real binding.
func (c *chain) run(s *signed, runner executor.Runner, source executor.DecisionSource) (resultclaim.Claim, executor.Outcome) {
	c.w.t.Helper()
	w := c.w
	supervisor, err := executor.New(executor.Config{Clock: w.clock, StopGrace: 2 * time.Second})
	if err != nil {
		w.t.Fatal(err)
	}
	outcome, err := supervisor.Execute(w.ctx, executor.SpecFromPlan(s.plan), runner, source)
	if err != nil {
		w.t.Fatalf("Execute() error = %v", err)
	}
	task := w.task(s.placement.task)
	built, err := claim.Build(outcome, claim.Attempt{
		TaskID: s.placement.task, TaskIRDigest: task.TaskIRDigest, AttemptID: s.placement.attempt,
		WorkerIdentity: s.placement.worker, LeaseID: s.placement.lease, FenceEpoch: s.admission.FenceEpoch(),
		WorkOrderDecision: s.plan.OrderDecisionID(), ExecutorDigest: s.plan.ExecutorDigest(), Nonce: s.plan.Nonce(),
	})
	if err != nil {
		w.t.Fatalf("claim.Build() error = %v", err)
	}
	return built, outcome
}
