package assignment_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	replayguardmemory "github.com/hat47x/eki-fabric/internal/adapters/reference/replayguard/memory"
	statesqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/agent/assignment"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/agent/trustanchor"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/spi/replayguard"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type source struct{ list []workerplane.Assignment }

func (s *source) ListAssignments(context.Context, workerplane.Principal) ([]workerplane.Assignment, error) {
	return s.list, nil
}

// world is one Control Plane store with real signed material.
type world struct {
	t      *testing.T
	now    time.Time
	auth   *signedchain.Authorities
	state  *statesqlite.Store
	policy policystate.State
	n      int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	s, err := statesqlite.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	policy, err := policystate.NewActive(1, "policy-1", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompareAndSwapPolicy(context.Background(), 0, policy); err != nil {
		t.Fatal(err)
	}
	return &world{t: t, now: now, auth: signedchain.New(t, now.Add(-2*time.Hour), 17), state: s, policy: policy}
}

// placeSandboxWith signs an Attempt for worker as the Control Plane clock read
// at, with the given key set and SandboxPolicy, and returns the assignment the
// Control Plane would deliver.
func (w *world) placeSandboxWith(auth *signedchain.Authorities, worker string, at time.Time, sandbox decisionpayload.SandboxPolicy) workerplane.Assignment {
	w.t.Helper()
	w.n++
	p := auth.PlaceSandbox(w.t, w.state, w.policy, w.n, worker, at, sandbox)
	return workerplane.Assignment{
		AttemptID: p.AttemptID.String(), LeaseID: p.LeaseID.String(),
		LeaseEnvelope: p.SignedLease, WorkOrderEnvelope: p.SignedWorkOrder,
	}
}

// placeWith signs an Attempt for worker as the Control Plane clock read at,
// with the given key set and the reference SandboxPolicy.
func (w *world) placeWith(auth *signedchain.Authorities, worker string, at time.Time) workerplane.Assignment {
	w.t.Helper()
	w.n++
	p := auth.Place(w.t, w.state, w.policy, w.n, worker, at)
	return workerplane.Assignment{
		AttemptID: p.AttemptID.String(), LeaseID: p.LeaseID.String(),
		LeaseEnvelope: p.SignedLease, WorkOrderEnvelope: p.SignedWorkOrder,
	}
}

// place signs an Attempt with the reference SandboxPolicy.
func (w *world) placeSandbox(worker string, at time.Time, sandbox decisionpayload.SandboxPolicy) workerplane.Assignment {
	return w.placeSandboxWith(w.auth, worker, at, sandbox)
}

// place signs an Attempt with the reference key set and SandboxPolicy.
func (w *world) place(worker string, at time.Time) workerplane.Assignment {
	return w.placeWith(w.auth, worker, at)
}

type agent struct {
	svc     *assignment.Service
	src     *source
	started []assignment.Ready
	gov     *governor.Decision
	machine startgate.MachineProfile
}

// defaultMachine returns m, or the default profile when zero. A test that
// never sets machine keeps the old fixed profile.
func defaultMachine(m startgate.MachineProfile) startgate.MachineProfile {
	if m == (startgate.MachineProfile{}) {
		return startgate.MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30}
	}
	return m
}

type options struct {
	anchor    func(*trustanchor.File)
	executors []assignment.ExecutorApproval
	replay    replayguard.Guard
	clock     *clock
	machine   startgate.MachineProfile
}

func (w *world) agent(worker string, opt options) *agent {
	w.t.Helper()
	doc := w.auth.TrustAnchor(w.now, "policy-1")
	if opt.anchor != nil {
		opt.anchor(&doc)
	}
	anchor, err := trustanchor.Build(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	clk := opt.clock
	if clk == nil {
		clk = &clock{now: w.now.Add(time.Second)}
	}
	replay := opt.replay
	if replay == nil {
		replay, err = replayguardmemory.New(clk, 1024)
		if err != nil {
			w.t.Fatal(err)
		}
	}
	execs := opt.executors
	if execs == nil {
		execs = []assignment.ExecutorApproval{{
			DecisionID: signedchain.ExecutorApproval.DecisionID, PayloadDigest: signedchain.ExecutorApproval.PayloadDigest,
			ExecutorDigest: signedchain.ExecutorApproval.ExecutorDigest,
		}}
	}
	a := &agent{src: &source{}}
	decision := assignment.StaticGovernor(50)()
	a.gov = &decision
	svc, err := assignment.New(assignment.Config{
		Source: a.src, Principal: workerplane.Principal{WorkerIdentity: worker, TrustDomain: "example.org"},
		Anchor: anchor, Replay: replay, Clock: clk, Executors: execs,
		Governor: func() governor.Decision { return *a.gov },
		Machine:  defaultMachine(opt.machine),
		Handler: assignment.HandlerFunc(func(_ context.Context, r assignment.Ready) error {
			a.started = append(a.started, r)
			return nil
		}),
	})
	if err != nil {
		w.t.Fatal(err)
	}
	a.svc = svc
	return a
}

func (a *agent) poll(t *testing.T, list ...workerplane.Assignment) assignment.Result {
	t.Helper()
	a.src.list = list
	results, err := a.svc.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one", results)
	}
	return results[0]
}

func flipped(b []byte) []byte {
	out := append([]byte(nil), b...)
	out[len(out)/2] ^= 0xff
	return out
}

func want(t *testing.T, got assignment.Result, outcome assignment.Outcome, reason string) {
	t.Helper()
	if got.Outcome != outcome || (reason != "" && got.Reason != reason) {
		t.Fatalf("result = %+v, want %s %q", got, outcome, reason)
	}
}

func TestAGenuineAssignmentIsVerifiedGatedAndHandedOver(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{})
	asg := w.place("worker-a", w.now)
	r := a.poll(t, asg)
	want(t, r, assignment.OutcomeReady, "")
	if len(a.started) != 1 || a.started[0].AttemptID.String() != asg.AttemptID || a.started[0].FenceEpoch != r.FenceEpoch || r.FenceEpoch == 0 {
		t.Fatalf("handed over %+v", a.started)
	}
	if got := a.started[0].Plan.Limits(); got.CPUMillis != signedchain.Limits.CPUMillis || got.MemoryBytes != signedchain.Limits.MemoryBytes {
		t.Fatalf("limits = %+v", got)
	}
	// The same bytes delivered again are not started twice.
	if results, err := a.svc.Poll(context.Background()); err != nil || len(results) != 0 || len(a.started) != 1 {
		t.Fatalf("second poll = %+v, %v, started %d", results, err, len(a.started))
	}
}

func TestATamperedLeaseOrWorkOrderIsRefusedAndDoesNotSpendTheNonce(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{})
	asg := w.place("worker-a", w.now)

	badLease := asg
	badLease.LeaseEnvelope = flipped(asg.LeaseEnvelope)
	want(t, a.poll(t, badLease), assignment.OutcomeRejected, "")

	badOrder := asg
	badOrder.WorkOrderEnvelope = flipped(asg.WorkOrderEnvelope)
	want(t, a.poll(t, badOrder), assignment.OutcomeRejected, "")
	if len(a.started) != 0 {
		t.Fatal("a tampered assignment was started")
	}
	// The genuine one still works: the refused bytes did not burn the nonce.
	want(t, a.poll(t, asg), assignment.OutcomeReady, "")
}

func TestAnExpiredLeaseIsRefused(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{})
	r := a.poll(t, w.place("worker-a", w.now.Add(-10*time.Minute)))
	want(t, r, assignment.OutcomeRejected, "lease_expired")
	if len(a.started) != 0 {
		t.Fatal("an expired Lease was started")
	}
}

func TestMaterialForAnotherWorkerIsRefused(t *testing.T) {
	w := newWorld(t)
	b := w.agent("worker-b", options{})
	r := b.poll(t, w.place("worker-a", w.now))
	want(t, r, assignment.OutcomeRejected, "lease_subject_mismatch")
	if len(b.started) != 0 {
		t.Fatal("worker-b started what was issued to worker-a")
	}
}

func TestALeaseWithAnotherWorkersWorkOrderIsRefused(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{})
	mine := w.place("worker-a", w.now)
	theirs := w.place("worker-b", w.now)
	mixed := mine
	mixed.WorkOrderEnvelope = theirs.WorkOrderEnvelope
	want(t, a.poll(t, mixed), assignment.OutcomeRejected, "work_order_subject_mismatch")
	// A WorkOrder of another Attempt of the same Worker is not bound to this Lease either.
	other := w.place("worker-a", w.now)
	mixed = mine
	mixed.WorkOrderEnvelope = other.WorkOrderEnvelope
	want(t, a.poll(t, mixed), assignment.OutcomeRejected, "work_order_lease_mismatch")
	if len(a.started) != 0 {
		t.Fatal("a mixed assignment was started")
	}
}

func TestAReplayedNonceIsNotStartedAgainEvenAfterARestart(t *testing.T) {
	w := newWorld(t)
	clk := &clock{now: w.now.Add(time.Second)}
	guard, err := replayguardmemory.New(clk, 1024)
	if err != nil {
		t.Fatal(err)
	}
	asg := w.place("worker-a", w.now)
	first := w.agent("worker-a", options{replay: guard, clock: clk})
	want(t, first.poll(t, asg), assignment.OutcomeReady, "")
	// A new process with the same durable guard sees the same bytes again.
	second := w.agent("worker-a", options{replay: guard, clock: clk})
	want(t, second.poll(t, asg), assignment.OutcomeReplayed, "nonce_already_used")
	if len(second.started) != 0 {
		t.Fatal("a replayed WorkOrder was started")
	}
}

func TestMaterialSignedByAnotherKeySetIsRefused(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{})
	forger := signedchain.New(t, w.now.Add(-2*time.Hour), 99)
	r := a.poll(t, w.placeWith(forger, "worker-a", w.now))
	want(t, r, assignment.OutcomeRejected, "lease_signature_invalid")
	if len(a.started) != 0 {
		t.Fatal("a forged Lease was started")
	}
}

func TestAWorkerAcceptsOnlyItsOwnPolicyRevisionAndApprovedExecutors(t *testing.T) {
	w := newWorld(t)
	asg := w.place("worker-a", w.now)

	other := w.agent("worker-a", options{anchor: func(f *trustanchor.File) { f.PolicyRevision = "policy-2" }})
	want(t, other.poll(t, asg), assignment.OutcomeRejected, "lease_policy_revision_mismatch")

	unapproved := w.agent("worker-a", options{executors: []assignment.ExecutorApproval{{
		DecisionID: "executor-approval-other", PayloadDigest: signedchain.ExecutorApproval.PayloadDigest,
		ExecutorDigest: signedchain.ExecutorApproval.ExecutorDigest,
	}}})
	want(t, unapproved.poll(t, asg), assignment.OutcomeRejected, "executor_not_approved")
}

func TestAStaleTrustBundleFailsClosed(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{anchor: func(f *trustanchor.File) { f.ValidatedAt = w.now.Add(-3 * time.Hour) }})
	want(t, a.poll(t, w.place("worker-a", w.now)), assignment.OutcomeRejected, "lease_trust_bundle_stale")
}

func TestAnAssignmentWhoseHintsDisagreeWithTheSignedLeaseIsRefused(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{})
	asg := w.place("worker-a", w.now)
	asg.AttemptID = "atm_00000000000000000000000000000000"
	want(t, a.poll(t, asg), assignment.OutcomeRejected, "assignment_hint_mismatch")
}

func TestTheLocalGovernorCanDeferWithoutSpendingTheNonce(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{})
	asg := w.place("worker-a", w.now)
	stopped := governor.Decision{StopRunning: true}
	a.gov = &stopped
	want(t, a.poll(t, asg), assignment.OutcomeDeferred, "local_governor_not_accepting")
	open := assignment.StaticGovernor(50)()
	a.gov = &open
	want(t, a.poll(t, asg), assignment.OutcomeReady, "")
}

func TestTheStartGateStopsAStartTheMachineCannotHold(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{})
	zero := assignment.StaticGovernor(0)()
	a.gov = &zero
	r := a.poll(t, w.place("worker-a", w.now))
	want(t, r, assignment.OutcomeRejected, "start_gate_no_capacity")
	if len(a.started) != 0 {
		t.Fatal("started without capacity")
	}
}

// A start the machine cannot hold must leave the nonce unspent: the Control
// Plane then re-issues the WorkOrder with a fresh nonce and the same Attempt
// starts after the machine frees up, instead of the Assignment being lost as
// a replay (#296 item 2).
func TestAStartGateRefusalLeavesTheNonceUnspent(t *testing.T) {
	w := newWorld(t)
	a := w.agent("worker-a", options{})
	zero := assignment.StaticGovernor(0)()
	a.gov = &zero
	asg := w.place("worker-a", w.now)
	want(t, a.poll(t, asg), assignment.OutcomeRejected, "start_gate_no_capacity")
	open := assignment.StaticGovernor(50)()
	a.gov = &open
	// The same bytes poll again: if the first poll had spent the nonce, this
	// would be a replay and the Attempt would never start.
	want(t, a.poll(t, asg), assignment.OutcomeReady, "")
	if len(a.started) != 1 {
		t.Fatalf("started %d times after the gate reopened", len(a.started))
	}
}

// A WorkOrder whose SandboxPolicy requires a GPU used to fail the start gate
// after Accept had already spent the nonce, so the same bytes could never
// start (#296 item 2). With the gate running before the nonce claim, the
// refusal is a rejection with the nonce unspent, and the assignment starts
// once the machine reports the GPU.
func TestAWorkOrderRequiringAGPUStartsOnceTheGovernorAllowsIt(t *testing.T) {
	w := newWorld(t)
	// The machine reports 2 GPUs from the start; the refusal below comes from
	// the Governor ceilings only.
	a := w.agent("worker-a", options{machine: startgate.MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30, GPUDevices: 2}})
	sandbox := decisionpayload.SandboxPolicy{
		Profile: signedchain.Sandbox.Profile,
		GPU:     &decisionpayload.GPULimit{DeviceCount: 1, MinimumVRAMBytes: 16 << 30},
	}
	asg := w.placeSandbox("worker-a", w.now, sandbox)
	zero := assignment.StaticGovernor(0)()
	a.gov = &zero
	want(t, a.poll(t, asg), assignment.OutcomeRejected, "start_gate_no_capacity")
	open := assignment.StaticGovernor(50)()
	a.gov = &open
	want(t, a.poll(t, asg), assignment.OutcomeReady, "")
	if len(a.started) != 1 {
		t.Fatalf("started %d times", len(a.started))
	}
	if plan := a.started[0].Plan; plan.Sandbox().GPU == nil || plan.Sandbox().GPU.DeviceCount != 1 {
		t.Fatalf("the started plan lost the GPU limit: %+v", plan.Sandbox().GPU)
	}
}

func TestConfigurationFailsClosed(t *testing.T) {
	if _, err := trustanchor.Load(""); !errors.Is(err, trustanchor.ErrInvalid) {
		t.Fatalf("empty path: %v", err)
	}
	if _, err := trustanchor.Load(filepath.Join(t.TempDir(), "missing.json")); !errors.Is(err, trustanchor.ErrInvalid) {
		t.Fatalf("missing file: %v", err)
	}
	w := newWorld(t)
	doc := w.auth.TrustAnchor(w.now, "policy-1")
	doc.Keys = doc.Keys[:1] // PLC-1 only: no Lease or WorkOrder key
	if _, err := trustanchor.Build(doc); !errors.Is(err, trustanchor.ErrInvalid) {
		t.Fatalf("no Lease/WorkOrder key: %v", err)
	}
	if _, err := assignment.New(assignment.Config{}); !errors.Is(err, assignment.ErrInvalidConfig) {
		t.Fatalf("empty config: %v", err)
	}
}

func TestAnAssignmentBehindAnAcceptedFenceIsSuperseded(t *testing.T) {
	w := newWorld(t)
	clk := &clock{now: w.now.Add(time.Second)}
	guard, err := replayguardmemory.New(clk, 1024)
	if err != nil {
		t.Fatal(err)
	}
	asg := w.place("worker-a", w.now)
	a := w.agent("worker-a", options{replay: guard, clock: clk})
	want(t, a.poll(t, asg), assignment.OutcomeReady, "")
	task := a.started[0].Admission.TaskID().String()

	// The Control Plane re-places the Task and the Worker accepts the newer
	// Attempt. The fence record is the durable state a restart preserves,
	// which the in-memory done map cannot keep.
	if ok, err := guard.Fence(context.Background(), task, 42, clk.now.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("seed fence = %v, %v", ok, err)
	}
	// A restarted Worker refuses the older Attempt as superseded: its fence
	// is behind the accepted one, and the refusal repeats on every poll
	// instead of the Attempt being lost or started.
	restarted := w.agent("worker-a", options{replay: guard, clock: clk})
	want(t, restarted.poll(t, asg), assignment.OutcomeRejected, "work_order_superseded")
	want(t, restarted.poll(t, asg), assignment.OutcomeRejected, "work_order_superseded")
	if len(restarted.started) != 0 {
		t.Fatal("a superseded Attempt was started")
	}
}
