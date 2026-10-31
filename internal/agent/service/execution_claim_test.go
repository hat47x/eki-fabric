package service

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

func validClaimSource() claimSource {
	return claimSource{
		taskID:          ids.TaskID("tsk_00000000000000000000000000000001"),
		taskIRDigest:    contract.SHA256([]byte("task-ir")),
		attemptID:       ids.AttemptID("atm_00000000000000000000000000000001"),
		leaseID:         ids.LeaseID("lea_00000000000000000000000000000001"),
		fenceEpoch:      1,
		orderDecisionID: "order-1",
		executorDigest:  contract.SHA256([]byte("executor")),
		nonce:           make([]byte, 16),
	}
}

func TestClaimFromCompletedOutcome(t *testing.T) {
	now := time.Now().UTC()
	outcome := executor.Outcome{
		Status:       executor.StatusCompleted,
		ExitCode:     0,
		OutputBytes:  5,
		OutputDigest: contract.SHA256([]byte("hello")),
		StartedAt:    now,
		EndedAt:      now.Add(time.Second),
	}
	claim, err := claimFromOutcome(validClaimSource(), "worker-test", outcome)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Status() != resultclaim.StatusCompleted || claim.OutputDigest() != outcome.OutputDigest {
		t.Fatalf("unexpected claim: %+v", claim)
	}
}

func TestClaimFromFailedOutcomeHasNoOutputDigest(t *testing.T) {
	now := time.Now().UTC()
	outcome := executor.Outcome{
		Status:    executor.StatusFailed,
		ExitCode:  7,
		StartedAt: now,
		EndedAt:   now.Add(time.Second),
	}
	claim, err := claimFromOutcome(validClaimSource(), "worker-test", outcome)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Status() != resultclaim.StatusFailed || !claim.OutputDigest().IsZero() {
		t.Fatalf("unexpected claim: %+v", claim)
	}
}
