// Package executor supervises one Attempt after the start gate allowed it.
//
// It applies the Plan's deadline, passes the effective limits to a Runner,
// listens to the Local Governor while the work runs, and stops the Runner when
// the governor stops running work. The result is an Outcome that the Agent can
// record and feed into recovery.
//
// This package does not make authorization decisions. The Agent must build the
// Spec from a startgate.Plan (SpecFromPlan). Enforcement of the limits belongs
// to the Runner: the Supervisor can only stop the work and report what it
// observed.
package executor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var (
	ErrInvalidConfig = errors.New("invalid executor configuration")
	ErrInvalidSpec   = errors.New("invalid execution spec")
	ErrRunnerStuck   = errors.New("the Runner did not stop within the grace period")
)

type Clock interface{ Now() time.Time }

// Spec is what a Runner is asked to execute. Build it with SpecFromPlan; the
// fields are descriptive, not authority. A Runner must enforce Limits and
// Sandbox itself.
type Spec struct {
	TaskID          ids.TaskID
	AttemptID       ids.AttemptID
	OrderDecisionID string
	ExecutorDigest  contract.Digest
	Limits          decisionpayload.ResourceLimits
	Sandbox         decisionpayload.SandboxPolicy
	Deadline        time.Time
	Nonce           []byte
}

// SpecFromPlan copies the effective limits and deadline of one Plan.
func SpecFromPlan(plan startgate.Plan) Spec {
	return Spec{
		TaskID:          plan.TaskID(),
		AttemptID:       plan.AttemptID(),
		OrderDecisionID: plan.OrderDecisionID(),
		ExecutorDigest:  plan.ExecutorDigest(),
		Limits:          plan.Limits(),
		Sandbox:         plan.Sandbox(),
		Deadline:        plan.Deadline(),
		Nonce:           plan.Nonce(),
	}
}

// Result is what a Runner reports after it returns. An ExitCode other than zero
// is a failed execution, whatever the Runner's error says.
type Result struct {
	ExitCode    int
	OutputBytes uint64
}

// Runner executes one Attempt. Run must return when ctx is cancelled; the
// Supervisor relies on that to stop work promptly.
type Runner interface {
	Run(ctx context.Context, spec Spec) (Result, error)
}

// DecisionSource delivers the Local Governor's decisions until the context is
// cancelled. A production Agent polls the governor; tests can feed a channel.
type DecisionSource interface {
	Next(ctx context.Context) (governor.Decision, error)
}

type Status string

const (
	StatusCompleted        Status = "completed"
	StatusFailed           Status = "failed"
	StatusReclaimed        Status = "reclaimed"
	StatusDeadlineExceeded Status = "deadline_exceeded"
	StatusCancelled        Status = "cancelled"
	StatusOutputExceeded   Status = "output_exceeded"
	StatusRunnerStuck      Status = "runner_stuck"
)

// Outcome is the observed end of one execution. Failure is set for every status
// other than StatusCompleted, and explains the cause for the audit record.
type Outcome struct {
	Status      Status
	ExitCode    int
	OutputBytes uint64
	Reasons     []governor.Reason
	StartedAt   time.Time
	EndedAt     time.Time
	Failure     error
}

type Config struct {
	Clock     Clock
	StopGrace time.Duration
}

type Supervisor struct {
	clock     Clock
	stopGrace time.Duration
}

func New(cfg Config) (*Supervisor, error) {
	if cfg.Clock == nil {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}
	if cfg.StopGrace <= 0 {
		return nil, fmt.Errorf("%w: StopGrace must be positive", ErrInvalidConfig)
	}
	return &Supervisor{clock: cfg.Clock, stopGrace: cfg.StopGrace}, nil
}

// Execute runs one Attempt to its end. Invalid input is reported as an error.
// Every other ending is an Outcome: the Agent records it, and a Reclaimed,
// DeadlineExceeded, or Cancelled outcome is not a failed execution by itself.
func (s *Supervisor) Execute(ctx context.Context, spec Spec, runner Runner, decisions DecisionSource) (Outcome, error) {
	if s == nil || s.clock == nil {
		return Outcome{}, fmt.Errorf("%w: supervisor is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return Outcome{}, fmt.Errorf("%w: context is required", ErrInvalidSpec)
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if runner == nil {
		return Outcome{}, fmt.Errorf("%w: Runner is required", ErrInvalidSpec)
	}
	if decisions == nil {
		return Outcome{}, fmt.Errorf("%w: decision source is required", ErrInvalidSpec)
	}
	if err := validateSpec(spec); err != nil {
		return Outcome{}, err
	}
	now := s.clock.Now().UTC()
	if now.IsZero() {
		return Outcome{}, fmt.Errorf("%w: clock returned zero time", ErrInvalidConfig)
	}
	if !now.Before(spec.Deadline) {
		return Outcome{Status: StatusDeadlineExceeded, StartedAt: now, EndedAt: now,
			Failure: errors.New("the deadline has already passed")}, nil
	}

	runCtx, cancel := context.WithDeadline(ctx, spec.Deadline)
	defer cancel()

	resultCh := make(chan runResult, 1)
	go func() {
		result, err := runner.Run(runCtx, spec)
		resultCh <- runResult{result: result, err: err}
	}()
	decisionCh := make(chan governor.Decision, 1)
	go func() {
		for {
			decision, err := decisions.Next(runCtx)
			if err != nil {
				return
			}
			select {
			case decisionCh <- decision:
			case <-runCtx.Done():
				return
			}
		}
	}()

	startedAt := now
	for {
		select {
		case outcome := <-resultCh:
			return s.finish(outcome, spec, startedAt, nil), nil
		case decision := <-decisionCh:
			if !decision.StopRunning {
				continue
			}
			cancel()
			return s.awaitStop(resultCh, spec, startedAt, StatusReclaimed, decision.Reasons), nil
		case <-runCtx.Done():
			cancel()
			status := StatusDeadlineExceeded
			if ctx.Err() != nil {
				status = StatusCancelled
			}
			return s.awaitStop(resultCh, spec, startedAt, status, nil), nil
		}
	}
}

type runResult struct {
	result Result
	err    error
}

func (s *Supervisor) finish(run runResult, spec Spec, startedAt time.Time, reasons []governor.Reason) Outcome {
	outcome := Outcome{
		ExitCode:    run.result.ExitCode,
		OutputBytes: run.result.OutputBytes,
		Reasons:     reasons,
		StartedAt:   startedAt,
		EndedAt:     s.clock.Now().UTC(),
	}
	if run.err != nil {
		outcome.Status = StatusFailed
		outcome.Failure = run.err
		return outcome
	}
	if run.result.OutputBytes > spec.Limits.OutputBytesMaximum {
		outcome.Status = StatusOutputExceeded
		outcome.Failure = fmt.Errorf("output %d bytes exceeds the limit %d", run.result.OutputBytes, spec.Limits.OutputBytesMaximum)
		return outcome
	}
	if run.result.ExitCode != 0 {
		outcome.Status = StatusFailed
		outcome.Failure = fmt.Errorf("runner exited with code %d", run.result.ExitCode)
		return outcome
	}
	outcome.Status = StatusCompleted
	return outcome
}

// stop waits for the Runner to return after cancellation. The grace period
// bounds how long the Agent waits before reporting that the Runner is stuck.
func (s *Supervisor) awaitStop(results <-chan runResult, spec Spec, startedAt time.Time, status Status, reasons []governor.Reason) Outcome {
	timer := time.NewTimer(s.stopGrace)
	defer timer.Stop()
	select {
	case run := <-results:
		outcome := s.finish(run, spec, startedAt, reasons)
		// The stop explains why this Attempt ended, even when the Runner
		// returned at almost the same moment.
		outcome.Status = status
		switch status {
		case StatusReclaimed:
			outcome.Failure = errors.New("stopped by the Local Governor")
		case StatusCancelled:
			outcome.Failure = errors.New("cancelled by the caller")
		case StatusDeadlineExceeded:
			if outcome.Failure == nil {
				outcome.Failure = errors.New("the deadline passed before the Runner returned")
			}
		}
		return outcome
	case <-timer.C:
		return Outcome{
			Status:    StatusRunnerStuck,
			Reasons:   reasons,
			StartedAt: startedAt,
			EndedAt:   s.clock.Now().UTC(),
			Failure:   ErrRunnerStuck,
		}
	}
}

func validateSpec(spec Spec) error {
	switch {
	case spec.TaskID == "":
		return fmt.Errorf("%w: TaskID is required", ErrInvalidSpec)
	case spec.AttemptID == "":
		return fmt.Errorf("%w: AttemptID is required", ErrInvalidSpec)
	case spec.OrderDecisionID == "":
		return fmt.Errorf("%w: Order decision ID is required", ErrInvalidSpec)
	case spec.ExecutorDigest.IsZero():
		return fmt.Errorf("%w: Executor digest is required", ErrInvalidSpec)
	case spec.Limits.CPUMillis == 0:
		return fmt.Errorf("%w: CPU millis must be positive", ErrInvalidSpec)
	case spec.Limits.WallTimeMillis == 0:
		return fmt.Errorf("%w: wall time millis must be positive", ErrInvalidSpec)
	case spec.Deadline.IsZero():
		return fmt.Errorf("%w: deadline is required", ErrInvalidSpec)
	case spec.Sandbox.Profile == "":
		return fmt.Errorf("%w: sandbox profile is required", ErrInvalidSpec)
	case len(spec.Nonce) < decisionpayload.MinWorkOrderNonceBytes || len(spec.Nonce) > decisionpayload.MaxWorkOrderNonceBytes:
		return fmt.Errorf("%w: nonce must contain %d..%d bytes", ErrInvalidSpec, decisionpayload.MinWorkOrderNonceBytes, decisionpayload.MaxWorkOrderNonceBytes)
	}
	return nil
}
