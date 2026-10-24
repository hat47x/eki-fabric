package statestoretest

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/rejectedresult"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// ResultRejectionTestStore is a store that can take a Task to an Attempt in
// verification and then record a rejection of its result.
type ResultRejectionTestStore interface {
	ResultAcceptanceTestStore
	statestore.ResultRejectionStore
}

type ResultRejectionFactory func(t testing.TB) ResultRejectionTestStore

type DurableResultRejectionFactory struct {
	Open func(t testing.TB, location string) (store ResultRejectionTestStore, close func() error)
}

// RunResultRejectionContract fixes what a store must guarantee when RAC-1
// rejects a result: the reason is recorded together with the Attempt's close,
// and the Task is left for recovery.
func RunResultRejectionContract(t *testing.T, newStore ResultRejectionFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("commit-records-the-reason-and-closes-the-attempt-together", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "reject", acceptanceOptions{})
		commit := rejectionCommit(t, fx)
		if err := store.CommitRejectedResult(ctx, commit); err != nil {
			t.Fatalf("CommitRejectedResult() error = %v", err)
		}
		loaded, ok, err := store.LoadRejectedResult(ctx, fx.attempt.ID)
		if err != nil || !ok || !loaded.Equal(commit.Result) {
			t.Fatalf("LoadRejectedResult() = %+v, %v, %v", loaded, ok, err)
		}
		if d := loaded.Dissent(); len(d) != 1 || d[0].VerifierID != "authority-1" ||
			d[0].Check != resultaccept.CheckValidatorRejected || d[0].ObservedDigest != fx.claim.OutputDigest().Hex() {
			t.Fatalf("dissent = %+v", d)
		}
		if e := loaded.Excluded(); len(e) != 1 || e[0].VerifierID != "verifier-z" {
			t.Fatalf("excluded = %+v", e)
		}
		attempt, err := store.LoadAttempt(ctx, fx.attempt.ID)
		if err != nil || attempt.Phase != attemptstate.AttemptPhaseClosed ||
			attempt.CloseReason != attemptstate.CloseReasonResultRejected || attempt.Revision != fx.attempt.Revision+1 {
			t.Fatalf("Attempt after rejection = %+v, %v", attempt, err)
		}
		task, err := store.LoadTask(ctx, fx.task.ID)
		if err != nil || task.Phase != fx.task.Phase || task.Revision != fx.task.Revision {
			t.Fatalf("the rejection changed the Task: %+v, %v", task, err)
		}
		if _, ok, err := store.LoadAcceptedResult(ctx, fx.task.ID); err != nil || ok {
			t.Fatalf("a rejection produced an accepted result: %v, %v", ok, err)
		}
	})

	t.Run("an-attempt-has-no-rejected-result-until-one-is-committed", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "reject-none", acceptanceOptions{})
		if _, ok, err := store.LoadRejectedResult(ctx, fx.attempt.ID); err != nil || ok {
			t.Fatalf("LoadRejectedResult() = %v, %v, want none", ok, err)
		}
	})

	t.Run("exact-replay-reports-already-exists", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "reject-replay", acceptanceOptions{})
		commit := rejectionCommit(t, fx)
		if err := store.CommitRejectedResult(ctx, commit); err != nil {
			t.Fatal(err)
		}
		if err := store.CommitRejectedResult(ctx, commit); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("replay error = %v, want %v", err, statestore.ErrAlreadyExists)
		}
		other := commit
		other.Result = mustRejectedResult(t, fx, func(c *rejectedresult.Config) { c.Verifiers = []string{"verifier-c"} })
		if err := store.CommitRejectedResult(ctx, other); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("different rejection error = %v, want %v", err, statestore.ErrConflict)
		}
	})

	t.Run("a-stale-attempt-revision-conflicts-and-changes-nothing", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "reject-stale", acceptanceOptions{})
		commit := rejectionCommit(t, fx)
		commit.ExpectedAttemptRevision--
		commit.NextAttempt.Revision--
		assertRejectionRefused(t, store, fx, commit, statestore.ErrConflict)
	})

	t.Run("only-an-attempt-in-verification-can-be-rejected", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "reject-claimed", acceptanceOptions{stopAt: attemptstate.AttemptPhaseClaimed})
		assertRejectionRefused(t, store, fx, rejectionCommit(t, fx), statestore.ErrConflict)
	})

	t.Run("a-rejection-names-the-claim-the-worker-reported", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "reject-claim-digest", acceptanceOptions{})
		commit := rejectionCommit(t, fx)
		commit.Result = mustRejectedResult(t, fx, func(c *rejectedresult.Config) {
			c.ClaimDigest = contract.SHA256([]byte("another claim"))
		})
		assertRejectionRefused(t, store, fx, commit, statestore.ErrConflict)

		noClaim := newAcceptanceFixture(t, store, "reject-no-claim", acceptanceOptions{withoutClaim: true})
		assertRejectionRefused(t, store, noClaim, rejectionCommit(t, noClaim), statestore.ErrConflict)

		failed := newAcceptanceFixture(t, store, "reject-failed-claim", acceptanceOptions{claimStatus: resultclaim.StatusFailed})
		assertRejectionRefused(t, store, failed, rejectionCommit(t, failed), statestore.ErrConflict)
	})

	t.Run("a-malformed-commit-is-refused-before-any-state-is-read", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "reject-malformed", acceptanceOptions{})
		cases := map[string]func(*statestore.ResultRejectionCommit){
			"attempt closed as accepted": func(c *statestore.ResultRejectionCommit) {
				c.NextAttempt.CloseReason = attemptstate.CloseReasonResultAccepted
			},
			"attempt left open": func(c *statestore.ResultRejectionCommit) {
				c.NextAttempt.Phase = attemptstate.AttemptPhaseVerifying
				c.NextAttempt.CloseReason = attemptstate.CloseReasonNone
			},
			"no record":         func(c *statestore.ResultRejectionCommit) { c.Result = rejectedresult.Record{} },
			"revision mismatch": func(c *statestore.ResultRejectionCommit) { c.NextAttempt.Revision++ },
		}
		for name, mutate := range cases {
			commit := rejectionCommit(t, fx)
			mutate(&commit)
			if err := store.CommitRejectedResult(ctx, commit); !errors.Is(err, statestore.ErrInvalidAcceptance) {
				t.Fatalf("%s: error = %v, want %v", name, err, statestore.ErrInvalidAcceptance)
			}
		}
		assertNothingRejected(t, store, fx)
	})
}

// RunResultRejectionDurabilityContract fixes that a rejection and its closed
// Attempt survive a restart together, and that a crash before the commit
// leaves neither.
func RunResultRejectionDurabilityContract(t *testing.T, factory DurableResultRejectionFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("a-rejection-survives-restart-with-its-attempt", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		fx := newAcceptanceFixture(t, store, "reject-restart", acceptanceOptions{})
		commit := rejectionCommit(t, fx)

		// A crash before the commit: the store is closed with nothing written.
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}
		reopened, closeReopened := factory.Open(t, location)
		assertNothingRejected(t, reopened, fx)
		if err := reopened.CommitRejectedResult(ctx, commit); err != nil {
			t.Fatalf("CommitRejectedResult() after restart error = %v", err)
		}
		if err := closeReopened(); err != nil {
			t.Fatal(err)
		}

		again, closeAgain := factory.Open(t, location)
		defer func() { _ = closeAgain() }()
		loaded, ok, err := again.LoadRejectedResult(ctx, fx.attempt.ID)
		if err != nil || !ok || !loaded.Equal(commit.Result) {
			t.Fatalf("rejected result after restart = %+v, %v, %v", loaded, ok, err)
		}
		attempt, err := again.LoadAttempt(ctx, fx.attempt.ID)
		if err != nil || attempt.Phase != attemptstate.AttemptPhaseClosed ||
			attempt.CloseReason != attemptstate.CloseReasonResultRejected {
			t.Fatalf("Attempt after restart = %+v, %v", attempt, err)
		}
		if err := again.CommitRejectedResult(ctx, commit); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("replay after restart error = %v, want %v", err, statestore.ErrAlreadyExists)
		}
	})
}

func mustRejectedResult(t testing.TB, fx acceptanceFixture, mutate func(*rejectedresult.Config)) rejectedresult.Record {
	t.Helper()
	output := fx.claim.OutputDigest()
	if output.IsZero() {
		output = contract.SHA256([]byte("hello"))
	}
	cfg := rejectedresult.Config{
		TaskID: fx.task.ID, AttemptID: fx.attempt.ID,
		ClaimDigest: fx.claim.Digest(), OutputDigest: output,
		PolicyDigest: contract.SHA256([]byte("result policy")),
		Reason:       resultaccept.ReasonAuthorityRejected,
		Dissent: []rejectedresult.Dissent{{
			VerifierID: "authority-1", Check: resultaccept.CheckValidatorRejected, ObservedDigest: output.Hex(),
		}},
		Verifiers:  []string{"verifier-b", "verifier-a", "authority-1"},
		Excluded:   []rejectedresult.Exclusion{{VerifierID: "verifier-z", Reason: "same principal as the producing worker"}},
		RejectedAt: fx.attempt.UpdatedAt.Add(2 * time.Second),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	record, err := rejectedresult.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func rejectionCommit(t testing.TB, fx acceptanceFixture) statestore.ResultRejectionCommit {
	t.Helper()
	next := fx.attempt
	next.Phase = attemptstate.AttemptPhaseClosed
	next.CloseReason = attemptstate.CloseReasonResultRejected
	next.Revision++
	next.UpdatedAt = fx.attempt.UpdatedAt.Add(2 * time.Second)
	return statestore.ResultRejectionCommit{
		Result:                  mustRejectedResult(t, fx, nil),
		ExpectedAttemptRevision: fx.attempt.Revision,
		NextAttempt:             next,
	}
}

func assertRejectionRefused(t testing.TB, store ResultRejectionTestStore, fx acceptanceFixture, commit statestore.ResultRejectionCommit, want error) {
	t.Helper()
	if err := store.CommitRejectedResult(context.Background(), commit); !errors.Is(err, want) {
		t.Fatalf("CommitRejectedResult() error = %v, want %v", err, want)
	}
	assertNothingRejected(t, store, fx)
}

func assertNothingRejected(t testing.TB, store ResultRejectionTestStore, fx acceptanceFixture) {
	t.Helper()
	ctx := context.Background()
	if _, ok, err := store.LoadRejectedResult(ctx, fx.attempt.ID); err != nil || ok {
		t.Fatalf("a rejected result was recorded: %v, %v", ok, err)
	}
	attempt, err := store.LoadAttempt(ctx, fx.attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Phase != fx.attempt.Phase || attempt.Revision != fx.attempt.Revision || attempt.CloseReason != fx.attempt.CloseReason {
		t.Fatalf("the Attempt changed: %+v, want %+v", attempt, fx.attempt)
	}
}
