package executor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var baseTime = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type runnerFunc func(ctx context.Context, spec Spec) (Result, error)

func (f runnerFunc) Run(ctx context.Context, spec Spec) (Result, error) { return f(ctx, spec) }

type sourceFunc func(ctx context.Context) (governor.Decision, error)

func (f sourceFunc) Next(ctx context.Context) (governor.Decision, error) { return f(ctx) }

func blockingSource(ctx context.Context) (governor.Decision, error) {
	<-ctx.Done()
	return governor.Decision{}, ctx.Err()
}

func channelSource(ch <-chan governor.Decision) DecisionSource {
	return sourceFunc(func(ctx context.Context) (governor.Decision, error) {
		select {
		case decision := <-ch:
			return decision, nil
		case <-ctx.Done():
			return governor.Decision{}, ctx.Err()
		}
	})
}

func validSpec() Spec {
	return Spec{
		TaskID:          ids.TaskID("tsk_00000000000000000000000000000001"),
		AttemptID:       ids.AttemptID("atm_00000000000000000000000000000001"),
		OrderDecisionID: "wor-decision-001",
		ExecutorDigest:  contract.SHA256([]byte("executable")),
		Limits: decisionpayload.ResourceLimits{
			CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeMillis: 600_000, OutputBytesMaximum: 1024,
		},
		Sandbox:  decisionpayload.SandboxPolicy{Profile: "sandbox/container/v1"},
		Deadline: time.Now().Add(time.Minute),
		Nonce:    []byte("0123456789abcdef"),
	}
}

func newSupervisor(t *testing.T, now time.Time) *Supervisor {
	t.Helper()
	supervisor, err := New(Config{Clock: &fakeClock{now: now}, StopGrace: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return supervisor
}

func TestExecuteCompletesWithinTheDeadline(t *testing.T) {
	supervisor := newSupervisor(t, baseTime)
	outcome, err := supervisor.Execute(context.Background(), validSpec(),
		runnerFunc(func(context.Context, Spec) (Result, error) {
			return Result{ExitCode: 0, OutputBytes: 512}, nil
		}), sourceFunc(blockingSource))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Status != StatusCompleted {
		t.Fatalf("status = %q, want %q (failure: %v)", outcome.Status, StatusCompleted, outcome.Failure)
	}
	if outcome.OutputBytes != 512 || !outcome.StartedAt.Equal(baseTime) || !outcome.EndedAt.Equal(baseTime) {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestExecuteReportsFailure(t *testing.T) {
	supervisor := newSupervisor(t, baseTime)
	runErr := errors.New("executor crashed")

	cases := map[string]struct {
		result   Result
		err      error
		wantCode Status
	}{
		"non-zero exit": {result: Result{ExitCode: 2}, wantCode: StatusFailed},
		"runner error":  {result: Result{}, err: runErr, wantCode: StatusFailed},
	}
	for name, tc := range cases {
		outcome, err := supervisor.Execute(context.Background(), validSpec(),
			runnerFunc(func(context.Context, Spec) (Result, error) { return tc.result, tc.err }), sourceFunc(blockingSource))
		if err != nil {
			t.Fatalf("%s: Execute() error = %v", name, err)
		}
		if outcome.Status != tc.wantCode || outcome.Failure == nil {
			t.Fatalf("%s: outcome = %+v, want %q with a failure", name, outcome, tc.wantCode)
		}
	}
}

func TestExecuteRejectsOversizedOutput(t *testing.T) {
	supervisor := newSupervisor(t, baseTime)
	outcome, err := supervisor.Execute(context.Background(), validSpec(),
		runnerFunc(func(context.Context, Spec) (Result, error) {
			return Result{OutputBytes: 2048}, nil
		}), sourceFunc(blockingSource))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Status != StatusOutputExceeded {
		t.Fatalf("status = %q, want %q", outcome.Status, StatusOutputExceeded)
	}
}

func TestExecuteStopsWhenTheGovernorStopsRunningWork(t *testing.T) {
	supervisor := newSupervisor(t, baseTime)
	decisions := make(chan governor.Decision, 1)
	decisions <- governor.Decision{StopRunning: true, Reasons: []governor.Reason{governor.ReasonUserPresent}}
	outcome, err := supervisor.Execute(context.Background(), validSpec(),
		runnerFunc(func(ctx context.Context, _ Spec) (Result, error) {
			<-ctx.Done()
			return Result{}, ctx.Err()
		}), channelSource(decisions))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Status != StatusReclaimed {
		t.Fatalf("status = %q, want %q", outcome.Status, StatusReclaimed)
	}
	if len(outcome.Reasons) != 1 || outcome.Reasons[0] != governor.ReasonUserPresent {
		t.Fatalf("reasons = %v, want the governor's reason", outcome.Reasons)
	}
}

func TestExecuteStopsAtTheDeadline(t *testing.T) {
	supervisor := newSupervisor(t, time.Now().UTC())
	spec := validSpec()
	spec.Deadline = time.Now().Add(100 * time.Millisecond)
	outcome, err := supervisor.Execute(context.Background(), spec,
		runnerFunc(func(ctx context.Context, _ Spec) (Result, error) {
			<-ctx.Done()
			return Result{}, ctx.Err()
		}), sourceFunc(blockingSource))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Status != StatusDeadlineExceeded {
		t.Fatalf("status = %q, want %q", outcome.Status, StatusDeadlineExceeded)
	}
}

func TestExecuteReportsCallerCancellation(t *testing.T) {
	supervisor := newSupervisor(t, baseTime)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() {
		<-started
		cancel()
	}()
	outcome, err := supervisor.Execute(ctx, validSpec(),
		runnerFunc(func(runCtx context.Context, _ Spec) (Result, error) {
			close(started)
			<-runCtx.Done()
			return Result{}, runCtx.Err()
		}), sourceFunc(blockingSource))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Status != StatusCancelled {
		t.Fatalf("status = %q, want %q", outcome.Status, StatusCancelled)
	}

	preCancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := supervisor.Execute(preCancelled, validSpec(),
		runnerFunc(func(context.Context, Spec) (Result, error) { return Result{}, nil }),
		sourceFunc(blockingSource)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled Execute() error = %v, want context.Canceled", err)
	}
}

func TestExecuteReportsAStuckRunner(t *testing.T) {
	supervisor, err := New(Config{Clock: &fakeClock{now: baseTime}, StopGrace: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	decisions := make(chan governor.Decision, 1)
	decisions <- governor.Decision{StopRunning: true}
	outcome, err := supervisor.Execute(context.Background(), validSpec(),
		runnerFunc(func(context.Context, Spec) (Result, error) {
			<-release
			return Result{}, nil
		}), channelSource(decisions))
	close(release)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Status != StatusRunnerStuck || !errors.Is(outcome.Failure, ErrRunnerStuck) {
		t.Fatalf("outcome = %+v, want %q", outcome, StatusRunnerStuck)
	}
}

func TestExecuteRejectsInvalidInput(t *testing.T) {
	supervisor := newSupervisor(t, baseTime)
	runner := runnerFunc(func(context.Context, Spec) (Result, error) { return Result{}, nil })
	source := sourceFunc(blockingSource)

	if _, err := supervisor.Execute(context.Background(), Spec{}, runner, source); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("empty spec error = %v, want %v", err, ErrInvalidSpec)
	}
	if _, err := supervisor.Execute(context.Background(), validSpec(), nil, source); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("nil runner error = %v, want %v", err, ErrInvalidSpec)
	}
	if _, err := supervisor.Execute(context.Background(), validSpec(), runner, nil); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("nil source error = %v, want %v", err, ErrInvalidSpec)
	}
	badSpec := validSpec()
	badSpec.Nonce = []byte("short")
	if _, err := supervisor.Execute(context.Background(), badSpec, runner, source); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("short nonce error = %v, want %v", err, ErrInvalidSpec)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil clock error = %v, want %v", err, ErrInvalidConfig)
	}
	if _, err := New(Config{Clock: &fakeClock{now: baseTime}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero grace error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestSpecCarriesThePlanBoundFields(t *testing.T) {
	spec := validSpec()
	if spec.OrderDecisionID == "" || spec.ExecutorDigest.IsZero() || len(spec.Nonce) < 16 {
		t.Fatalf("spec = %+v, want the WorkOrder binding fields", spec)
	}
}
