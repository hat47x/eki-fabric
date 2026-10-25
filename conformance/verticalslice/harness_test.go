package verticalslice_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	queuesqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/sqlite"
	statesqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/agent/claim"
	"github.com/hat47x/eki-fabric/internal/agent/claimclient"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	"github.com/hat47x/eki-fabric/internal/control/recoveryadmission"
	"github.com/hat47x/eki-fabric/internal/control/resultacceptance"
	workerserver "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// The vertical slice test composes the real Control Plane and Agent components
// over a durable SQLite store. It is not a network test and not a signing test:
// the Placement decision, the WorkOrder signature and the Worker's acceptance of
// it are synthetic inputs (see README.md). Everything after the Lease is real.

const trustDomain = "example.org"

var executorDigest = contract.SHA256([]byte("registered executor"))

// manualClock is the only clock in the test. It starts at the real time so that
// the context deadlines the executor derives from it stay meaningful.
type manualClock struct{ now time.Time }

func (c *manualClock) Now() time.Time { return c.now }

func (c *manualClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// world is one Control Plane and any number of Workers.
type world struct {
	t              *testing.T
	ctx            context.Context
	dir            string
	clock          *manualClock
	state          *statesqlite.Store
	queues         map[string]*queuesqlite.Store
	attempt        map[ids.TaskID][]ids.AttemptID
	awaiting       map[ids.TaskID]attemptstate.AttemptRecord
	recoveryPolicy recovery.Policy
	terms          recoveryadmission.Terms
	policy         policystate.State
	seq            int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{
		t: t, ctx: context.Background(), dir: t.TempDir(),
		clock:    &manualClock{now: time.Now().UTC().Truncate(time.Second)},
		queues:   map[string]*queuesqlite.Store{},
		attempt:  map[ids.TaskID][]ids.AttemptID{},
		awaiting: map[ids.TaskID]attemptstate.AttemptRecord{},
		recoveryPolicy: recovery.Policy{
			Revision: 1, BaseBackoff: time.Second, MaxBackoff: time.Minute, Window: time.Minute,
			MaxReleasesPerWindow: 4, MaxTransferBytesPerWindow: 1 << 30, MaxComputeMillisPerWindow: 1 << 30,
			PermitTTL: 10 * time.Minute,
		},
		terms: recoveryadmission.Terms{
			Retryable: true, MaxAttempts: 3, Priority: recovery.PriorityNormal,
			EstimatedTransferBytes: 1 << 20, EstimatedComputeMillis: 100,
		},
	}
	w.openState()
	policy, err := policystate.NewActive(1, "policy-1", w.clock.now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.state.CompareAndSwapPolicy(w.ctx, 0, policy); err != nil {
		t.Fatal(err)
	}
	w.policy = policy
	t.Cleanup(w.closeAll)
	return w
}

func (w *world) statePath() string { return filepath.Join(w.dir, "control.db") }

func (w *world) openState() {
	w.t.Helper()
	s, err := statesqlite.Open(w.ctx, w.statePath())
	if err != nil {
		w.t.Fatal(err)
	}
	w.state = s
}

func (w *world) closeAll() {
	if w.state != nil {
		_ = w.state.Close()
		w.state = nil
	}
	for name, q := range w.queues {
		_ = q.Close()
		delete(w.queues, name)
	}
}

// restart stops every process and starts them again over the same files. Nothing
// is carried over in memory: what the test sees afterwards is what was durable.
func (w *world) restart() {
	w.t.Helper()
	names := make([]string, 0, len(w.queues))
	for name := range w.queues {
		names = append(names, name)
	}
	w.closeAll()
	w.openState()
	for _, name := range names {
		w.workerQueueStore(name)
	}
}

func (w *world) workerQueueStore(worker string) *queuesqlite.Store {
	w.t.Helper()
	if q, ok := w.queues[worker]; ok {
		return q
	}
	q, err := queuesqlite.Open(w.ctx, filepath.Join(w.dir, worker+".db"))
	if err != nil {
		w.t.Fatal(err)
	}
	w.queues[worker] = q
	return q
}

// --- the Client side ---

func (w *world) admissionService(maxOutstanding uint32) *admission.Service {
	w.t.Helper()
	svc, err := admission.New(admission.Config{
		Store: w.state,
		Policy: admission.Policy{
			ApprovedExecutors: []string{"dev.example.transform"},
			PerTask: admission.ResourceCeiling{
				CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeSeconds: 3600,
				OutputBytesMax: 1 << 30, NetworkBytesMax: 1 << 30, TemporaryDiskBytes: 1 << 30,
			},
			MaxAttempts: 3, MaxOutstandingTasks: maxOutstanding,
		},
		Now: w.clock.Now,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return svc
}

// taskBody is a valid request. mutate edits the decoded JSON, so a test can add
// exactly the field a malicious Client would add.
func taskBody(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	body := map[string]any{
		"schema":     "eki.task-request/v1",
		"executor":   map[string]any{"id": "dev.example.transform", "version_constraint": "^1"},
		"inputs":     []any{},
		"parameters": map[string]any{"mode": "normalize"},
		"resources": map[string]any{
			"cpu_millis": 1000, "memory_bytes": 268435456, "wall_time_seconds": 300,
			"output_bytes_max": 1048576, "network_bytes_max": 1048576,
		},
		"reliability": map[string]any{
			"retryable": true, "preemptible": true, "checkpointable": false, "deterministic": true,
			"max_attempts": 3, "effect_mode": "none",
		},
		"priority": "normal",
	}
	if mutate != nil {
		mutate(body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// submit admits a Task for alice and makes it ready for placement. The step from
// admitted to ready is synthetic: nothing implements it yet.
func (w *world) submit(key string) ids.TaskID {
	w.t.Helper()
	accepted, err := w.admissionService(10).Submit(w.ctx, admission.Principal{ID: "alice"}, key, taskBody(w.t, nil))
	if err != nil {
		w.t.Fatalf("Submit() error = %v", err)
	}
	task := w.task(accepted.TaskID)
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = w.clock.now
	if err := w.state.CompareAndSwapTask(w.ctx, task.ID, task.Revision, ready); err != nil {
		w.t.Fatal(err)
	}
	return accepted.TaskID
}

func (w *world) task(id ids.TaskID) taskstate.TaskRecord {
	w.t.Helper()
	task, err := w.state.LoadTask(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return task
}

func (w *world) attemptRecord(id ids.AttemptID) attemptstate.AttemptRecord {
	w.t.Helper()
	a, err := w.state.LoadAttempt(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return a
}

func (w *world) leaseRecord(id ids.LeaseID) leasestate.LeaseRecord {
	w.t.Helper()
	l, err := w.state.LoadLease(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return l
}

// --- placement and dispatch (synthetic decisions, real state) ---

// placement is one Attempt that holds a Lease on one Worker.
type placement struct {
	task    ids.TaskID
	attempt ids.AttemptID
	lease   ids.LeaseID
	worker  string
	fence   uint64
}

// tryPlace creates the next Attempt of a Task, binds it to worker and reserves a
// Lease. The Placement decision is a synthetic input. The state changes go
// through the same public store operations production code uses.
func (w *world) tryPlace(taskID ids.TaskID, worker string) (placement, error) {
	w.t.Helper()
	w.seq++
	task := w.task(taskID)
	attempt, err := w.nextAttempt(taskID)
	if err != nil {
		return placement{}, err
	}

	placing := attempt
	placing.Phase, placing.WorkerIdentity = attemptstate.AttemptPhasePlacing, worker
	placing.Revision++
	placing.UpdatedAt = w.clock.now
	if err := w.state.CompareAndSwapAttempt(w.ctx, attempt.ID, attempt.Revision, placing); err != nil {
		w.t.Fatal(err)
	}
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		w.t.Fatal(err)
	}
	reserved, err := w.state.ReserveLease(w.ctx, statestore.LeaseReservation{
		LeaseID: leaseID, TaskID: taskID, TaskIRDigest: task.TaskIRDigest,
		ExpectedTaskRevision: task.Revision, AttemptID: attempt.ID, ExpectedAttemptRevision: placing.Revision,
		WorkerIdentity: worker, PlacementTrustDomain: trustDomain, PlacementAuthorityPrincipal: "placement-1",
		PlacementDecisionID: "decision-" + string(rune('a'+w.seq)), PlacementPayloadDigest: contract.SHA256([]byte("placement")),
		PlacementExpiresAt: w.clock.now.Add(time.Hour), PolicyRevision: w.policy.Revision, ExpectedPolicyEpoch: w.policy.Epoch,
		IssuedAt: w.clock.now.Add(-time.Second), ExpiresAt: w.clock.now.Add(5 * time.Minute),
	})
	if err != nil {
		return placement{}, err
	}
	assigned := w.attemptRecord(attempt.ID)
	dispatched := assigned
	dispatched.Phase = attemptstate.AttemptPhaseDispatched
	dispatched.Revision++
	dispatched.UpdatedAt = w.clock.now
	if err := w.state.CompareAndSwapAttempt(w.ctx, attempt.ID, assigned.Revision, dispatched); err != nil {
		w.t.Fatal(err)
	}
	return placement{task: taskID, attempt: attempt.ID, lease: reserved.Lease.ID, worker: worker, fence: reserved.Lease.FenceEpoch}, nil
}

func (w *world) place(taskID ids.TaskID, worker string) placement {
	w.t.Helper()
	p, err := w.tryPlace(taskID, worker)
	if err != nil {
		w.t.Fatalf("place on %s: %v", worker, err)
	}
	return p
}

// --- the Worker side ---

type runnerFunc func(ctx context.Context, spec executor.Spec) (executor.Result, error)

func (f runnerFunc) Run(ctx context.Context, spec executor.Spec) (executor.Result, error) {
	return f(ctx, spec)
}

type sourceFunc func(ctx context.Context) (governor.Decision, error)

func (f sourceFunc) Next(ctx context.Context) (governor.Decision, error) { return f(ctx) }

// quiet is a Local Governor that never intervenes.
func quiet(ctx context.Context) (governor.Decision, error) {
	<-ctx.Done()
	return governor.Decision{}, ctx.Err()
}

// userReturns is a Local Governor that stops the work at once, as it does when
// the person using the device comes back.
func userReturns(context.Context) (governor.Decision, error) {
	return governor.Decision{StopRunning: true, Reasons: []governor.Reason{governor.ReasonUserPresent}}, nil
}

// producesOutput is a Runner that finishes normally with the given output.
func producesOutput(output string) runnerFunc {
	return func(context.Context, executor.Spec) (executor.Result, error) {
		return executor.Result{ExitCode: 0, OutputBytes: uint64(len(output)), OutputDigest: contract.SHA256([]byte(output))}, nil
	}
}

// runsUntilStopped is a Runner that works until the Supervisor stops it.
func runsUntilStopped(ctx context.Context, _ executor.Spec) (executor.Result, error) {
	<-ctx.Done()
	return executor.Result{}, ctx.Err()
}

// execute runs the Attempt on its Worker under the Supervisor and returns the
// Claim the Agent builds from the Outcome. The Claim is not delivered yet.
func (w *world) execute(p placement, runner executor.Runner, source executor.DecisionSource) (resultclaim.Claim, executor.Outcome) {
	w.t.Helper()
	task := w.task(p.task)
	supervisor, err := executor.New(executor.Config{Clock: w.clock, StopGrace: 2 * time.Second})
	if err != nil {
		w.t.Fatal(err)
	}
	nonce := []byte("0123456789abcdef")
	outcome, err := supervisor.Execute(w.ctx, executor.Spec{
		TaskID: p.task, AttemptID: p.attempt, OrderDecisionID: "workorder-" + p.attempt.String(),
		ExecutorDigest: executorDigest,
		Limits:         decisionpayload.ResourceLimits{CPUMillis: 1000, MemoryBytes: 1 << 28, WallTimeMillis: 300_000, OutputBytesMaximum: 1 << 20},
		Sandbox:        decisionpayload.SandboxPolicy{Profile: "sandbox/container/v1"},
		Deadline:       w.clock.now.Add(10 * time.Minute),
		Nonce:          nonce,
	}, runner, source)
	if err != nil {
		w.t.Fatalf("Execute() error = %v", err)
	}
	built, err := claim.Build(outcome, claim.Attempt{
		TaskID: p.task, TaskIRDigest: task.TaskIRDigest, AttemptID: p.attempt, WorkerIdentity: p.worker,
		LeaseID: p.lease, FenceEpoch: p.fence, WorkOrderDecision: "workorder-" + p.attempt.String(),
		ExecutorDigest: executorDigest, Nonce: nonce,
	})
	if err != nil {
		w.t.Fatalf("claim.Build() error = %v", err)
	}
	return built, outcome
}

// workerPlane is the Control Plane's door for Worker reports, as the transport
// would expose it after authenticating the Worker.
func (w *world) workerPlane() *workerserver.Server {
	w.t.Helper()
	accept, err := claimaccept.New(claimaccept.Config{Attempts: w.state, Leases: w.state, Claims: w.state, Clock: w.clock})
	if err != nil {
		w.t.Fatal(err)
	}
	plane, err := claimplane.New(accept)
	if err != nil {
		w.t.Fatal(err)
	}
	api, err := workerserver.New(workerserver.Config{Claims: plane})
	if err != nil {
		w.t.Fatal(err)
	}
	return api
}

func (w *world) principal(worker string) workerplane.Principal {
	return workerplane.Principal{WorkerIdentity: worker, TrustDomain: trustDomain}
}

// queueFor builds the Worker's durable queue over its own file, wired to the
// Control Plane through the real client and Worker Plane.
func (w *world) queueFor(worker string, acks *[]workerplane.ClaimAck) *claimqueue.Queue {
	w.t.Helper()
	api := recordingAPI{ClaimClient: w.workerPlane(), acks: acks}
	client, err := claimclient.New(api, w.principal(worker))
	if err != nil {
		w.t.Fatal(err)
	}
	queue, err := claimqueue.New(claimqueue.Config{
		Store: w.workerQueueStore(worker), Sender: client, Clock: w.clock,
		BaseDelay: time.Second, MaxDelay: time.Minute,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return queue
}

// enqueue puts the Claim in the Worker's durable queue without sending it.
func (w *world) enqueue(worker string, c resultclaim.Claim) {
	w.t.Helper()
	var acks []workerplane.ClaimAck
	if _, err := w.queueFor(worker, &acks).Enqueue(w.ctx, c); err != nil {
		w.t.Fatal(err)
	}
}

// deliver sends everything due in the Worker's queue and returns the answers.
func (w *world) deliver(worker string) []workerplane.ClaimAck {
	w.t.Helper()
	var acks []workerplane.ClaimAck
	if _, err := w.queueFor(worker, &acks).DeliverDue(w.ctx, 8); err != nil {
		w.t.Fatalf("DeliverDue() error = %v", err)
	}
	return acks
}

// report puts the Claim in the Worker's durable queue and delivers it. It returns
// the Control Plane's answer.
func (w *world) report(worker string, c resultclaim.Claim) workerplane.ClaimAck {
	w.t.Helper()
	w.enqueue(worker, c)
	acks := w.deliver(worker)
	if len(acks) != 1 {
		w.t.Fatalf("delivered %d answers, want 1", len(acks))
	}
	return acks[0]
}

type recordingAPI struct {
	workerplane.ClaimClient
	acks *[]workerplane.ClaimAck
}

func (r recordingAPI) SubmitClaim(ctx context.Context, p workerplane.Principal, c resultclaim.Claim) (workerplane.ClaimAck, error) {
	ack, err := r.ClaimClient.SubmitClaim(ctx, p, c)
	if err == nil {
		*r.acks = append(*r.acks, ack)
	}
	return ack, err
}

// --- recovery ---

// terms are what the admitted Task says about retrying. They mirror the request
// that taskBody builds (retryable, three attempts), and a test can change them.
type fixedTerms struct{ w *world }

func (f fixedTerms) RecoveryTerms(context.Context, ids.TaskID) (recoveryadmission.Terms, error) {
	return f.w.terms, nil
}

type fixedPolicy struct{ w *world }

func (f fixedPolicy) RecoveryPolicy(context.Context) (recovery.Policy, error) {
	return f.w.recoveryPolicy, nil
}

func (w *world) recovery() *recoveryadmission.Service {
	w.t.Helper()
	svc, err := recoveryadmission.New(recoveryadmission.Config{
		Store: w.state, Contracts: fixedTerms{w}, Policies: fixedPolicy{w}, Clock: w.clock,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return svc
}

// nextAttempt returns the Attempt that waits to be placed. After a recovery that is
// the one the Permit created; otherwise it is a new first Attempt.
func (w *world) nextAttempt(taskID ids.TaskID) (attemptstate.AttemptRecord, error) {
	if waiting, ok := w.awaiting[taskID]; ok {
		delete(w.awaiting, taskID)
		return waiting, nil
	}
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
		return attemptstate.AttemptRecord{}, err
	}
	w.attempt[taskID] = append(w.attempt[taskID], attemptID)
	return attempt, nil
}

// recoverAttempt takes a closed Attempt through the recovery boundary: the retry
// is queued from the durable Attempt, waits out its backoff, is released within
// the window budget, and the Permit creates the next Attempt. The new Attempt
// waits to be placed. Nothing is kept in test memory except that it exists.
func (w *world) recoverAttempt(taskID ids.TaskID, previous ids.AttemptID) attemptstate.AttemptRecord {
	w.t.Helper()
	svc := w.recovery()
	intake, err := svc.Intake(w.ctx, previous)
	if err != nil || intake.Hold != nil {
		w.t.Fatalf("Intake() = %+v, %v", intake, err)
	}
	if w.clock.now.Before(intake.Pending.NotBefore) {
		w.clock.now = intake.Pending.NotBefore
	}
	released, err := svc.Release(w.ctx)
	if err != nil {
		w.t.Fatalf("Release() error = %v", err)
	}
	found := false
	for _, permit := range released.Permits {
		if permit.PreviousAttemptID() == previous {
			found = true
		}
	}
	if !found {
		w.t.Fatalf("the retry of %s was not released: %+v", previous, released)
	}
	return w.admitRecovery(taskID, previous)
}

// admitRecovery creates the next Attempt from the Permit stored for previous.
func (w *world) admitRecovery(taskID ids.TaskID, previous ids.AttemptID) attemptstate.AttemptRecord {
	w.t.Helper()
	next, err := w.recovery().Admit(w.ctx, previous)
	if err != nil {
		w.t.Fatalf("Admit() error = %v", err)
	}
	w.attempt[taskID] = append(w.attempt[taskID], next.ID)
	w.awaiting[taskID] = next
	return next
}

// --- verification and acceptance ---

type policySource struct{ policy resultacceptance.ResultPolicy }

func (p policySource) ResultPolicy(context.Context, ids.TaskID) (resultacceptance.ResultPolicy, error) {
	return p.policy, nil
}

// producers gives every Worker its own principal and operator, so that two
// different Workers are independent of each other and of any verifier.
type producers struct{}

func (producers) ProducerDimensions(_ context.Context, worker string) (map[resultaccept.Dimension]string, error) {
	return map[resultaccept.Dimension]string{
		resultaccept.DimensionPrincipal:      "principal-of-" + worker,
		resultaccept.DimensionOperator:       "operator-of-" + worker,
		resultaccept.DimensionExecutorDigest: executorDigest.Hex(),
	}, nil
}

func (w *world) acceptance() *resultacceptance.Service {
	w.t.Helper()
	svc, err := resultacceptance.New(resultacceptance.Config{
		Attempts: w.state, Tasks: w.state, Claims: w.state, Results: w.state, Rejections: w.state,
		Policies: policySource{resultacceptance.ResultPolicy{
			Policy: resultaccept.Policy{
				Level: resultaccept.LevelRA2, Threshold: 2,
				DistinctDimensions: []resultaccept.Dimension{resultaccept.DimensionPrincipal, resultaccept.DimensionOperator},
				RejectAuthorities:  []string{"authority-1"},
			},
			Output: resultaccept.OutputMechanical,
		}},
		Producers: producers{}, Clock: w.clock,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return svc
}

// reproduced is one independent verifier that re-derived the claimed output.
func reproduced(p placement, verifier, output string) resultaccept.Verification {
	return resultaccept.Verification{
		VerifierID: verifier, TaskID: p.task.String(), AttemptID: p.attempt.String(),
		Check: resultaccept.CheckReproduced, ObservedDigest: contract.SHA256([]byte(output)).Hex(),
		Dimensions: map[resultaccept.Dimension]string{
			resultaccept.DimensionPrincipal:      "principal-of-" + verifier,
			resultaccept.DimensionOperator:       "operator-of-" + verifier,
			resultaccept.DimensionExecutorDigest: contract.SHA256([]byte("executor-of-" + verifier)).Hex(),
		},
	}
}

func (w *world) verifyAndAccept(p placement, output string) resultacceptance.Result {
	w.t.Helper()
	result, err := w.acceptance().Accept(w.ctx, resultacceptance.Request{
		AttemptID: p.attempt,
		Verifications: []resultaccept.Verification{
			reproduced(p, "verifier-1", output), reproduced(p, "verifier-2", output),
		},
	})
	if err != nil {
		w.t.Fatalf("Accept() error = %v", err)
	}
	return result
}
