// Package claim builds the ResultClaim that the Agent sends after an Attempt
// ends.
//
// The mapping is explicit so that a new outcome status cannot be silently
// dropped or turned into "completed". The Claim is then validated by the
// resultclaim package; this builder never relaxes those rules.
package claim

import (
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var (
	ErrUnknownStatus  = errors.New("the execution outcome has no result claim status")
	ErrInvalidAttempt = errors.New("invalid attempt binding for the result claim")
)

// Attempt is the durable binding the Agent holds for the finished execution.
// It comes from the Admission and the local Lease record, not from the Outcome.
type Attempt struct {
	TaskID            ids.TaskID
	TaskIRDigest      contract.Digest
	AttemptID         ids.AttemptID
	WorkerIdentity    string
	LeaseID           ids.LeaseID
	FenceEpoch        uint64
	WorkOrderDecision string
	ExecutorDigest    contract.Digest
	Nonce             []byte
}

// Build converts one Outcome into a validated Claim.
func Build(outcome executor.Outcome, attempt Attempt) (resultclaim.Claim, error) {
	if attempt.TaskID == "" || attempt.TaskIRDigest.IsZero() || attempt.AttemptID == "" {
		return resultclaim.Claim{}, fmt.Errorf("%w: Task and Attempt are required", ErrInvalidAttempt)
	}
	status, ok := statusOf(outcome.Status)
	if !ok {
		return resultclaim.Claim{}, fmt.Errorf("%w: %q", ErrUnknownStatus, outcome.Status)
	}
	startedAt := outcome.StartedAt
	endedAt := outcome.EndedAt
	if startedAt.IsZero() || endedAt.IsZero() {
		return resultclaim.Claim{}, fmt.Errorf("%w: the outcome has no time window", ErrInvalidAttempt)
	}
	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID:            attempt.TaskID,
		TaskIRDigest:      attempt.TaskIRDigest,
		AttemptID:         attempt.AttemptID,
		WorkerIdentity:    attempt.WorkerIdentity,
		LeaseID:           attempt.LeaseID,
		FenceEpoch:        attempt.FenceEpoch,
		WorkOrderDecision: attempt.WorkOrderDecision,
		ExecutorDigest:    attempt.ExecutorDigest,
		Nonce:             attempt.Nonce,
		Status:            status,
		ExitCode:          outcome.ExitCode,
		OutputBytes:       outcome.OutputBytes,
		StartedAt:         startedAt,
		EndedAt:           endedAt,
	})
	if err != nil {
		return resultclaim.Claim{}, err
	}
	return claim, nil
}

func statusOf(status executor.Status) (resultclaim.Status, bool) {
	switch status {
	case executor.StatusCompleted:
		return resultclaim.StatusCompleted, true
	case executor.StatusFailed:
		return resultclaim.StatusFailed, true
	case executor.StatusReclaimed:
		return resultclaim.StatusReclaimed, true
	case executor.StatusDeadlineExceeded:
		return resultclaim.StatusDeadlineExceeded, true
	case executor.StatusCancelled:
		return resultclaim.StatusCancelled, true
	case executor.StatusOutputExceeded:
		return resultclaim.StatusOutputExceeded, true
	case executor.StatusRunnerStuck:
		return resultclaim.StatusRunnerStuck, true
	}
	return "", false
}
