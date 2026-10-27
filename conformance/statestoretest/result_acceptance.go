package statestoretest

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/acceptedresult"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// ResultAcceptanceTestStore is a store that can take a Task to an Attempt in
// verification and then accept the result.
type ResultAcceptanceTestStore interface {
	TaskLeaseStore
	statestore.ResultClaimStore
	statestore.ResultAcceptanceStore
}

type ResultAcceptanceFactory func(t testing.TB) ResultAcceptanceTestStore

type DurableResultAcceptanceFactory struct {
	Open func(t testing.TB, location string) (store ResultAcceptanceTestStore, close func() error)
}

// acceptanceFixture is one Task with one Attempt, and the claim the Worker
// reported for it, as the store holds them before any acceptance.
type acceptanceFixture struct {
	task    taskstate.TaskRecord
	attempt attemptstate.AttemptRecord
	claim   resultclaim.Claim
}

type acceptanceOptions struct {
	// stopAt is the phase the Attempt is left in. The zero value is A-VERIFYING.
	stopAt attemptstate.AttemptPhase
	// claimStatus is the status of the recorded claim. The zero value is completed.
	claimStatus resultclaim.Status
	// withoutClaim leaves the Attempt with no recorded claim.
	withoutClaim bool
}

// RunResultAcceptanceContract fixes what a store must guarantee when a verified
// result is promoted to an AcceptedResult.
func RunResultAcceptanceContract(t *testing.T, newStore ResultAcceptanceFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("commit-completes-the-task-and-closes-the-attempt-together", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept", acceptanceOptions{})
		commit := acceptanceCommit(t, fx)

		if err := store.CommitAcceptedResult(ctx, commit); err != nil {
			t.Fatalf("CommitAcceptedResult() error = %v", err)
		}
		loaded, ok, err := store.LoadAcceptedResult(ctx, fx.task.ID)
		if err != nil || !ok || !loaded.Equal(commit.Result) {
			t.Fatalf("LoadAcceptedResult() = %+v, %v, %v", loaded, ok, err)
		}
		attempt, err := store.LoadAttempt(ctx, fx.attempt.ID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.Phase != attemptstate.AttemptPhaseClosed ||
			attempt.CloseReason != attemptstate.CloseReasonResultAccepted ||
			attempt.Revision != fx.attempt.Revision+1 {
			t.Fatalf("Attempt after acceptance = %+v", attempt)
		}
		task, err := store.LoadTask(ctx, fx.task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Phase != taskstate.TaskPhaseTerminal ||
			task.TerminalOutcome != taskstate.TerminalOutcomeCompleted ||
			task.Revision != fx.task.Revision+1 {
			t.Fatalf("Task after acceptance = %+v", task)
		}
	})

	t.Run("a-task-has-no-accepted-result-until-one-is-committed", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-none", acceptanceOptions{})
		if _, ok, err := store.LoadAcceptedResult(ctx, fx.task.ID); err != nil || ok {
			t.Fatalf("LoadAcceptedResult() = %v, %v, want none", ok, err)
		}
	})

	t.Run("exact-replay-reports-already-exists-and-changes-nothing", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-replay", acceptanceOptions{})
		commit := acceptanceCommit(t, fx)
		if err := store.CommitAcceptedResult(ctx, commit); err != nil {
			t.Fatal(err)
		}
		if err := store.CommitAcceptedResult(ctx, commit); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("replay error = %v, want %v", err, statestore.ErrAlreadyExists)
		}
	})

	t.Run("a-different-result-for-the-same-task-conflicts", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-different", acceptanceOptions{})
		commit := acceptanceCommit(t, fx)
		if err := store.CommitAcceptedResult(ctx, commit); err != nil {
			t.Fatal(err)
		}
		other := commit
		other.Result = mustAcceptedResult(t, fx, func(c *acceptedresult.Config) {
			c.Level = resultaccept.LevelRA2
			c.Verifiers = []string{"verifier-c"}
		})
		if err := store.CommitAcceptedResult(ctx, other); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("different result error = %v, want %v", err, statestore.ErrConflict)
		}
		loaded, _, err := store.LoadAcceptedResult(ctx, fx.task.ID)
		if err != nil || !loaded.Equal(commit.Result) {
			t.Fatalf("the first accepted result was replaced: %+v, %v", loaded, err)
		}
	})

	t.Run("a-stale-attempt-revision-conflicts-and-changes-nothing", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-stale-attempt", acceptanceOptions{})
		commit := acceptanceCommit(t, fx)
		commit.ExpectedAttemptRevision--
		commit.NextAttempt.Revision--
		assertAcceptanceRejected(t, store, fx, commit, statestore.ErrConflict)
	})

	t.Run("a-stale-task-revision-conflicts-and-changes-nothing", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-stale-task", acceptanceOptions{})
		commit := acceptanceCommit(t, fx)
		commit.ExpectedTaskRevision--
		commit.NextTask.Revision--
		assertAcceptanceRejected(t, store, fx, commit, statestore.ErrConflict)
	})

	t.Run("an-attempt-whose-lease-fence-was-superseded-is-not-accepted", func(t *testing.T) {
		// V3: A1 reached verification, its Lease expired, and the Task was
		// placed again on A2 under a newer FenceEpoch. A1 no longer holds the
		// Task's execution authority, so its result must not complete the
		// Task, even though its claim and evidence are well formed.
		// The refusal is ErrStaleFence, not a revision conflict to retry.
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-superseded-fence", acceptanceOptions{})
		fx = supersedeAcceptanceFixture(t, store, fx, "accept-superseded-fence")
		err := store.CommitAcceptedResult(ctx, acceptanceCommit(t, fx))
		if !errors.Is(err, statestore.ErrStaleFence) || errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("CommitAcceptedResult() error = %v, want %v and not %v", err, statestore.ErrStaleFence, statestore.ErrConflict)
		}
		assertNothingAccepted(t, store, fx)
	})

	t.Run("a-superseded-attempt-in-verification-closes-as-stale", func(t *testing.T) {
		// The Attempt would otherwise wait in A-VERIFYING forever. Its close
		// reason is not an interruption, so it does not enter recovery and does
		// not consume the Task's attempt budget again.
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "stale-close", acceptanceOptions{})
		fx = supersedeAcceptanceFixture(t, store, fx, "stale-close")
		stale := staleClose(fx)
		if err := store.CloseStaleAttempt(ctx, stale); err != nil {
			t.Fatalf("CloseStaleAttempt() error = %v", err)
		}
		attempt, err := store.LoadAttempt(ctx, fx.attempt.ID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.Phase != attemptstate.AttemptPhaseClosed || attempt.CloseReason != attemptstate.CloseReasonStaleAttempt ||
			attempt.Revision != fx.attempt.Revision+1 {
			t.Fatalf("Attempt = %+v, want closed as %s", attempt, attemptstate.CloseReasonStaleAttempt)
		}
		if attempt.CloseReason.IsInterruption() {
			t.Fatalf("%s must not be an interruption", attempt.CloseReason)
		}
		task, err := store.LoadTask(ctx, fx.task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Phase != fx.task.Phase || task.Revision != fx.task.Revision {
			t.Fatalf("the Task changed: %+v, want %+v", task, fx.task)
		}
		if _, ok, err := store.LoadAcceptedResult(ctx, fx.task.ID); err != nil || ok {
			t.Fatalf("an accepted result was recorded: %v, %v", ok, err)
		}
		// A repeat is a conflict: the Attempt is no longer in verification.
		if err := store.CloseStaleAttempt(ctx, stale); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("repeat CloseStaleAttempt() error = %v, want %v", err, statestore.ErrConflict)
		}
	})

	t.Run("an-attempt-holding-the-latest-fence-is-not-closed-as-stale", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "stale-close-current", acceptanceOptions{})
		if err := store.CloseStaleAttempt(ctx, staleClose(fx)); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("CloseStaleAttempt() error = %v, want %v", err, statestore.ErrConflict)
		}
		assertNothingAccepted(t, store, fx)
		// The Attempt can still be accepted.
		if err := store.CommitAcceptedResult(ctx, acceptanceCommit(t, fx)); err != nil {
			t.Fatalf("CommitAcceptedResult() error = %v", err)
		}
	})

	t.Run("a-stale-close-needs-the-attempt-in-verification-at-its-revision", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "stale-close-revision", acceptanceOptions{})
		fx = supersedeAcceptanceFixture(t, store, fx, "stale-close-revision")
		stale := staleClose(fx)
		stale.ExpectedAttemptRevision--
		stale.NextAttempt.Revision--
		if err := store.CloseStaleAttempt(ctx, stale); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("CloseStaleAttempt() error = %v, want %v", err, statestore.ErrConflict)
		}

		claimed := newAcceptanceFixture(t, store, "stale-close-claimed", acceptanceOptions{stopAt: attemptstate.AttemptPhaseClaimed})
		claimed = supersedeAcceptanceFixture(t, store, claimed, "stale-close-claimed")
		if err := store.CloseStaleAttempt(ctx, staleClose(claimed)); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("CloseStaleAttempt(A-CLAIMED) error = %v, want %v", err, statestore.ErrConflict)
		}
		assertNothingAccepted(t, store, fx)
		assertNothingAccepted(t, store, claimed)
	})

	t.Run("a-stale-close-must-name-the-stale-reason", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "stale-close-reason", acceptanceOptions{})
		fx = supersedeAcceptanceFixture(t, store, fx, "stale-close-reason")
		stale := staleClose(fx)
		stale.NextAttempt.CloseReason = attemptstate.CloseReasonLeaseExpired
		if err := store.CloseStaleAttempt(ctx, stale); !errors.Is(err, statestore.ErrInvalidAcceptance) {
			t.Fatalf("CloseStaleAttempt() error = %v, want %v", err, statestore.ErrInvalidAcceptance)
		}
		assertNothingAccepted(t, store, fx)
	})

	t.Run("only-an-attempt-in-verification-can-be-accepted", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-claimed", acceptanceOptions{stopAt: attemptstate.AttemptPhaseClaimed})
		assertAcceptanceRejected(t, store, fx, acceptanceCommit(t, fx), statestore.ErrConflict)
	})

	t.Run("a-result-needs-the-claim-the-worker-reported", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-no-claim", acceptanceOptions{withoutClaim: true})
		assertAcceptanceRejected(t, store, fx, acceptanceCommit(t, fx), statestore.ErrConflict)
	})

	t.Run("a-claim-that-did-not-complete-cannot-be-accepted", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-failed-claim", acceptanceOptions{claimStatus: resultclaim.StatusFailed})
		assertAcceptanceRejected(t, store, fx, acceptanceCommit(t, fx), statestore.ErrConflict)
	})

	t.Run("a-result-for-another-claim-digest-conflicts", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-claim-digest", acceptanceOptions{})
		commit := acceptanceCommit(t, fx)
		commit.Result = mustAcceptedResult(t, fx, func(c *acceptedresult.Config) {
			c.ClaimDigest = contract.SHA256([]byte("another claim"))
		})
		assertAcceptanceRejected(t, store, fx, commit, statestore.ErrConflict)
	})

	t.Run("a-result-for-another-output-conflicts", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-output", acceptanceOptions{})
		commit := acceptanceCommit(t, fx)
		commit.Result = mustAcceptedResult(t, fx, func(c *acceptedresult.Config) {
			c.OutputDigest = contract.SHA256([]byte("an output the worker never claimed"))
		})
		assertAcceptanceRejected(t, store, fx, commit, statestore.ErrConflict)
	})

	t.Run("a-task-that-reached-a-terminal-phase-is-not-completed", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-terminal", acceptanceOptions{})
		cancelled := fx.task
		cancelled.Phase = taskstate.TaskPhaseTerminal
		cancelled.TerminalOutcome = taskstate.TerminalOutcomeCancelled
		cancelled.Revision++
		cancelled.UpdatedAt = cancelled.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(ctx, fx.task.ID, fx.task.Revision, cancelled); err != nil {
			t.Fatal(err)
		}
		commit := acceptanceCommit(t, fx)
		commit.ExpectedTaskRevision = cancelled.Revision
		commit.NextTask.Revision = cancelled.Revision + 1
		fx.task = cancelled
		assertAcceptanceRejected(t, store, fx, commit, statestore.ErrConflict)
	})

	t.Run("a-task-with-a-cancellation-request-is-not-completed", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-cancel-requested", acceptanceOptions{})
		requested := fx.task
		requested.CancellationRequested = true
		requested.Revision++
		requested.UpdatedAt = requested.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(ctx, fx.task.ID, fx.task.Revision, requested); err != nil {
			t.Fatal(err)
		}
		commit := acceptanceCommit(t, fx)
		commit.ExpectedTaskRevision = requested.Revision
		commit.NextTask.Revision = requested.Revision + 1
		commit.NextTask.CancellationRequested = true
		fx.task = requested
		assertAcceptanceRejected(t, store, fx, commit, statestore.ErrConflict)
	})

	t.Run("an-attempt-of-another-task-cannot-complete-this-task", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-attempt-task", acceptanceOptions{})
		other := newAcceptanceFixture(t, store, "accept-other-task", acceptanceOptions{})

		commit := acceptanceCommit(t, fx)
		commit.Result = mustAcceptedResult(t, fx, func(c *acceptedresult.Config) { c.TaskID = other.task.ID })
		commit.NextAttempt.TaskID = other.task.ID
		commit.NextTask = other.task
		commit.NextTask.Phase = taskstate.TaskPhaseTerminal
		commit.NextTask.TerminalOutcome = taskstate.TerminalOutcomeCompleted
		commit.NextTask.Revision = other.task.Revision + 1
		commit.ExpectedTaskRevision = other.task.Revision
		assertAcceptanceRejected(t, store, fx, commit, statestore.ErrConflict)
	})

	t.Run("a-malformed-commit-is-refused-before-any-state-is-read", func(t *testing.T) {
		store := newStore(t)
		fx := newAcceptanceFixture(t, store, "accept-malformed", acceptanceOptions{})
		cases := map[string]func(*statestore.ResultAcceptanceCommit){
			"attempt closed for another reason": func(c *statestore.ResultAcceptanceCommit) {
				c.NextAttempt.CloseReason = attemptstate.CloseReasonResultRejected
			},
			"attempt left open": func(c *statestore.ResultAcceptanceCommit) {
				c.NextAttempt.Phase = attemptstate.AttemptPhaseVerifying
				c.NextAttempt.CloseReason = attemptstate.CloseReasonNone
			},
			"task not completed": func(c *statestore.ResultAcceptanceCommit) {
				c.NextTask.TerminalOutcome = taskstate.TerminalOutcomeFailed
			},
			"task not terminal": func(c *statestore.ResultAcceptanceCommit) {
				c.NextTask.Phase = taskstate.TaskPhaseActive
				c.NextTask.TerminalOutcome = taskstate.TerminalOutcomeNone
			},
			"no accepted result": func(c *statestore.ResultAcceptanceCommit) {
				c.Result = acceptedresult.Record{}
			},
		}
		for name, mutate := range cases {
			commit := acceptanceCommit(t, fx)
			mutate(&commit)
			if err := store.CommitAcceptedResult(ctx, commit); !errors.Is(err, statestore.ErrInvalidAcceptance) {
				t.Fatalf("%s: error = %v, want %v", name, err, statestore.ErrInvalidAcceptance)
			}
		}
		assertNothingAccepted(t, store, fx)
	})
}

// RunResultAcceptanceDurabilityContract fixes that an accepted result, the closed
// Attempt and the completed Task survive a restart together.
func RunResultAcceptanceDurabilityContract(t *testing.T, factory DurableResultAcceptanceFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("an-accepted-result-survives-restart-with-its-attempt-and-task", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		fx := newAcceptanceFixture(t, store, "accept-restart", acceptanceOptions{})
		commit := acceptanceCommit(t, fx)
		if err := store.CommitAcceptedResult(ctx, commit); err != nil {
			t.Fatal(err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		defer func() { _ = closeReopened() }()
		loaded, ok, err := reopened.LoadAcceptedResult(ctx, fx.task.ID)
		if err != nil || !ok || !loaded.Equal(commit.Result) {
			t.Fatalf("accepted result after restart = %+v, %v, %v", loaded, ok, err)
		}
		attempt, err := reopened.LoadAttempt(ctx, fx.attempt.ID)
		if err != nil || attempt.Phase != attemptstate.AttemptPhaseClosed ||
			attempt.CloseReason != attemptstate.CloseReasonResultAccepted {
			t.Fatalf("Attempt after restart = %+v, %v", attempt, err)
		}
		task, err := reopened.LoadTask(ctx, fx.task.ID)
		if err != nil || task.Phase != taskstate.TaskPhaseTerminal ||
			task.TerminalOutcome != taskstate.TerminalOutcomeCompleted {
			t.Fatalf("Task after restart = %+v, %v", task, err)
		}
		if err := reopened.CommitAcceptedResult(ctx, commit); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("replay after restart error = %v, want %v", err, statestore.ErrAlreadyExists)
		}
	})
}

// newAcceptanceFixture takes a Task through placement and the Worker's report to
// an Attempt in verification, using only the store's public contract.
func newAcceptanceFixture(t testing.TB, store ResultAcceptanceTestStore, seed string, opts acceptanceOptions) acceptanceFixture {
	t.Helper()
	ctx := context.Background()
	if opts.stopAt == "" {
		opts.stopAt = attemptstate.AttemptPhaseVerifying
	}
	if opts.claimStatus == "" {
		opts.claimStatus = resultclaim.StatusCompleted
	}

	task := readyTask(t, store, seed)
	reserved, err := store.ReserveLease(ctx, leaseReservation(t, store, task, "placement-"+seed, leaseNow, leaseNow.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.LoadAttempt(ctx, reserved.Lease.AttemptID)
	if err != nil {
		t.Fatal(err)
	}

	step := func(phase attemptstate.AttemptPhase) {
		next := attempt
		next.Phase = phase
		next.Revision++
		next.UpdatedAt = attempt.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, next); err != nil {
			t.Fatalf("move Attempt to %s: %v", phase, err)
		}
		attempt = next
	}
	step(attemptstate.AttemptPhaseDispatched)
	step(attemptstate.AttemptPhaseClaimed)

	completed := opts.claimStatus == resultclaim.StatusCompleted
	cfg := resultclaim.ClaimConfig{
		TaskID: task.ID, TaskIRDigest: task.TaskIRDigest, AttemptID: attempt.ID,
		WorkerIdentity: attempt.WorkerIdentity, LeaseID: reserved.Lease.ID, FenceEpoch: reserved.Lease.FenceEpoch,
		WorkOrderDecision: "workorder-" + seed, ExecutorDigest: contract.SHA256([]byte("executor")),
		Nonce: []byte("0123456789abcdef"), Status: opts.claimStatus, ExitCode: 1, OutputBytes: 5,
		StartedAt: leaseNow, EndedAt: leaseNow.Add(time.Second),
	}
	if completed {
		cfg.ExitCode = 0
		cfg.OutputDigest = contract.SHA256([]byte("hello"))
	}
	claim, err := resultclaim.NewClaim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.withoutClaim {
		if err := store.ApplyResultClaim(ctx, statestore.ResultClaimAcceptance{Claim: claim, Record: true}); err != nil {
			t.Fatal(err)
		}
	}
	if opts.stopAt == attemptstate.AttemptPhaseVerifying {
		step(attemptstate.AttemptPhaseVerifying)
	}

	stored, err := store.LoadTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return acceptanceFixture{task: stored, attempt: attempt, claim: claim}
}

func mustAcceptedResult(t testing.TB, fx acceptanceFixture, mutate func(*acceptedresult.Config)) acceptedresult.Record {
	t.Helper()
	cfg := acceptedresult.Config{
		TaskID: fx.task.ID, AttemptID: fx.attempt.ID,
		ClaimDigest: fx.claim.Digest(), OutputDigest: fx.claim.OutputDigest(),
		Level: resultaccept.LevelRA1, PolicyDigest: contract.SHA256([]byte("result policy")),
		Verifiers:  []string{"verifier-b", "verifier-a"},
		AcceptedAt: fx.attempt.UpdatedAt.Add(2 * time.Second),
	}
	if cfg.OutputDigest.IsZero() {
		// A claim that did not complete has no output. The test still needs a
		// well-formed record to present to the store.
		cfg.OutputDigest = contract.SHA256([]byte("hello"))
	}
	if mutate != nil {
		mutate(&cfg)
	}
	record, err := acceptedresult.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// acceptanceCommit is the commit a correct caller would make for the fixture.
func acceptanceCommit(t testing.TB, fx acceptanceFixture) statestore.ResultAcceptanceCommit {
	t.Helper()
	nextAttempt := fx.attempt
	nextAttempt.Phase = attemptstate.AttemptPhaseClosed
	nextAttempt.CloseReason = attemptstate.CloseReasonResultAccepted
	nextAttempt.Revision++
	nextAttempt.UpdatedAt = fx.attempt.UpdatedAt.Add(2 * time.Second)

	nextTask := fx.task
	nextTask.Phase = taskstate.TaskPhaseTerminal
	nextTask.TerminalOutcome = taskstate.TerminalOutcomeCompleted
	nextTask.Revision++
	nextTask.UpdatedAt = fx.task.UpdatedAt.Add(2 * time.Second)

	return statestore.ResultAcceptanceCommit{
		Result:                  mustAcceptedResult(t, fx, nil),
		ExpectedAttemptRevision: fx.attempt.Revision,
		NextAttempt:             nextAttempt,
		ExpectedTaskRevision:    fx.task.Revision,
		NextTask:                nextTask,
	}
}

// supersedeAcceptanceFixture places the fixture's Task again on a second
// Attempt after the first Lease expired (V3), so the fixture's Attempt no longer
// holds the Task's latest fence. It returns the fixture with the Task reloaded.
func supersedeAcceptanceFixture(t testing.TB, store ResultAcceptanceTestStore, fx acceptanceFixture, seed string) acceptanceFixture {
	t.Helper()
	ctx := context.Background()
	a1Lease, err := store.LoadLease(ctx, fx.attempt.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.ReserveLease(ctx, leaseReservation(t, store, fx.task, "placement-"+seed+"-a2",
		a1Lease.ExpiresAt.Add(time.Minute), a1Lease.ExpiresAt.Add(2*time.Minute)))
	if err != nil {
		t.Fatalf("ReserveLease(A2) error = %v", err)
	}
	if reserved.Lease.FenceEpoch <= a1Lease.FenceEpoch {
		t.Fatalf("A2 fence %d does not supersede A1 fence %d", reserved.Lease.FenceEpoch, a1Lease.FenceEpoch)
	}
	if fx.task, err = store.LoadTask(ctx, fx.task.ID); err != nil {
		t.Fatal(err)
	}
	return fx
}

// staleClose is the close a correct caller would make for a superseded Attempt.
func staleClose(fx acceptanceFixture) statestore.StaleAttemptClose {
	next := fx.attempt
	next.Phase = attemptstate.AttemptPhaseClosed
	next.CloseReason = attemptstate.CloseReasonStaleAttempt
	next.Revision++
	next.UpdatedAt = fx.attempt.UpdatedAt.Add(2 * time.Second)
	return statestore.StaleAttemptClose{ExpectedAttemptRevision: fx.attempt.Revision, NextAttempt: next}
}

// assertAcceptanceRejected checks that a commit fails with want and leaves no
// trace: no accepted result, the Attempt where it was, and the Task unchanged.
func assertAcceptanceRejected(t testing.TB, store ResultAcceptanceTestStore, fx acceptanceFixture, commit statestore.ResultAcceptanceCommit, want error) {
	t.Helper()
	if err := store.CommitAcceptedResult(context.Background(), commit); !errors.Is(err, want) {
		t.Fatalf("CommitAcceptedResult() error = %v, want %v", err, want)
	}
	assertNothingAccepted(t, store, fx)
}

func assertNothingAccepted(t testing.TB, store ResultAcceptanceTestStore, fx acceptanceFixture) {
	t.Helper()
	ctx := context.Background()
	if _, ok, err := store.LoadAcceptedResult(ctx, fx.task.ID); err != nil || ok {
		t.Fatalf("an accepted result was recorded: %v, %v", ok, err)
	}
	attempt, err := store.LoadAttempt(ctx, fx.attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Phase != fx.attempt.Phase || attempt.Revision != fx.attempt.Revision || attempt.CloseReason != fx.attempt.CloseReason {
		t.Fatalf("the Attempt changed: %+v, want %+v", attempt, fx.attempt)
	}
	task, err := store.LoadTask(ctx, fx.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Phase != fx.task.Phase || task.Revision != fx.task.Revision || task.TerminalOutcome != fx.task.TerminalOutcome {
		t.Fatalf("the Task changed: %+v, want %+v", task, fx.task)
	}
}
