// Package dispatch is the Control Plane's placement loop: one tick takes
// Ready Tasks one step further down the PLC-1, LSE-1, and WOR-1 chain.
//
// The loop records nothing of its own. Every state change goes through the
// State Store with the same compare-and-swap discipline the API paths use, so
// a tick that dies halfway leaves the Task exactly where it was, and the next
// tick re-plans it from scratch. A Task is claimed by exactly one tick at a
// time because the Attempt creation and the phase transitions it performs are
// compare-and-swap operations.
//
// The first candidate set for an Attempt is fixed here (CREATED -> PLACING).
// Re-planning an Attempt that is already PLACING keeps its Worker; the loop
// never moves a bound Worker to another one.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/placementauth"
	"github.com/hat47x/eki-fabric/internal/core/placementissue"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/scheduler"
	"github.com/hat47x/eki-fabric/internal/core/suitability"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

// ErrInvalidConfig means the loop is not usable as configured.
var ErrInvalidConfig = errors.New("invalid dispatch configuration")

const (
	// EvaluatorVersion names the fixed suitability heuristic of this loop.
	// It is an input to the suitability basis digest, so a change here makes
	// every later Fabric Entry distinguishable.
	EvaluatorVersion = "dispatch-mvp-1"
	// EvidenceKind marks the evidence of the fixed Worker assessment.
	EvidenceKind = "dispatch/worker-assessment"
)

// TaskSource is the read side of the loop over Tasks.
type TaskSource interface {
	ListTasksByPhase(ctx context.Context, phase taskstate.TaskPhase, limit int) ([]taskstate.TaskRecord, error)
	// CompareAndSwapTask promotes a Task to the next phase. The store checks
	// the write against taskstate.ValidateTransition (ADR-0032).
	CompareAndSwapTask(ctx context.Context, id ids.TaskID, expectedRevision uint64, next taskstate.TaskRecord) error
}

// AttemptSource lists the Attempts of one Task and moves them forward.
type AttemptSource interface {
	ListAttemptsByTask(ctx context.Context, taskID ids.TaskID) ([]attemptstate.AttemptRecord, error)
	// CreateAttempt accepts only an initial A-CREATED / revision 1 record.
	CreateAttempt(ctx context.Context, record attemptstate.AttemptRecord) error
	// CompareAndSwapAttempt replaces an Attempt only when it still reads the
	// expected revision.
	CompareAndSwapAttempt(ctx context.Context, id ids.AttemptID, expectedRevision uint64, next attemptstate.AttemptRecord) error
}

// WorkerSource lists the registered Workers.
type WorkerSource interface {
	ListWorkers(ctx context.Context) ([]workerregistry.Worker, error)
}

// PolicySource reads the current policy.
type PolicySource interface {
	LoadCurrentPolicy(ctx context.Context) (policystate.State, error)
}

type Config struct {
	Tasks        TaskSource
	Attempts     AttemptSource
	AttemptLists AttemptSource
	Workers      WorkerSource
	Policies     PolicySource

	// IssuePlacement signs one approved placement (PLC-1).
	IssuePlacement func(ctx context.Context, req placementissue.IssueRequest) ([]byte, error)
	// Reserve reserves a Lease from a signed Placement Approval (LSE-1).
	Reserve func(ctx context.Context, signedPlacement []byte, lifetime time.Duration) (ReservedLease, error)
	// EmitLease signs and records the reserved Lease.
	EmitLease func(ctx context.Context, leaseID ids.LeaseID) (signedLease []byte, err error)
	// IssueWorkOrder signs and records the WorkOrder of an EMITTED Lease (WOR-1).
	IssueWorkOrder func(ctx context.Context, leaseID ids.LeaseID, worker string) (signedOrder []byte, err error)

	// LeaseLifetime is the lifetime the loop asks for. It must fit inside the
	// Placement validity and the Lease service maximum.
	LeaseLifetime time.Duration
	// Limit bounds how many Tasks one tick may advance.
	Limit int
	Clock interface{ Now() time.Time }
	Log   *slog.Logger
}

// ReservedLease is what the loop needs from a reserved Lease.
type ReservedLease struct {
	LeaseID    ids.LeaseID
	FenceEpoch uint64
}

// Loop advances Ready Tasks through the issuing chain.
type Loop struct {
	cfg      Config
	attempts AttemptSource
	limit    int
	now      func() time.Time
}

// New validates the configuration.
func New(cfg Config) (*Loop, error) {
	switch {
	case cfg.Tasks == nil || cfg.Attempts == nil || cfg.Workers == nil || cfg.Policies == nil:
		return nil, fmt.Errorf("%w: store is required", ErrInvalidConfig)
	case cfg.Reserve == nil || cfg.EmitLease == nil || cfg.IssueWorkOrder == nil:
		return nil, fmt.Errorf("%w: issuing functions are required", ErrInvalidConfig)
	case cfg.LeaseLifetime <= 0:
		return nil, fmt.Errorf("%w: lease lifetime must be positive", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 8
	}
	if cfg.AttemptLists == nil {
		cfg.AttemptLists = cfg.Attempts
	}
	return &Loop{cfg: cfg, attempts: cfg.AttemptLists, limit: limit, now: cfg.Clock.Now}, nil
}

// TickResult reports what one tick did.
type TickResult struct {
	// Promoted is how many admitted Tasks the tick moved to ready (ADR-0032).
	Promoted int
	// Considered is how many Ready Tasks the tick looked at.
	Considered int
	// Dispatched is how many Tasks reached an EMITTED WorkOrder.
	Dispatched int
	// Deferred is how many Tasks the tick left for a later tick, with the
	// reason. A deferred Task is not a failure: the usual cause is that no
	// Worker is usable yet.
	Deferred []DeferredTask
}

// DeferredTask names one Task the tick left for later.
type DeferredTask struct {
	TaskID ids.TaskID
	Reason string
}

// Tick takes up to the configured number of Ready Tasks one step further. It
// never returns a Task error as its own error: a Task that cannot advance is
// deferred, and a tick stops only when the context ends or the store fails.
func (l *Loop) Tick(ctx context.Context) (TickResult, error) {
	result := TickResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// Admitted Tasks are promoted first, so a Task admitted since the last tick
	// can be dispatched by this one (ADR-0032 §2.2).
	if err := l.promoteAdmitted(ctx, &result); err != nil {
		return result, err
	}
	tasks, err := l.cfg.Tasks.ListTasksByPhase(ctx, taskstate.TaskPhaseReady, l.limit)
	if err != nil {
		return result, fmt.Errorf("list ready tasks: %w", err)
	}
	result.Considered = len(tasks)
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := l.dispatchOne(ctx, task); err != nil {
			result.Deferred = append(result.Deferred, DeferredTask{TaskID: task.ID, Reason: err.Error()})
			continue
		}
		result.Dispatched++
	}
	return result, nil
}

// promoteAdmitted moves admitted Tasks to ready, so the placement loop has
// work to dispatch (ADR-0032 §2.2). Admission accepts a Task but does not
// decide that it may start now: policy currentness, budget and capacity are
// judged here, and a cancellation that arrived after admission is respected.
func (l *Loop) promoteAdmitted(ctx context.Context, result *TickResult) error {
	tasks, err := l.cfg.Tasks.ListTasksByPhase(ctx, taskstate.TaskPhaseAdmitted, l.limit)
	if err != nil {
		return fmt.Errorf("list admitted tasks: %w", err)
	}
	if len(tasks) == 0 {
		return nil
	}
	policy, err := l.cfg.Policies.LoadCurrentPolicy(ctx)
	if err != nil {
		// No policy means no Task may be placed: leave them admitted rather
		// than failing the tick, so the loop keeps serving the other steps.
		for _, task := range tasks {
			result.Deferred = append(result.Deferred, DeferredTask{TaskID: task.ID, Reason: "policy is not available"})
		}
		return nil
	}
	if policy.Status != policystate.StatusActive {
		for _, task := range tasks {
			result.Deferred = append(result.Deferred, DeferredTask{
				TaskID: task.ID,
				Reason: fmt.Sprintf("policy %s is %s, not active", policy.Revision, policy.Status),
			})
		}
		return nil
	}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if task.CancellationRequested {
			// A Task whose principal asked to cancel it must not start.
			result.Deferred = append(result.Deferred, DeferredTask{TaskID: task.ID, Reason: "cancellation was requested"})
			continue
		}
		next := task
		next.Phase = taskstate.TaskPhaseReady
		next.Revision = task.Revision + 1
		next.UpdatedAt = l.now().UTC()
		if err := l.cfg.Tasks.CompareAndSwapTask(ctx, task.ID, task.Revision, next); err != nil {
			// A revision conflict means another writer got there first; the
			// next tick reads the Task again and decides from there.
			result.Deferred = append(result.Deferred, DeferredTask{TaskID: task.ID, Reason: err.Error()})
			continue
		}
		result.Promoted++
	}
	return nil
}

func (l *Loop) dispatchOne(ctx context.Context, task taskstate.TaskRecord) error {
	attempts, err := l.attempts.ListAttemptsByTask(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("list attempts: %w", err)
	}
	var attempt *attemptstate.AttemptRecord
	for i := range attempts {
		if attempts[i].Phase != attemptstate.AttemptPhaseClosed {
			a := attempts[i]
			attempt = &a
			break
		}
	}
	if attempt != nil && attempt.LeaseID != "" {
		// A previous tick already bound this Attempt to a Lease. Its WorkOrder
		// is either emitted or the tick died between the two; the Lease side
		// continues at the Lease, not here.
		return errors.New("attempt already holds a lease")
	}

	policy, err := l.cfg.Policies.LoadCurrentPolicy(ctx)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}
	if policy.Status != policystate.StatusActive {
		return fmt.Errorf("policy %s is %s, not active", policy.Revision, policy.Status)
	}
	worker, err := l.pickWorker(ctx)
	if err != nil {
		return err
	}
	if attempt == nil {
		created, err := l.createAttempt(ctx, task, worker, l.now())
		if err != nil {
			return err
		}
		attempt = &created
		// The Task has an Attempt now, which is what active means (ADR-0032
		// §2.3). If this write fails the Attempt exists and the Task is still
		// ready; the next tick finds the placing Attempt and continues.
		if err := l.markActive(ctx, task); err != nil {
			return err
		}
	} else if attempt.WorkerIdentity != "" && attempt.WorkerIdentity != worker {
		// Re-planning keeps the Worker an Attempt is bound to.
		worker = attempt.WorkerIdentity
	}

	signedPlacement, err := l.place(ctx, task, attempt, worker, policy)
	if err != nil {
		return err
	}
	reserved, err := l.cfg.Reserve(ctx, signedPlacement, l.cfg.LeaseLifetime)
	if err != nil {
		return fmt.Errorf("reserve lease: %w", err)
	}
	if _, err := l.cfg.EmitLease(ctx, reserved.LeaseID); err != nil {
		return fmt.Errorf("emit lease: %w", err)
	}
	if _, err := l.cfg.IssueWorkOrder(ctx, reserved.LeaseID, worker); err != nil {
		return fmt.Errorf("issue work order: %w", err)
	}
	// Placement, Lease reservation and emission each advance the Attempt, so
	// the copy read before them is stale. Re-read before the final transition.
	current, err := l.reloadAttempt(ctx, task.ID, attempt.ID)
	if err != nil {
		return err
	}
	if current.Phase == attemptstate.AttemptPhasePlacing {
		assigned, err := l.assignAttempt(ctx, current, worker, reserved.LeaseID)
		if err != nil {
			return err
		}
		current = assigned
	}
	if current.Phase != attemptstate.AttemptPhaseAssigned {
		return fmt.Errorf("attempt %s is %s after the WorkOrder, not ASSIGNED", current.ID, current.Phase)
	}
	// The WorkOrder is now visible to the Worker, so the Attempt is
	// DISPATCHED. Claim acceptance only accepts DISPATCHED or later phases;
	// leaving the Attempt ASSIGNED would reject every ResultClaim.
	dispatched := current
	dispatched.Phase = attemptstate.AttemptPhaseDispatched
	dispatched.WorkerIdentity, dispatched.LeaseID = worker, reserved.LeaseID
	dispatched.Revision++
	dispatched.UpdatedAt = l.now()
	if err := l.cfg.Attempts.CompareAndSwapAttempt(ctx, current.ID, current.Revision, dispatched); err != nil {
		return fmt.Errorf("mark attempt dispatched: %w", err)
	}
	return nil
}

// markActive records that the Task now has an Attempt, which is what the
// active phase means (ADR-0032 §2.3). The write is a compare-and-swap so a
// Task another writer moved (a cancellation, for example) is not overwritten.
func (l *Loop) markActive(ctx context.Context, task taskstate.TaskRecord) error {
	if task.Phase != taskstate.TaskPhaseReady {
		return nil
	}
	next := task
	next.Phase = taskstate.TaskPhaseActive
	next.Revision = task.Revision + 1
	next.UpdatedAt = l.now().UTC()
	if err := l.cfg.Tasks.CompareAndSwapTask(ctx, task.ID, task.Revision, next); err != nil {
		return fmt.Errorf("mark task active: %w", err)
	}
	return nil
}

// reloadAttempt reads the current Attempt after the issuing chain advanced its
// revision. The in-memory copy from before placement is stale because the
// placement and the Agent's fetch both update the same record.
func (l *Loop) reloadAttempt(ctx context.Context, taskID ids.TaskID, id ids.AttemptID) (attemptstate.AttemptRecord, error) {
	attempts, err := l.attempts.ListAttemptsByTask(ctx, taskID)
	if err != nil {
		return attemptstate.AttemptRecord{}, fmt.Errorf("reload attempt: %w", err)
	}
	for _, attempt := range attempts {
		if attempt.ID == id {
			return attempt, nil
		}
	}
	return attemptstate.AttemptRecord{}, fmt.Errorf("reload attempt: %s not found", id)
}

// pickWorker returns the first usable Worker, or an error naming the gap. The
// fixed heuristic of this loop takes the first; the Scheduler still ranks the
// one-element candidate list it produces.
func (l *Loop) pickWorker(ctx context.Context) (string, error) {
	workers, err := l.cfg.Workers.ListWorkers(ctx)
	if err != nil {
		return "", fmt.Errorf("list workers: %w", err)
	}
	for _, w := range workers {
		if w.Usable() {
			return w.Identity.String(), nil
		}
	}
	return "", errors.New("no usable worker is registered")
}

func (l *Loop) createAttempt(ctx context.Context, task taskstate.TaskRecord, worker string, now time.Time) (attemptstate.AttemptRecord, error) {
	attempts, err := l.attempts.ListAttemptsByTask(ctx, task.ID)
	if err != nil {
		return attemptstate.AttemptRecord{}, fmt.Errorf("list attempts: %w", err)
	}
	next := uint64(len(attempts)) + 1
	id, err := ids.NewAttemptID()
	if err != nil {
		return attemptstate.AttemptRecord{}, fmt.Errorf("new attempt id: %w", err)
	}
	attempt, err := attemptstate.New(id, task.ID, task.TaskIRDigest, next, now)
	if err != nil {
		return attemptstate.AttemptRecord{}, fmt.Errorf("create attempt: %w", err)
	}
	if err := l.cfg.Attempts.CreateAttempt(ctx, attempt); err != nil {
		return attemptstate.AttemptRecord{}, fmt.Errorf("record attempt: %w", err)
	}
	placing := attempt
	placing.Phase, placing.WorkerIdentity = attemptstate.AttemptPhasePlacing, worker
	placing.Revision, placing.UpdatedAt = attempt.Revision+1, now
	if err := l.cfg.Attempts.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, placing); err != nil {
		return attemptstate.AttemptRecord{}, fmt.Errorf("bind worker to attempt: %w", err)
	}
	return placing, nil
}

// place runs suitability, the Scheduler, placement authorization, and PLC-1
// for one Attempt, and returns the signed Placement Approval.
func (l *Loop) place(ctx context.Context, task taskstate.TaskRecord, attempt *attemptstate.AttemptRecord, worker string, policy policystate.State) ([]byte, error) {
	// The MVP heuristic feeds the evaluator a fixed estimate per component
	// and a baseline that fabric placement beats by construction, so every
	// Task is fabric-justified against a usable Worker. The workload digest
	// stands for the Task IR until #198 defines a real one.
	//
	// FABRIC_JUSTIFIED here means only that the fixed estimate says so. The
	// estimates are constants of this loop, not measurements (#242).
	components := []suitability.ComponentEstimate{
		{Kind: suitability.ComponentSuitabilityDecision, Millis: 1, EvidenceRef: "dispatch/mvp"},
		{Kind: suitability.ComponentAdmissionPlacement, Millis: 1, EvidenceRef: "dispatch/mvp"},
		{Kind: suitability.ComponentDispatch, Millis: 1, EvidenceRef: "dispatch/mvp"},
		{Kind: suitability.ComponentQueue, Millis: 1, EvidenceRef: "dispatch/mvp"},
		{Kind: suitability.ComponentInputTransfer, Millis: 1, EvidenceRef: "dispatch/mvp"},
		{Kind: suitability.ComponentCompute, Millis: 1, EvidenceRef: "dispatch/mvp"},
		{Kind: suitability.ComponentRetryPenalty, Millis: 1, EvidenceRef: "dispatch/mvp"},
		{Kind: suitability.ComponentOutputTransfer, Millis: 1, EvidenceRef: "dispatch/mvp"},
		{Kind: suitability.ComponentVerification, Millis: 1, EvidenceRef: "dispatch/mvp"},
	}
	receipt, entry, err := suitability.EvaluateForFabricEntry(suitability.Input{
		TaskID:               task.ID,
		TaskIRDigest:         task.TaskIRDigest,
		WorkloadDigest:       task.TaskIRDigest,
		PolicyRevision:       policy.Epoch,
		EvaluatorVersion:     EvaluatorVersion,
		FeasibilityConfirmed: true,
		Baseline:             &suitability.DurationEvidence{Millis: 100, EvidenceRef: "dispatch/mvp-baseline"},
		Components:           components,
		MinimumBenefitMillis: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("evaluate suitability: %w", err)
	}
	if entry == nil {
		return nil, fmt.Errorf("suitability decision %q does not justify fabric placement", receipt.Decision)
	}
	planned, err := scheduler.Plan(scheduler.Request{
		TaskID: task.ID, TaskIRDigest: task.TaskIRDigest, AttemptID: attempt.ID,
		FabricEntry: entry, Candidates: []scheduler.Candidate{{
			WorkerIdentity:   worker,
			PlanningEligible: true,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	if planned.Outcome != scheduler.OutcomeProposed || planned.Proposal == nil {
		return nil, fmt.Errorf("scheduler outcome %q with no proposal", planned.Outcome)
	}
	assessment := placementauth.WorkerAssessment{
		WorkerIdentity: worker,
		Online:         placementauth.Check{Satisfied: true, Evidence: l.evidence(worker, task)},
		// The MVP assessment asserts the remaining checks as satisfied with
		// the same evidence. Asserting them from the dispatch loop is the
		// placeholder for real checks (#242).
		ProviderActive:       placementauth.Check{Satisfied: true, Evidence: l.evidence(worker, task)},
		ExecutorSupported:    placementauth.Check{Satisfied: true, Evidence: l.evidence(worker, task)},
		DataClassAllowed:     placementauth.Check{Satisfied: true, Evidence: l.evidence(worker, task)},
		ResourceFeasible:     placementauth.Check{Satisfied: true, Evidence: l.evidence(worker, task)},
		AssuranceSatisfied:   placementauth.Check{Satisfied: true, Evidence: l.evidence(worker, task)},
		TransferWithinBudget: placementauth.Check{Satisfied: true, Evidence: l.evidence(worker, task)},
	}
	authorized, err := placementauth.Authorize(placementauth.Request{
		Proposal: planned.Proposal, TaskID: task.ID, TaskIRDigest: task.TaskIRDigest,
		AttemptID: attempt.ID, PolicyRevision: policy.Epoch, Worker: assessment,
	})
	if err != nil {
		return nil, fmt.Errorf("authorize placement: %w", err)
	}
	if authorized.Outcome != placementauth.OutcomeApproved || authorized.Draft == nil {
		return nil, fmt.Errorf("placement rejected: %v", authorized.Rejections)
	}
	evidence := make([]decisionpayload.EvidenceRefConfig, 0, len(authorized.Draft.Evidence()))
	for _, item := range authorized.Draft.Evidence() {
		evidence = append(evidence, decisionpayload.EvidenceRefConfig{
			Kind: item.Evidence.Kind, Identity: item.Evidence.Identity, Digest: item.Evidence.Digest,
		})
	}
	issued, err := l.cfg.IssuePlacement(ctx, placementissue.IssueRequest{
		TaskID: task.ID, TaskIRDigest: task.TaskIRDigest, AttemptID: attempt.ID,
		WorkerIdentity: worker, PolicyRevision: policy.Revision, Evidence: evidence,
	})
	if err != nil {
		return nil, fmt.Errorf("issue placement: %w", err)
	}
	return issued, nil
}

func (l *Loop) evidence(worker string, task taskstate.TaskRecord) placementauth.EvidenceRef {
	return placementauth.EvidenceRef{
		Kind:     EvidenceKind,
		Identity: worker,
		Digest:   contract.SHA256([]byte(worker + "|" + task.ID.String() + "|" + task.TaskIRDigest.String())),
	}
}

func (l *Loop) assignAttempt(ctx context.Context, attempt attemptstate.AttemptRecord, worker string, leaseID ids.LeaseID) (attemptstate.AttemptRecord, error) {
	assigned := attempt
	assigned.Phase, assigned.LeaseID = attemptstate.AttemptPhaseAssigned, leaseID
	assigned.Revision, assigned.UpdatedAt = attempt.Revision+1, l.now()
	if err := l.cfg.Attempts.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, assigned); err != nil {
		return attemptstate.AttemptRecord{}, fmt.Errorf("assign attempt: %w", err)
	}
	return assigned, nil
}
