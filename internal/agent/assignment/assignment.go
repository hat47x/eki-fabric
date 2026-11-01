// Package assignment is the Worker's side of assignment delivery in the
// process-level TEST PROFILE (not the Worker Protocol #198).
//
// The Control Plane hands the Worker the signed Lease and WorkOrder of each
// Attempt bound to it. Everything in them is hostile until the Worker has
// checked it against its own trust anchor and its own state:
//
//  1. leaseverify: the Lease is for this Worker, signed by a key the trust
//     anchor lists for the Lease role, and inside its validity window with a
//     fresh Trust Bundle;
//  2. the Lease names the Policy revision the Worker accepts;
//  3. workorderverify and workorderaccept: the WorkOrder is for this Worker,
//     signed by a WOR-1 key, bound to exactly that Lease (decision and payload
//     digest), names an Executor Approval from the Worker's own approved list,
//     and its Task's fence epoch is not behind a higher fence the Worker
//     already accepted, and its one-time nonce is claimed in the Worker's
//     durable replay guard;
//  4. startgate: the Local Governor and the machine allow a start, and the
//     effective limits are the smaller of the grant and the local ceiling.
//
// Only an assignment that passes all four reaches the Handler, as a Ready value
// the Handler may start from. This package never starts an Executor.
//
// What it does not check: the Placement Approval. The Control Plane does not
// store its signed bytes (only its decision ID and digest), so the Worker has
// nothing independent to verify. The placement evidence passed to
// workorderaccept is taken from the verified WorkOrder itself, which makes that
// one comparison vacuous here. It is an open question, not a claim of
// verification.
package assignment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/agent/trustanchor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseverify"
	"github.com/hat47x/eki-fabric/internal/core/workorderaccept"
	"github.com/hat47x/eki-fabric/internal/core/workorderverify"
	"github.com/hat47x/eki-fabric/internal/spi/replayguard"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

var ErrInvalidConfig = errors.New("invalid assignment configuration")

// Outcome is what happened to one assignment.
type Outcome string

const (
	// OutcomeReady: verified, admitted and start-gated, and given to the Handler.
	OutcomeReady Outcome = "ready"
	// OutcomeReplayed: the WorkOrder's nonce was already spent here. This is the
	// normal answer when the Control Plane delivers the same bytes again.
	OutcomeReplayed Outcome = "replayed"
	// OutcomeDeferred: the Local Governor does not accept new work now. Nothing
	// was consumed, so a later poll can still start it.
	OutcomeDeferred Outcome = "deferred"
	// OutcomeRejected: refused. Reason says which check.
	OutcomeRejected Outcome = "rejected"
)

// Result is the outcome of one assignment, for the log and the event record.
type Result struct {
	AttemptID  string
	LeaseID    string
	Outcome    Outcome
	Reason     string
	FenceEpoch uint64
	Plan       startgate.Plan
}

// ExecutorApproval is an Executor Approval the Worker is willing to run under,
// from the Worker's own configuration.
type ExecutorApproval struct {
	DecisionID     string
	PayloadDigest  contract.Digest
	ExecutorDigest contract.Digest
}

// Ready is a verified, admitted and start-gated assignment.
type Ready struct {
	AttemptID  ids.AttemptID
	LeaseID    ids.LeaseID
	FenceEpoch uint64
	Admission  workorderaccept.Admission
	Plan       startgate.Plan
}

// Handler is the executor interface: it receives only assignments that passed
// every check. A returned error is reported as a rejection of that assignment.
type Handler interface {
	Start(ctx context.Context, ready Ready) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, ready Ready) error

func (f HandlerFunc) Start(ctx context.Context, ready Ready) error { return f(ctx, ready) }

type Clock interface{ Now() time.Time }

type Config struct {
	Source    workerplane.AssignmentSource
	Principal workerplane.Principal
	Anchor    *trustanchor.Anchor
	Replay    replayguard.Guard
	Clock     Clock
	Executors []ExecutorApproval
	// Governor returns the Local Governor's current decision.
	Governor func() governor.Decision
	Machine  startgate.MachineProfile
	Handler  Handler
	Logger   *slog.Logger
}

// Service polls for assignments and runs the checks.
type Service struct {
	cfg    Config
	codec  *cosecbor.Codec
	lease  *leaseverify.Verifier
	order  *workorderverify.Verifier
	accept *workorderaccept.Service
	gate   *startgate.Gate
	log    *slog.Logger

	mu   sync.Mutex
	done map[string]struct{} // Attempts already handed over or already spent
	// The in-memory done map dedupes per Attempt within one process. Across
	// restarts, the durable dedupe is the replay guard: the nonce claim for a
	// redelivery of the same Attempt, and the per-Task fence record for an
	// Attempt a higher fence already replaced.
}

func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Source == nil:
		return nil, fmt.Errorf("%w: assignment source is required", ErrInvalidConfig)
	case cfg.Principal.WorkerIdentity == "":
		return nil, fmt.Errorf("%w: Worker identity is required", ErrInvalidConfig)
	case cfg.Anchor == nil:
		return nil, fmt.Errorf("%w: trust anchor is required", ErrInvalidConfig)
	case cfg.Replay == nil:
		return nil, fmt.Errorf("%w: durable replay guard is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	case len(cfg.Executors) == 0:
		return nil, fmt.Errorf("%w: at least one approved Executor is required", ErrInvalidConfig)
	case cfg.Governor == nil:
		return nil, fmt.Errorf("%w: Local Governor is required", ErrInvalidConfig)
	case cfg.Handler == nil:
		return nil, fmt.Errorf("%w: handler is required", ErrInvalidConfig)
	}
	codec, err := cosecbor.New()
	if err != nil {
		return nil, err
	}
	payload, err := payloadcbor.New()
	if err != nil {
		return nil, err
	}
	lease, err := leaseverify.New(leaseverify.Config{
		Codec: codec, Payload: payload, Boundary: cfg.Anchor.Boundary,
		TrustDomain: cfg.Anchor.TrustDomain, WorkerIdentity: cfg.Principal.WorkerIdentity,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	order, err := workorderverify.New(workorderverify.Config{
		Codec: codec, Payload: payload, Boundary: cfg.Anchor.Boundary,
		TrustDomain: cfg.Anchor.TrustDomain, WorkerIdentity: cfg.Principal.WorkerIdentity,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	accept, err := workorderaccept.New(workorderaccept.Config{Verifier: order, Replay: cfg.Replay, Clock: cfg.Clock})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	gate, err := startgate.New(startgate.Config{Clock: cfg.Clock})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{cfg: cfg, codec: codec, lease: lease, order: order, accept: accept, gate: gate, log: log, done: map[string]struct{}{}}, nil
}

// Poll fetches the assignments once and checks each. A transport error is
// returned; a refused assignment is a Result, not an error. An Attempt that was
// already handed over or already spent is reported once and then skipped.
func (s *Service) Poll(ctx context.Context) ([]Result, error) {
	list, err := s.cfg.Source.ListAssignments(ctx, s.cfg.Principal)
	if err != nil {
		return nil, err
	}
	var results []Result
	for _, a := range list {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		s.mu.Lock()
		_, seen := s.done[a.AttemptID]
		s.mu.Unlock()
		if seen {
			continue
		}
		r := s.check(ctx, a)
		if r.Outcome == OutcomeReady || r.Outcome == OutcomeReplayed {
			s.mu.Lock()
			s.done[a.AttemptID] = struct{}{}
			s.mu.Unlock()
		}
		results = append(results, r)
	}
	return results, nil
}

func (s *Service) check(ctx context.Context, a workerplane.Assignment) Result {
	r := Result{AttemptID: a.AttemptID, LeaseID: a.LeaseID}
	reject := func(reason string) Result {
		r.Outcome, r.Reason = OutcomeRejected, reason
		return r
	}
	// A revoked Lease (ADR-0030 §2.2): the delivery listing still names the
	// Attempt so the Worker learns the revocation, but the material must not
	// be started or re-verified into a start. The signed bytes are not
	// consulted: revocation is a fact of the listing. An Attempt already
	// decided never reaches here — Poll skips it before the checks.
	if a.LeaseState == workerplane.LeaseStateRevoked {
		return reject("lease_revoked")
	}
	now := s.cfg.Clock.Now().UTC()
	bundle := s.cfg.Anchor.TrustBundle()

	// 1. The Lease.
	leaseVerified, err := s.lease.Verify(leaseverify.Request{Raw: a.LeaseEnvelope, Now: now, TrustBundle: bundle})
	if err != nil {
		return reject("lease_" + reasonOf(err))
	}
	lease := leaseVerified.Lease()
	// 2. The Policy revision this Worker accepts.
	if lease.PolicyRevision() != s.cfg.Anchor.PolicyRevision {
		return reject("lease_policy_revision_mismatch")
	}
	// The identifiers beside the bytes are only hints; they must agree with the
	// signed Lease or the delivery is inconsistent.
	if a.AttemptID != lease.AttemptID().String() || a.LeaseID != lease.LeaseID().String() {
		return reject("assignment_hint_mismatch")
	}
	r.FenceEpoch = lease.FenceEpoch()
	envelope, err := s.codec.DecodeSign1(a.LeaseEnvelope)
	if err != nil {
		return reject("lease_malformed")
	}
	leaseDigest := contract.SHA256(envelope.Payload())

	// 3. The WorkOrder. It is verified once to learn which Executor Approval it
	// names, so the Worker's own list can supply the evidence, and again inside
	// Accept, which also claims the nonce.
	orderVerified, err := s.order.Verify(workorderverify.Request{Raw: a.WorkOrderEnvelope, Now: now, TrustBundle: bundle})
	if err != nil {
		return reject("work_order_" + reasonOf(err))
	}
	order := orderVerified.Order()
	if order.PolicyRevision() != s.cfg.Anchor.PolicyRevision {
		return reject("work_order_policy_revision_mismatch")
	}
	executor, ok := s.approvedExecutor(order.ExecutorApprovalRef().DecisionID(), order.ExecutorApprovalRef().PayloadDigest(), order.ExecutorDigest())
	if !ok {
		return reject("executor_not_approved")
	}

	// A Local Governor that accepts no new work defers the start before the
	// nonce is spent, so the same WorkOrder can still run later.
	gov := s.cfg.Governor()
	if gov.StopRunning || !gov.AcceptNewLeases {
		r.Outcome, r.Reason = OutcomeDeferred, "local_governor_not_accepting"
		return r
	}

	// The start gate runs before the nonce claim: a start the machine cannot
	// hold is refused with the nonce still unspent, so the Control Plane can
	// re-issue the WorkOrder with a fresh nonce instead of the Assignment
	// being lost as a replay.
	preliminary, err := s.accept.Preview(ctx, workorderaccept.Request{
		Raw: a.WorkOrderEnvelope, TrustBundle: bundle,
		Lease: workorderaccept.LeaseEvidence{
			DecisionID: lease.DecisionID(), PayloadDigest: leaseDigest, TaskID: lease.TaskID(),
			TaskIRDigest: lease.TaskIRDigest(), AttemptID: lease.AttemptID(),
			WorkerIdentity: lease.WorkerIdentity(), FenceEpoch: lease.FenceEpoch(), ExpiresAt: lease.ExpiresAt(),
		},
		Placement: workorderaccept.PlacementEvidence{
			DecisionID: order.PlacementRef().DecisionID(), PayloadDigest: order.PlacementRef().PayloadDigest(),
			TaskID: lease.TaskID(), TaskIRDigest: lease.TaskIRDigest(), AttemptID: lease.AttemptID(),
			WorkerIdentity: lease.WorkerIdentity(),
		},
		Executor: workorderaccept.ExecutorEvidence{
			DecisionID: executor.DecisionID, PayloadDigest: executor.PayloadDigest, ExecutorDigest: executor.ExecutorDigest,
		},
	})
	if err != nil {
		return reject(acceptReason(err))
	}
	// Capacity and slot shortages describe this endpoint right now, not the
	// Assignment: defer it so a later poll can start it, and leave the nonce
	// unspent (ADR-0031 §2.4). Everything else the gate refuses is a rejection.
	if _, err := s.gate.Check(startgate.Request{Admission: preliminary, Governor: gov, Machine: s.cfg.Machine}); err != nil {
		if errors.Is(err, startgate.ErrNoCapacity) || errors.Is(err, startgate.ErrNoSlot) {
			r.Outcome, r.Reason = OutcomeDeferred, "start_gate_"+gateReason(err)
			return r
		}
		return reject("start_gate_" + gateReason(err))
	}

	admission, err := s.accept.Accept(ctx, workorderaccept.Request{
		Raw: a.WorkOrderEnvelope, TrustBundle: bundle,
		Lease: workorderaccept.LeaseEvidence{
			DecisionID: lease.DecisionID(), PayloadDigest: leaseDigest, TaskID: lease.TaskID(),
			TaskIRDigest: lease.TaskIRDigest(), AttemptID: lease.AttemptID(),
			WorkerIdentity: lease.WorkerIdentity(), FenceEpoch: lease.FenceEpoch(), ExpiresAt: lease.ExpiresAt(),
		},
		Placement: workorderaccept.PlacementEvidence{
			DecisionID: order.PlacementRef().DecisionID(), PayloadDigest: order.PlacementRef().PayloadDigest(),
			TaskID: lease.TaskID(), TaskIRDigest: lease.TaskIRDigest(), AttemptID: lease.AttemptID(),
			WorkerIdentity: lease.WorkerIdentity(),
		},
		Executor: workorderaccept.ExecutorEvidence{
			DecisionID: executor.DecisionID, PayloadDigest: executor.PayloadDigest, ExecutorDigest: executor.ExecutorDigest,
		},
	})
	switch {
	case errors.Is(err, workorderaccept.ErrReplay):
		r.Outcome, r.Reason = OutcomeReplayed, "nonce_already_used"
		return r
	case err != nil:
		return reject(acceptReason(err))
	}

	// 4. The start gate on the admitted work. The gate itself ran on the
	// Preview before the nonce claim; this second Check re-runs it on the
	// admitted values, which are the same evidence, so a refusal here is a
	// race with the clock, not a lost Assignment.
	plan, err := s.gate.Check(startgate.Request{Admission: admission, Governor: gov, Machine: s.cfg.Machine})
	if err != nil {
		return reject("start_gate_" + gateReason(err))
	}
	ready := Ready{AttemptID: lease.AttemptID(), LeaseID: lease.LeaseID(), FenceEpoch: lease.FenceEpoch(), Admission: admission, Plan: plan}
	if err := s.cfg.Handler.Start(ctx, ready); err != nil {
		return reject("handler_failed")
	}
	r.Outcome, r.Plan = OutcomeReady, plan
	return r
}

func (s *Service) approvedExecutor(decision string, payload, digest contract.Digest) (ExecutorApproval, bool) {
	for _, e := range s.cfg.Executors {
		if e.DecisionID == decision && e.PayloadDigest == payload && e.ExecutorDigest == digest {
			return e, true
		}
	}
	return ExecutorApproval{}, false
}

// reasonOf is the stable, lower-case verification reason of an error.
func reasonOf(err error) string {
	if reason, ok := decisiontrust.FailureReason(err); ok {
		return strings.ToLower(string(reason))
	}
	return "invalid"
}

func acceptReason(err error) string {
	switch {
	case errors.Is(err, workorderaccept.ErrLeaseMismatch):
		return "work_order_lease_mismatch"
	case errors.Is(err, workorderaccept.ErrLeaseExpired):
		return "lease_expired"
	case errors.Is(err, workorderaccept.ErrPlacementMismatch):
		return "work_order_placement_mismatch"
	case errors.Is(err, workorderaccept.ErrExecutorMismatch):
		return "work_order_executor_mismatch"
	case errors.Is(err, workorderaccept.ErrSuperseded):
		return "work_order_superseded"
	case errors.Is(err, workorderaccept.ErrFenceUnknown):
		return "fence_claim_unknown"
	case errors.Is(err, workorderaccept.ErrReplayUnknown):
		return "nonce_claim_unknown"
	}
	if _, ok := decisiontrust.FailureReason(err); ok {
		return "work_order_" + reasonOf(err)
	}
	return "work_order_not_accepted"
}

func gateReason(err error) string {
	switch {
	case errors.Is(err, startgate.ErrGovernorStopped):
		return "governor_stopped"
	case errors.Is(err, startgate.ErrNotAccepting):
		return "not_accepting"
	case errors.Is(err, startgate.ErrNoCapacity):
		return "no_capacity"
	case errors.Is(err, startgate.ErrNoSlot):
		return "no_slot"
	case errors.Is(err, startgate.ErrMachineUnknown):
		return "machine_unknown"
	case errors.Is(err, startgate.ErrAdmissionExpired):
		return "admission_expired"
	}
	return "refused"
}

// StaticGovernor is a fixed Local Governor decision for the test profile: it
// accepts new work and allows the same percentage of every resource, GPU
// included. The real Local Governor (agent/governor) is not wired into the
// Agent process yet.
func StaticGovernor(percent int) func() governor.Decision {
	return func() governor.Decision {
		return governor.Decision{AcceptNewLeases: true, Ceilings: map[governor.Resource]int{
			governor.ResourceCPU: percent, governor.ResourceMemory: percent,
			governor.ResourceDiskIO: percent, governor.ResourceNetwork: percent,
			governor.ResourceGPU: percent,
		}}
	}
}
