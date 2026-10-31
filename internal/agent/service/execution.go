package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/assignment"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/agent/nativerunner"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// executionHandler runs one ready assignment with the fixed command from
// ExecutionConfig, then submits the observed ResultClaim.
type executionHandler struct {
	runner     executor.Runner
	supervisor *executor.Supervisor
	events     *eventLog
	log        *slog.Logger
	claims     claimEnqueuer
	principal  workerplane.Principal
}

// claimEnqueuer records a ResultClaim durably. Delivery to the Control Plane is
// the queue's job, so a failed send never loses the observed result.
type claimEnqueuer interface {
	Enqueue(ctx context.Context, claim resultclaim.Claim) (alreadyQueued bool, err error)
}

func newExecutionHandler(c *ExecutionConfig, events *eventLog, log *slog.Logger, claims claimEnqueuer, principal workerplane.Principal) (*executionHandler, error) {
	if c == nil || c.Command == "" {
		return nil, fmt.Errorf("%w: execution command is required", ErrInvalidConfig)
	}
	if isNilEnqueuer(claims) || principal.WorkerIdentity == "" {
		return nil, fmt.Errorf("%w: execution needs a claim queue and an authenticated Worker", ErrInvalidConfig)
	}
	grace := time.Duration(c.StopGraceSeconds) * time.Second
	if grace <= 0 {
		grace = 5 * time.Second
	}
	supervisor, err := executor.New(executor.Config{Clock: systemClock{}, StopGrace: grace})
	if err != nil {
		return nil, err
	}
	return &executionHandler{
		runner: nativerunner.Runner{
			Command:          c.Command,
			Args:             c.Args,
			Env:              c.Env,
			AllowedArgs:      c.AllowedArgs,
			ExecutableSHA256: c.ExecutableSHA256,
		},
		supervisor: supervisor,
		events:     events,
		log:        log,
		claims:     claims,
		principal:  principal,
	}, nil
}

func (h *executionHandler) Start(ctx context.Context, ready assignment.Ready) error {
	outcome, err := h.supervisor.Execute(ctx, executor.SpecFromPlan(ready.Plan), h.runner, noStopSource{})
	ev := event{
		Time:            time.Now().UTC(),
		AttemptID:       string(ready.AttemptID),
		LeaseID:         string(ready.LeaseID),
		Outcome:         string(assignment.OutcomeReady),
		ExecutionStatus: string(outcome.Status),
		ExitCode:        outcome.ExitCode,
		OutputBytes:     outcome.OutputBytes,
		OutputDigest:    outcome.OutputDigest.String(),
	}
	if outcome.Failure != nil {
		ev.Failure = outcome.Failure.Error()
	}
	if writeErr := h.events.write(ev); writeErr != nil {
		h.log.Error("execution event not recorded", "error", writeErr.Error())
	}
	if err != nil {
		return fmt.Errorf("execute assignment %s: %w", ready.AttemptID, err)
	}

	claim, err := claimFromOutcome(claimSource{
		taskID:          ready.Plan.TaskID(),
		taskIRDigest:    ready.Admission.TaskIRDigest(),
		attemptID:       ready.AttemptID,
		leaseID:         ready.LeaseID,
		fenceEpoch:      ready.FenceEpoch,
		orderDecisionID: ready.Plan.OrderDecisionID(),
		executorDigest:  ready.Plan.ExecutorDigest(),
		nonce:           ready.Plan.Nonce(),
	}, h.principal.WorkerIdentity, outcome)
	if err != nil {
		return fmt.Errorf("build result claim for %s: %w", ready.AttemptID, err)
	}
	// The claim reaches the queue before any send is tried. The queue's
	// delivery pass sends it and retries after a Control Plane outage or an
	// Agent restart.
	alreadyQueued, err := h.claims.Enqueue(ctx, claim)
	if err != nil {
		h.log.Error("result claim not queued", "attempt", ready.AttemptID, "error", err.Error())
		return fmt.Errorf("queue result claim for %s: %w", ready.AttemptID, err)
	}
	queuedEvent := event{
		Time:      time.Now().UTC(),
		AttemptID: string(ready.AttemptID),
		LeaseID:   string(ready.LeaseID),
		Outcome:   eventClaimQueued,
	}
	if alreadyQueued {
		queuedEvent.Reason = "already_queued"
	}
	if writeErr := h.events.write(queuedEvent); writeErr != nil {
		h.log.Error("claim event not recorded", "error", writeErr.Error())
	}
	return nil
}

func isNilEnqueuer(c claimEnqueuer) bool {
	if c == nil {
		return true
	}
	if q, ok := c.(*claimqueue.Queue); ok {
		return q == nil
	}
	return false
}

// Outcomes recorded in events_file for the ResultClaim's path to the Control
// Plane. Queued means the claim is durable on this Agent, not that the Control
// Plane has it.
const (
	eventClaimQueued         = "result_claim_queued"
	eventClaimSubmitted      = "result_claim_submitted"
	eventClaimDeliveryFailed = "result_claim_delivery_failed"
)

// recordingClaims records every delivery try in events_file. It wraps the
// transport under the claim queue, so a failed send is visible and a retry
// that finally succeeds is distinguishable from the failures before it.
type recordingClaims struct {
	next   workerplane.ClaimClient
	events *eventLog
	log    *slog.Logger
}

func (r recordingClaims) SubmitClaim(ctx context.Context, p workerplane.Principal, claim resultclaim.Claim) (workerplane.ClaimAck, error) {
	ack, err := r.next.SubmitClaim(ctx, p, claim)
	ev := event{Time: time.Now().UTC(), AttemptID: string(claim.AttemptID()), LeaseID: string(claim.LeaseID())}
	if err != nil {
		ev.Outcome, ev.Failure = eventClaimDeliveryFailed, err.Error()
	} else {
		ev.Outcome, ev.ClaimDecision = eventClaimSubmitted, ack.Decision
	}
	if writeErr := r.events.write(ev); writeErr != nil {
		r.log.Error("claim delivery event not recorded", "error", writeErr.Error())
	}
	return ack, err
}

type claimSource struct {
	taskID          ids.TaskID
	taskIRDigest    contract.Digest
	attemptID       ids.AttemptID
	leaseID         ids.LeaseID
	fenceEpoch      uint64
	orderDecisionID string
	executorDigest  contract.Digest
	nonce           []byte
}

func claimFromOutcome(src claimSource, workerIdentity string, outcome executor.Outcome) (resultclaim.Claim, error) {
	status, err := claimStatus(outcome.Status)
	if err != nil {
		return resultclaim.Claim{}, err
	}
	outputDigest := outcome.OutputDigest
	if status != resultclaim.StatusCompleted {
		outputDigest = contract.Digest{}
	}
	return resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID:            src.taskID,
		TaskIRDigest:      src.taskIRDigest,
		AttemptID:         src.attemptID,
		WorkerIdentity:    workerIdentity,
		LeaseID:           src.leaseID,
		FenceEpoch:        src.fenceEpoch,
		WorkOrderDecision: src.orderDecisionID,
		ExecutorDigest:    src.executorDigest,
		Nonce:             src.nonce,
		Status:            status,
		ExitCode:          outcome.ExitCode,
		OutputBytes:       outcome.OutputBytes,
		OutputDigest:      outputDigest,
		StartedAt:         outcome.StartedAt,
		EndedAt:           outcome.EndedAt,
	})
}

func claimStatus(status executor.Status) (resultclaim.Status, error) {
	switch status {
	case executor.StatusCompleted:
		return resultclaim.StatusCompleted, nil
	case executor.StatusFailed:
		return resultclaim.StatusFailed, nil
	case executor.StatusReclaimed:
		return resultclaim.StatusReclaimed, nil
	case executor.StatusDeadlineExceeded:
		return resultclaim.StatusDeadlineExceeded, nil
	case executor.StatusCancelled:
		return resultclaim.StatusCancelled, nil
	case executor.StatusOutputExceeded:
		return resultclaim.StatusOutputExceeded, nil
	case executor.StatusRunnerStuck:
		return resultclaim.StatusRunnerStuck, nil
	}
	return "", fmt.Errorf("unsupported execution status %q", status)
}

// noStopSource has no Local Governor signal. The Supervisor still enforces the
// deadline and cancellation; resource ceilings are the Runner's responsibility.
type noStopSource struct{}

func (noStopSource) Next(ctx context.Context) (governor.Decision, error) {
	<-ctx.Done()
	return governor.Decision{}, ctx.Err()
}
