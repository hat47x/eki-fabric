package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/assignment"
	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/agent/nativerunner"
	"github.com/hat47x/eki-fabric/internal/core/contract"
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
	claims     workerplane.ClaimClient
	principal  workerplane.Principal
}

func newExecutionHandler(c *ExecutionConfig, events *eventLog, log *slog.Logger, claims workerplane.ClaimClient, principal workerplane.Principal) (*executionHandler, error) {
	if c == nil || c.Command == "" {
		return nil, fmt.Errorf("%w: execution command is required", ErrInvalidConfig)
	}
	if claims == nil || principal.WorkerIdentity == "" {
		return nil, fmt.Errorf("%w: execution needs a claim client and an authenticated Worker", ErrInvalidConfig)
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

	claim, err := claimFromOutcome(ready, h.principal.WorkerIdentity, outcome)
	if err != nil {
		return fmt.Errorf("build result claim for %s: %w", ready.AttemptID, err)
	}
	ack, err := h.claims.SubmitClaim(ctx, h.principal, claim)
	if err != nil {
		return fmt.Errorf("submit result claim for %s: %w", ready.AttemptID, err)
	}
	claimEvent := event{
		Time:          time.Now().UTC(),
		AttemptID:     string(ready.AttemptID),
		LeaseID:       string(ready.LeaseID),
		Outcome:       "result_claim_submitted",
		ClaimDecision: ack.Decision,
	}
	if writeErr := h.events.write(claimEvent); writeErr != nil {
		h.log.Error("claim event not recorded", "error", writeErr.Error())
	}
	return nil
}

func claimFromOutcome(ready assignment.Ready, workerIdentity string, outcome executor.Outcome) (resultclaim.Claim, error) {
	status, err := claimStatus(outcome.Status)
	if err != nil {
		return resultclaim.Claim{}, err
	}
	outputDigest := outcome.OutputDigest
	if status != resultclaim.StatusCompleted {
		outputDigest = contract.Digest{}
	}
	return resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID:            ready.Plan.TaskID(),
		TaskIRDigest:      ready.Admission.TaskIRDigest(),
		AttemptID:         ready.AttemptID,
		WorkerIdentity:    workerIdentity,
		LeaseID:           ready.LeaseID,
		FenceEpoch:        ready.FenceEpoch,
		WorkOrderDecision: ready.Plan.OrderDecisionID(),
		ExecutorDigest:    ready.Plan.ExecutorDigest(),
		Nonce:             ready.Plan.Nonce(),
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
