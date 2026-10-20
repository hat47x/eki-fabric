package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func sqliteClaimFixture(t *testing.T, store *Store) (attemptstate.AttemptRecord, resultclaim.Claim, attemptstate.AttemptRecord) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	task := newTaskRecord(t, "result-claim")
	task.Phase = taskstate.TaskPhaseReady
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptstate.New(mustAttemptIDForSQLite(t), task.ID, task.TaskIRDigest, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: attempt.TaskID, TaskIRDigest: attempt.TaskIRDigest, AttemptID: attempt.ID,
		WorkerIdentity: "worker-a", LeaseID: ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch: 7, WorkOrderDecision: "wor-001",
		ExecutorDigest: contract.SHA256([]byte("executable")),
		Nonce:          []byte("0123456789abcdef"), Status: resultclaim.StatusFailed,
		ExitCode: 1, OutputBytes: 10,
		StartedAt: now, EndedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	next := attempt
	next.Phase = attemptstate.AttemptPhaseClosed
	next.CloseReason = attemptstate.CloseReasonExecutionFailureConfirmed
	next.Revision++
	next.UpdatedAt = now.Add(time.Second)
	return attempt, claim, next
}

func TestApplyResultClaimCommitsTheClaimAndTheAttemptTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store := openStore(t, path)
	attempt, claim, next := sqliteClaimFixture(t, store)
	ctx := context.Background()

	if err := store.ApplyResultClaim(ctx, statestore.ResultClaimAcceptance{
		Claim: claim, Record: true, ApplyTransition: true,
		ExpectedAttemptRevision: attempt.Revision, NextAttempt: next,
	}); err != nil {
		t.Fatalf("ApplyResultClaim() error = %v", err)
	}
	stored, ok, err := store.LoadResultClaim(ctx, attempt.ID)
	if err != nil || !ok || !stored.Equal(claim) {
		t.Fatalf("LoadResultClaim() = %+v, %v, %v", stored, ok, err)
	}
	updated, err := store.LoadAttempt(ctx, attempt.ID)
	if err != nil || updated.Phase != attemptstate.AttemptPhaseClosed {
		t.Fatalf("LoadAttempt() = %+v, %v", updated, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openStore(t, path)
	defer reopened.Close()
	restored, ok, err := reopened.LoadResultClaim(ctx, attempt.ID)
	if err != nil || !ok || !restored.Equal(claim) {
		t.Fatalf("claim did not survive restart: %+v, %v, %v", restored, ok, err)
	}
}

func TestApplyResultClaimRollsBackOnAStaleAttempt(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	attempt, claim, next := sqliteClaimFixture(t, store)
	ctx := context.Background()

	err := store.ApplyResultClaim(ctx, statestore.ResultClaimAcceptance{
		Claim: claim, Record: true, ApplyTransition: true,
		ExpectedAttemptRevision: attempt.Revision + 5, NextAttempt: next,
	})
	if !errors.Is(err, statestore.ErrConflict) {
		t.Fatalf("ApplyResultClaim() error = %v, want %v", err, statestore.ErrConflict)
	}
	if _, ok, _ := store.LoadResultClaim(ctx, attempt.ID); ok {
		t.Fatal("the claim was recorded despite the failed transition")
	}
	current, err := store.LoadAttempt(ctx, attempt.ID)
	if err != nil || current.Phase != attemptstate.AttemptPhaseCreated {
		t.Fatalf("the Attempt changed: %+v, %v", current, err)
	}
}

func TestApplyResultClaimRejectsASecondRecordAndADifferentClaim(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	attempt, claim, next := sqliteClaimFixture(t, store)
	ctx := context.Background()
	acceptance := statestore.ResultClaimAcceptance{
		Claim: claim, Record: true, ApplyTransition: true,
		ExpectedAttemptRevision: attempt.Revision, NextAttempt: next,
	}
	if err := store.ApplyResultClaim(ctx, acceptance); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyResultClaim(ctx, acceptance); !errors.Is(err, statestore.ErrAlreadyExists) {
		t.Fatalf("second apply error = %v, want %v", err, statestore.ErrAlreadyExists)
	}

	other, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: claim.TaskID(), TaskIRDigest: claim.TaskIRDigest(), AttemptID: claim.AttemptID(),
		WorkerIdentity: claim.WorkerIdentity(), LeaseID: claim.LeaseID(), FenceEpoch: claim.FenceEpoch(),
		WorkOrderDecision: claim.WorkOrderDecision(), ExecutorDigest: claim.ExecutorDigest(),
		Nonce: claim.Nonce(), Status: claim.Status(), ExitCode: claim.ExitCode(), OutputBytes: 11,
		StartedAt: claim.StartedAt(), EndedAt: claim.EndedAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyResultClaim(ctx, statestore.ResultClaimAcceptance{Claim: other, Record: true}); !errors.Is(err, statestore.ErrConflict) {
		t.Fatalf("different claim error = %v, want %v", err, statestore.ErrConflict)
	}
}

func TestApplyResultClaimRejectsInvalidTransition(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	attempt, claim, next := sqliteClaimFixture(t, store)
	next.Revision = attempt.Revision // invalid: must advance by one
	if err := store.ApplyResultClaim(context.Background(), statestore.ResultClaimAcceptance{
		Claim: claim, Record: true, ApplyTransition: true,
		ExpectedAttemptRevision: attempt.Revision, NextAttempt: next,
	}); !errors.Is(err, statestore.ErrConflict) {
		t.Fatalf("invalid transition error = %v, want %v", err, statestore.ErrConflict)
	}
}
