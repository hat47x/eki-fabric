package nativerunner

import (
	"context"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
)

// stopGraceForTest is generous on purpose. These tests assert which status the
// Supervisor reports, not how fast the platform stops a process. On Windows the
// stop path spawns taskkill to kill the process tree, and a loaded machine can
// take longer than a second to finish that, which flaked these tests as
// runner_stuck. The Agent's default grace is 5 seconds for the same reason.
const stopGraceForTest = 10 * time.Second

func TestSupervisorReportsDeadline(t *testing.T) {
	now := time.Now().UTC()
	supervisor, err := executor.New(executor.Config{Clock: fixedClock{now: now}, StopGrace: stopGraceForTest})
	if err != nil {
		t.Fatal(err)
	}
	spec := validSpec(now)
	spec.Deadline = now.Add(200 * time.Millisecond)
	outcome, err := supervisor.Execute(context.Background(), spec, helper("sleep"), blockingDecisions{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != executor.StatusDeadlineExceeded {
		t.Fatalf("status = %s, want deadline_exceeded: %+v", outcome.Status, outcome)
	}
}

func TestSupervisorReportsCancellation(t *testing.T) {
	now := time.Now().UTC()
	supervisor, err := executor.New(executor.Config{Clock: fixedClock{now: now}, StopGrace: stopGraceForTest})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	outcome, err := supervisor.Execute(ctx, validSpec(now), helper("sleep"), blockingDecisions{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != executor.StatusCancelled {
		t.Fatalf("status = %s, want cancelled: %+v", outcome.Status, outcome)
	}
}
