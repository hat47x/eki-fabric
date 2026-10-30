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
)

// executionHandler runs one ready assignment with the fixed command from
// ExecutionConfig. It records the outcome; it does not report a Task result to
// the Control Plane yet.
type executionHandler struct {
	runner     executor.Runner
	supervisor *executor.Supervisor
	events     *eventLog
	log        *slog.Logger
}

func newExecutionHandler(c *ExecutionConfig, events *eventLog, log *slog.Logger) (*executionHandler, error) {
	if c == nil || c.Command == "" {
		return nil, fmt.Errorf("%w: execution command is required", ErrInvalidConfig)
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
	return nil
}

// noStopSource has no Local Governor signal. The Supervisor still enforces the
// deadline and cancellation; resource ceilings are the Runner's responsibility.
type noStopSource struct{}

func (noStopSource) Next(ctx context.Context) (governor.Decision, error) {
	<-ctx.Done()
	return governor.Decision{}, ctx.Err()
}
