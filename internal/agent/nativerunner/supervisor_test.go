package nativerunner

import (
	"context"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type blockingDecisions struct{}

func (blockingDecisions) Next(ctx context.Context) (governor.Decision, error) {
	<-ctx.Done()
	return governor.Decision{}, ctx.Err()
}

func validSpec(now time.Time) executor.Spec {
	return executor.Spec{
		TaskID:          ids.TaskID("tsk_00000000000000000000000000000001"),
		AttemptID:       ids.AttemptID("atm_00000000000000000000000000000001"),
		OrderDecisionID: "order-1",
		ExecutorDigest:  contract.SHA256([]byte("executor")),
		Limits: decisionpayload.ResourceLimits{
			// Generous on purpose: this spec asserts completion. The Windows
			// CPU-time rejection has its own test and a 1ms ceiling here only
			// flaked under a loaded machine (a Go helper starts in ~15ms of
			// CPU).
			CPUMillis:          5000,
			WallTimeMillis:     1000,
			OutputBytesMaximum: 100,
		},
		Sandbox:  decisionpayload.SandboxPolicy{Profile: SandboxProfile},
		Deadline: now.Add(time.Minute),
		Nonce:    make([]byte, 16),
	}
}

func TestSupervisorCompletesNativeRunner(t *testing.T) {
	now := time.Now().UTC()
	supervisor, err := executor.New(executor.Config{Clock: fixedClock{now: now}, StopGrace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := supervisor.Execute(context.Background(), validSpec(now), helper("hello"), blockingDecisions{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != executor.StatusCompleted {
		t.Fatalf("status = %s, want completed: %+v", outcome.Status, outcome)
	}
	if outcome.OutputDigest != contract.SHA256([]byte("hello")) {
		t.Fatalf("unexpected output digest: %s", outcome.OutputDigest)
	}
}

func TestSupervisorFailsNativeRunnerExit(t *testing.T) {
	now := time.Now().UTC()
	supervisor, err := executor.New(executor.Config{Clock: fixedClock{now: now}, StopGrace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := supervisor.Execute(context.Background(), validSpec(now), helper("exit7"), blockingDecisions{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != executor.StatusFailed {
		t.Fatalf("status = %s, want failed: %+v", outcome.Status, outcome)
	}
}

// AB-1 (#465): the output bytes come back with the Outcome of the run that
// produced them, and they measure to the digest the Outcome reports. A failed
// run carries no body.
func TestSupervisorOutcomeCarriesTheBodyItsDigestCovers(t *testing.T) {
	now := time.Now().UTC()
	supervisor, err := executor.New(executor.Config{Clock: fixedClock{now: now}, StopGrace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := supervisor.Execute(context.Background(), validSpec(now), helper("hello"), blockingDecisions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(outcome.Body) != "hello" {
		t.Fatalf("body = %q, want hello", outcome.Body)
	}
	if contract.SHA256(outcome.Body) != outcome.OutputDigest {
		t.Fatalf("body does not measure to the Outcome digest %s", outcome.OutputDigest)
	}

	failed, err := supervisor.Execute(context.Background(), validSpec(now), helper("exit7"), blockingDecisions{})
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status == executor.StatusCompleted || failed.Body != nil {
		t.Fatalf("a failed run carried a body: %+v", failed)
	}
}
