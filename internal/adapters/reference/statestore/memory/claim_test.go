package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var claimTime = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)

func claimAttempt(t *testing.T) attemptstate.AttemptRecord {
	t.Helper()
	record, err := attemptstate.New(ids.AttemptID("atm_00000000000000000000000000000001"),
		ids.TaskID("tsk_00000000000000000000000000000001"),
		contract.SHA256([]byte("taskir")), 1, claimTime)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func failedClaim(t *testing.T, attempt attemptstate.AttemptRecord) resultclaim.Claim {
	t.Helper()
	return failedClaimWithOutput(t, attempt, 10)
}

func failedClaimWithOutput(t *testing.T, attempt attemptstate.AttemptRecord, output uint64) resultclaim.Claim {
	t.Helper()
	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: attempt.TaskID, TaskIRDigest: attempt.TaskIRDigest, AttemptID: attempt.ID,
		WorkerIdentity: "worker-a", LeaseID: ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch: 7, WorkOrderDecision: "wor-001",
		ExecutorDigest: contract.SHA256([]byte("executable")),
		Nonce:          []byte("0123456789abcdef"), Status: resultclaim.StatusFailed,
		ExitCode: 1, OutputBytes: output,
		StartedAt: claimTime, EndedAt: claimTime.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

func closedAttempt(t *testing.T, attempt attemptstate.AttemptRecord) attemptstate.AttemptRecord {
	t.Helper()
	next := attempt
	next.Phase = attemptstate.AttemptPhaseClosed
	next.CloseReason = attemptstate.CloseReasonExecutionFailureConfirmed
	next.Revision++
	next.UpdatedAt = claimTime.Add(time.Second)
	return next
}

func TestApplyResultClaimRecordsBothAtomically(t *testing.T) {
	store := New()
	attempt := claimAttempt(t)
	store.attempts[attempt.ID] = attempt
	claim := failedClaim(t, attempt)
	next := closedAttempt(t, attempt)

	if err := store.ApplyResultClaim(context.Background(), statestore.ResultClaimAcceptance{
		Claim: claim, Record: true, ApplyTransition: true,
		ExpectedAttemptRevision: attempt.Revision, NextAttempt: next,
	}); err != nil {
		t.Fatalf("ApplyResultClaim() error = %v", err)
	}
	stored, ok, err := store.LoadResultClaim(context.Background(), attempt.ID)
	if err != nil || !ok || !stored.Equal(claim) {
		t.Fatalf("LoadResultClaim() = %+v, %v, %v", stored, ok, err)
	}
	if store.attempts[attempt.ID].Phase != attemptstate.AttemptPhaseClosed {
		t.Fatal("Attempt did not close with the claim")
	}
}

func TestApplyResultClaimRejectsAStaleAttemptWithoutRecording(t *testing.T) {
	store := New()
	attempt := claimAttempt(t)
	store.attempts[attempt.ID] = attempt
	claim := failedClaim(t, attempt)

	err := store.ApplyResultClaim(context.Background(), statestore.ResultClaimAcceptance{
		Claim: claim, Record: true, ApplyTransition: true,
		ExpectedAttemptRevision: attempt.Revision + 5, NextAttempt: closedAttempt(t, attempt),
	})
	if !errors.Is(err, statestore.ErrConflict) {
		t.Fatalf("ApplyResultClaim() error = %v, want %v", err, statestore.ErrConflict)
	}
	if _, ok, _ := store.LoadResultClaim(context.Background(), attempt.ID); ok {
		t.Fatal("a failed atomic apply recorded the claim")
	}
	if store.attempts[attempt.ID].Phase != attemptstate.AttemptPhaseCreated {
		t.Fatal("a failed atomic apply changed the Attempt")
	}
}

func TestApplyResultClaimRejectsASecondRecord(t *testing.T) {
	store := New()
	attempt := claimAttempt(t)
	store.attempts[attempt.ID] = attempt
	claim := failedClaim(t, attempt)
	acceptance := statestore.ResultClaimAcceptance{
		Claim: claim, Record: true, ApplyTransition: true,
		ExpectedAttemptRevision: attempt.Revision, NextAttempt: closedAttempt(t, attempt),
	}
	if err := store.ApplyResultClaim(context.Background(), acceptance); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyResultClaim(context.Background(), acceptance); !errors.Is(err, statestore.ErrAlreadyExists) {
		t.Fatalf("second ApplyResultClaim() error = %v, want %v", err, statestore.ErrAlreadyExists)
	}
}

func TestApplyResultClaimRejectsADifferentClaimForTheSameAttempt(t *testing.T) {
	store := New()
	attempt := claimAttempt(t)
	store.attempts[attempt.ID] = attempt
	store.claims[attempt.ID] = failedClaim(t, attempt)

	other := failedClaimWithOutput(t, attempt, 11)
	if err := store.ApplyResultClaim(context.Background(), statestore.ResultClaimAcceptance{
		Claim: other, Record: true,
	}); !errors.Is(err, statestore.ErrConflict) {
		t.Fatalf("different claim error = %v, want %v", err, statestore.ErrConflict)
	}
}
