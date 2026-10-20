package reclaim

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
)

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func attempt() Attempt {
	return Attempt{
		Contract: recovery.Contract{
			TaskID:       ids.TaskID("tsk_00000000000000000000000000000001"),
			TaskIRDigest: contract.SHA256([]byte("taskir")),
			Retryable:    true,
			MaxAttempts:  4,
		},
		AttemptID:              ids.AttemptID("atm_00000000000000000000000000000001"),
		AttemptNumber:          1,
		Priority:               recovery.PriorityNormal,
		EstimatedTransferBytes: 1 << 20,
		EstimatedComputeMillis: 100,
	}
}

func TestReclaimableOutcomesBecomeInterruptions(t *testing.T) {
	for _, status := range []executor.Status{
		executor.StatusReclaimed,
		executor.StatusFailed,
		executor.StatusDeadlineExceeded,
	} {
		interruption, err := FromOutcome(executor.Outcome{Status: status}, attempt())
		if err != nil {
			t.Fatalf("%s: FromOutcome() error = %v", status, err)
		}
		if interruption.Contract.TaskID == "" || interruption.AttemptNumber != 1 {
			t.Fatalf("%s: interruption = %+v", status, interruption)
		}
	}
}

func TestIntentionalAndStuckOutcomesAreNotRetriedAutomatically(t *testing.T) {
	cases := map[executor.Status]error{
		executor.StatusCancelled:      ErrIntentionalStop,
		executor.StatusRunnerStuck:    ErrNeedsIntervention,
		executor.StatusCompleted:      ErrNotReclaimable,
		executor.StatusOutputExceeded: ErrNotReclaimable,
		executor.Status("unknown"):    ErrNotReclaimable,
	}
	for status, want := range cases {
		if _, err := FromOutcome(executor.Outcome{Status: status}, attempt()); !errors.Is(err, want) {
			t.Fatalf("%s: error = %v, want %v", status, err, want)
		}
	}
}

func TestInvalidAttemptIsRejected(t *testing.T) {
	base := attempt()
	cases := map[string]func(*Attempt){
		"task":         func(a *Attempt) { a.Contract.TaskID = "" },
		"task ir":      func(a *Attempt) { a.Contract.TaskIRDigest = contract.Digest{} },
		"attempt":      func(a *Attempt) { a.AttemptID = "" },
		"number":       func(a *Attempt) { a.AttemptNumber = 0 },
		"max attempts": func(a *Attempt) { a.Contract.MaxAttempts = 0 },
	}
	for name, mutate := range cases {
		a := base
		mutate(&a)
		if _, err := FromOutcome(executor.Outcome{Status: executor.StatusReclaimed}, a); !errors.Is(err, ErrInvalidAttempt) {
			t.Fatalf("%s: error = %v, want %v", name, err, ErrInvalidAttempt)
		}
	}
}

// TestRecoveredInterruptionFeedsTheRecoveryBoundary checks that a reclaimed
// execution can be classified by the recovery core unchanged.
func TestRecoveredInterruptionFeedsTheRecoveryBoundary(t *testing.T) {
	interruption, err := FromOutcome(executor.Outcome{Status: executor.StatusReclaimed}, attempt())
	if err != nil {
		t.Fatalf("FromOutcome() error = %v", err)
	}
	policy := recovery.Policy{
		Revision: 7, BaseBackoff: time.Second, MaxBackoff: 30 * time.Second,
		Window: time.Second, MaxReleasesPerWindow: 50,
		MaxTransferBytesPerWindow: 1 << 30, MaxComputeMillisPerWindow: 60_000,
		PermitTTL: time.Minute,
	}
	intake, err := recovery.Classify(now, policy, []recovery.Interruption{interruption})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if len(intake.Pending) != 1 || intake.Pending[0].NextAttemptNumber != 2 {
		t.Fatalf("intake = %+v, want one pending attempt 2", intake)
	}
}
