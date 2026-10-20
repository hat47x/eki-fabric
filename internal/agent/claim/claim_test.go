package claim

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var (
	baseTime = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	task     = ids.TaskID("tsk_00000000000000000000000000000001")
	attempt  = ids.AttemptID("atm_00000000000000000000000000000001")
	lease    = ids.LeaseID("les_00000000000000000000000000000001")
)

func binding() Attempt {
	return Attempt{
		TaskID:            task,
		TaskIRDigest:      contract.SHA256([]byte("taskir")),
		AttemptID:         attempt,
		WorkerIdentity:    "worker-a",
		LeaseID:           lease,
		FenceEpoch:        7,
		WorkOrderDecision: "wor-decision-001",
		ExecutorDigest:    contract.SHA256([]byte("executable")),
		Nonce:             []byte("0123456789abcdef"),
	}
}

func outcome(status executor.Status, exitCode int) executor.Outcome {
	return executor.Outcome{
		Status: status, ExitCode: exitCode,
		StartedAt: baseTime, EndedAt: baseTime.Add(time.Minute),
	}
}

func TestBuildMapsEveryOutcomeStatus(t *testing.T) {
	cases := map[executor.Status]resultclaim.Status{
		executor.StatusCompleted:        resultclaim.StatusCompleted,
		executor.StatusFailed:           resultclaim.StatusFailed,
		executor.StatusReclaimed:        resultclaim.StatusReclaimed,
		executor.StatusDeadlineExceeded: resultclaim.StatusDeadlineExceeded,
		executor.StatusCancelled:        resultclaim.StatusCancelled,
		executor.StatusOutputExceeded:   resultclaim.StatusOutputExceeded,
		executor.StatusRunnerStuck:      resultclaim.StatusRunnerStuck,
	}
	for input, want := range cases {
		exit := 0
		if input != executor.StatusCompleted {
			exit = 1
		}
		claim, err := Build(outcome(input, exit), binding())
		if err != nil {
			t.Fatalf("%s: Build() error = %v", input, err)
		}
		if claim.Status() != want {
			t.Fatalf("%s: status = %q, want %q", input, claim.Status(), want)
		}
		if claim.TaskID() != task || claim.AttemptID() != attempt || claim.LeaseID() != lease || claim.FenceEpoch() != 7 {
			t.Fatalf("%s: claim binding = %+v", input, claim)
		}
	}
}

func TestBuildRejectsUnknownStatus(t *testing.T) {
	if _, err := Build(outcome(executor.Status("dropped"), 0), binding()); !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("error = %v, want %v", err, ErrUnknownStatus)
	}
}

func TestBuildRejectsIncompleteBinding(t *testing.T) {
	cases := map[string]func(*Attempt){
		"task":    func(a *Attempt) { a.TaskID = "" },
		"task ir": func(a *Attempt) { a.TaskIRDigest = contract.Digest{} },
		"attempt": func(a *Attempt) { a.AttemptID = "" },
	}
	for name, mutate := range cases {
		attempt := binding()
		mutate(&attempt)
		if _, err := Build(outcome(executor.StatusReclaimed, 0), attempt); !errors.Is(err, ErrInvalidAttempt) {
			t.Fatalf("%s: error = %v, want %v", name, err, ErrInvalidAttempt)
		}
	}
}

func TestBuildRejectsMissingTimeWindow(t *testing.T) {
	broken := outcome(executor.StatusFailed, 1)
	broken.EndedAt = time.Time{}
	if _, err := Build(broken, binding()); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidAttempt)
	}
}

func TestBuildKeepsResultClaimValidation(t *testing.T) {
	// A "completed" outcome with a non-zero exit code is not a valid claim;
	// the builder must not repair it silently.
	if _, err := Build(outcome(executor.StatusCompleted, 2), binding()); !errors.Is(err, resultclaim.ErrInvalidClaim) {
		t.Fatalf("error = %v, want %v", err, resultclaim.ErrInvalidClaim)
	}
}
