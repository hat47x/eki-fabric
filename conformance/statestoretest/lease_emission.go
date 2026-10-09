package statestoretest

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type LeaseEmissionTestStore interface {
	TaskLeaseStore
	statestore.LeaseEmissionStore
}

type LeaseEmissionFactory func(t testing.TB) LeaseEmissionTestStore

type DurableLeaseEmissionFactory struct {
	Open func(t testing.TB, location string) (store LeaseEmissionTestStore, close func() error)
}

func RunLeaseEmissionContract(t *testing.T, newStore LeaseEmissionFactory) {
	t.Helper()

	t.Run("commit-load-exact-replay", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "emit")
		reservation := leaseReservation(t, store, task, "placement-emit", leaseNow, leaseNow.Add(time.Minute))
		reserved, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatal(err)
		}
		commit := emissionCommit(t, store, reserved.Lease, reserved.Lease.IssuedAt.Add(time.Second))

		first, err := store.CommitLeaseEmission(context.Background(), commit)
		if err != nil {
			t.Fatalf("CommitLeaseEmission() error = %v", err)
		}
		if first.Replayed || first.Record.Status() != leaseemission.StatusEmitted {
			t.Fatalf("first emission = %#v", first)
		}
		if !bytes.Equal(first.Record.SignedEnvelope(), commit.SignedEnvelope) ||
			first.Record.EnvelopeDigest() != commit.EnvelopeDigest ||
			first.Record.PayloadDigest() != commit.PayloadDigest {
			t.Fatalf("emission lost exact signed bytes: %#v", first.Record)
		}
		loaded, err := store.LoadLeaseEmission(context.Background(), reserved.Lease.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertEmissionEqual(t, loaded, first.Record)

		again, err := store.CommitLeaseEmission(context.Background(), commit)
		if err != nil {
			t.Fatalf("exact emission replay error = %v", err)
		}
		if !again.Replayed {
			t.Fatal("exact emission replay was not marked replayed")
		}
		assertEmissionEqual(t, again.Record, first.Record)
	})

	t.Run("already-emitted-bytes-replay-after-policy-revoke", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "emit-replay-policy")
		reservation := leaseReservation(t, store, task, "placement-emit", leaseNow, leaseNow.Add(time.Minute))
		reserved, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatal(err)
		}
		commit := emissionCommit(t, store, reserved.Lease, reserved.Lease.IssuedAt.Add(time.Second))
		first, err := store.CommitLeaseEmission(context.Background(), commit)
		if err != nil {
			t.Fatal(err)
		}

		current, err := store.LoadCurrentPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		revoked, err := policystate.Revoke(current, current.Epoch+1, commit.CommittedAt.Add(time.Second), "revoke after emission")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), current.Epoch, revoked); err != nil {
			t.Fatal(err)
		}

		replay, err := store.CommitLeaseEmission(context.Background(), commit)
		if err != nil {
			t.Fatalf("exact already-emitted replay after revoke error = %v", err)
		}
		if !replay.Replayed {
			t.Fatal("already-emitted Authority was not replayed exactly")
		}
		assertEmissionEqual(t, replay.Record, first.Record)
	})

	t.Run("decision-id-cannot-be-reused-for-another-lease", func(t *testing.T) {
		store := newStore(t)

		taskA := readyTask(t, store, "emit-decision-a")
		reservationA := leaseReservation(t, store, taskA, "placement-decision-a", leaseNow, leaseNow.Add(time.Minute))
		leaseA, err := store.ReserveLease(context.Background(), reservationA)
		if err != nil {
			t.Fatal(err)
		}
		commitA := emissionCommit(t, store, leaseA.Lease, leaseA.Lease.IssuedAt.Add(time.Second))
		commitA.DecisionID = "shared-lease-decision"
		if _, err := store.CommitLeaseEmission(context.Background(), commitA); err != nil {
			t.Fatal(err)
		}

		taskB := readyTask(t, store, "emit-decision-b")
		reservationB := leaseReservation(t, store, taskB, "placement-decision-b", leaseNow, leaseNow.Add(time.Minute))
		leaseB, err := store.ReserveLease(context.Background(), reservationB)
		if err != nil {
			t.Fatal(err)
		}
		commitB := emissionCommit(t, store, leaseB.Lease, leaseB.Lease.IssuedAt.Add(time.Second))
		commitB.DecisionID = commitA.DecisionID
		if _, err := store.CommitLeaseEmission(context.Background(), commitB); !errors.Is(err, statestore.ErrLeaseEmissionConflict) {
			t.Fatalf("reused Decision ID error = %v, want ErrLeaseEmissionConflict", err)
		}
		if _, err := store.LoadLeaseEmission(context.Background(), leaseB.Lease.ID); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("Decision ID collision emitted second Lease: %v", err)
		}
	})

	t.Run("policy-drift-stops-new-emission", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "emit-policy-drift")
		reservation := leaseReservation(t, store, task, "placement-policy", leaseNow, leaseNow.Add(time.Minute))
		reserved, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatal(err)
		}
		commit := emissionCommit(t, store, reserved.Lease, reserved.Lease.IssuedAt.Add(time.Second))

		current, err := store.LoadCurrentPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		revoked, err := policystate.Revoke(current, current.Epoch+1, commit.CommittedAt, "revoke before emission")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), current.Epoch, revoked); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitLeaseEmission(context.Background(), commit); !errors.Is(err, statestore.ErrPolicyChanged) {
			t.Fatalf("CommitLeaseEmission() error = %v, want ErrPolicyChanged", err)
		}
		if _, err := store.LoadLeaseEmission(context.Background(), reserved.Lease.ID); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("failed emission became durable: %v", err)
		}
	})

	t.Run("task-or-attempt-drift-stops-new-emission", func(t *testing.T) {
		t.Run("task", func(t *testing.T) {
			store := newStore(t)
			task := readyTask(t, store, "emit-task-drift")
			reservation := leaseReservation(t, store, task, "placement-task", leaseNow, leaseNow.Add(time.Minute))
			reserved, err := store.ReserveLease(context.Background(), reservation)
			if err != nil {
				t.Fatal(err)
			}
			commit := emissionCommit(t, store, reserved.Lease, reserved.Lease.IssuedAt.Add(time.Second))
			current, err := store.LoadTask(context.Background(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			next := current
			next.Phase = taskstate.TaskPhaseActive
			next.Revision++
			next.UpdatedAt = current.UpdatedAt.Add(time.Second)
			if err := store.CompareAndSwapTask(context.Background(), current.ID, current.Revision, next); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CommitLeaseEmission(context.Background(), commit); !errors.Is(err, statestore.ErrConflict) {
				t.Fatalf("Task drift emission error = %v, want ErrConflict", err)
			}
		})

		t.Run("attempt", func(t *testing.T) {
			store := newStore(t)
			task := readyTask(t, store, "emit-attempt-drift")
			reservation := leaseReservation(t, store, task, "placement-attempt", leaseNow, leaseNow.Add(time.Minute))
			reserved, err := store.ReserveLease(context.Background(), reservation)
			if err != nil {
				t.Fatal(err)
			}
			commit := emissionCommit(t, store, reserved.Lease, reserved.Lease.IssuedAt.Add(time.Second))
			current, err := store.LoadAttempt(context.Background(), reserved.Lease.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			next := current
			next.Phase = attemptstate.AttemptPhaseDispatched
			next.Revision++
			next.UpdatedAt = current.UpdatedAt.Add(time.Second)
			if err := store.CompareAndSwapAttempt(context.Background(), current.ID, current.Revision, next); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CommitLeaseEmission(context.Background(), commit); !errors.Is(err, statestore.ErrConflict) {
				t.Fatalf("Attempt drift emission error = %v, want ErrConflict", err)
			}
		})
	})

	t.Run("abort-is-durable-releases-exclusion-and-keeps-fence-monotonic", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "emit-abort")
		firstReq := leaseReservation(t, store, task, "placement-abort", leaseNow, leaseNow.Add(time.Minute))
		first, err := store.ReserveLease(context.Background(), firstReq)
		if err != nil {
			t.Fatal(err)
		}
		assigned, err := store.LoadAttempt(context.Background(), first.Lease.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		abort := statestore.LeaseEmissionAbort{
			LeaseID: first.Lease.ID, ExpectedAttemptRevision: assigned.Revision,
			CloseReason: attemptstate.CloseReasonPolicyRevoked,
			Reason: "signing premise changed before durable emission",
			CommittedAt: first.Lease.IssuedAt.Add(time.Second),
		}
		aborted, err := store.AbortLeaseEmission(context.Background(), abort)
		if err != nil {
			t.Fatalf("AbortLeaseEmission() error = %v", err)
		}
		if aborted.Replayed || aborted.Record.Status() != leaseemission.StatusAborted {
			t.Fatalf("abort result = %#v", aborted)
		}
		closed, err := store.LoadAttempt(context.Background(), first.Lease.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		if closed.Phase != attemptstate.AttemptPhaseClosed ||
			closed.CloseReason != attemptstate.CloseReasonPolicyRevoked {
			t.Fatalf("abort did not close Attempt atomically: %#v", closed)
		}

		replay, err := store.AbortLeaseEmission(context.Background(), abort)
		if err != nil || !replay.Replayed {
			t.Fatalf("exact abort replay = %#v err=%v", replay, err)
		}
		conflict := abort
		conflict.CloseReason = attemptstate.CloseReasonSuperseded
		if _, err := store.AbortLeaseEmission(context.Background(), conflict); !errors.Is(err, statestore.ErrLeaseEmissionConflict) {
			t.Fatalf("conflicting abort replay error = %v", err)
		}

		oldReplay := firstReq
		oldReplay.LeaseID = mustLeaseID(t)
		if _, err := store.ReserveLease(context.Background(), oldReplay); !errors.Is(err, statestore.ErrConflict) {
			t.Fatalf("old Placement recreated Lease after abort: %v", err)
		}

		secondNow := abort.CommittedAt.Add(time.Nanosecond)
		secondReq := leaseReservation(t, store, task, "placement-after-abort", secondNow, secondNow.Add(time.Minute))
		second, err := store.ReserveLease(context.Background(), secondReq)
		if err != nil {
			t.Fatalf("new Placement after abort error = %v", err)
		}
		if second.Lease.FenceEpoch != first.Lease.FenceEpoch+1 {
			t.Fatalf("FenceEpoch rolled back after abort: first=%d second=%d", first.Lease.FenceEpoch, second.Lease.FenceEpoch)
		}
	})

	t.Run("concurrent-emit-vs-abort-has-one-linearization-winner", func(t *testing.T) {
		store := newStore(t)
		task := readyTask(t, store, "emit-abort-race")
		reservation := leaseReservation(t, store, task, "placement-race", leaseNow, leaseNow.Add(time.Minute))
		reserved, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatal(err)
		}
		commit := emissionCommit(t, store, reserved.Lease, reserved.Lease.IssuedAt.Add(time.Second))
		attempt, err := store.LoadAttempt(context.Background(), reserved.Lease.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		abort := statestore.LeaseEmissionAbort{
			LeaseID: reserved.Lease.ID,
			ExpectedAttemptRevision: attempt.Revision,
			CloseReason: attemptstate.CloseReasonSuperseded,
			Reason: "concurrent authority premise loss",
			CommittedAt: commit.CommittedAt,
		}

		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.CommitLeaseEmission(context.Background(), commit)
			results <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := store.AbortLeaseEmission(context.Background(), abort)
			results <- err
		}()
		close(start)
		wg.Wait()
		close(results)

		var success, conflict int
		for err := range results {
			switch {
			case err == nil:
				success++
			case errors.Is(err, statestore.ErrLeaseEmissionConflict),
				errors.Is(err, statestore.ErrLeaseEmissionAborted),
				errors.Is(err, statestore.ErrConflict):
				conflict++
			default:
				t.Fatalf("unexpected concurrent emit/abort error = %v", err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("emit/abort race success=%d conflict=%d, want 1/1", success, conflict)
		}
		record, err := store.LoadLeaseEmission(context.Background(), reserved.Lease.ID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status() != leaseemission.StatusEmitted && record.Status() != leaseemission.StatusAborted {
			t.Fatalf("race left invalid emission status %q", record.Status())
		}
	})

	t.Run("emitted-and-aborted-are-mutually-exclusive", func(t *testing.T) {
		t.Run("cannot-abort-emitted", func(t *testing.T) {
			store := newStore(t)
			task := readyTask(t, store, "emit-no-abort")
			reservation := leaseReservation(t, store, task, "placement-e", leaseNow, leaseNow.Add(time.Minute))
			reserved, err := store.ReserveLease(context.Background(), reservation)
			if err != nil {
				t.Fatal(err)
			}
			commit := emissionCommit(t, store, reserved.Lease, reserved.Lease.IssuedAt.Add(time.Second))
			if _, err := store.CommitLeaseEmission(context.Background(), commit); err != nil {
				t.Fatal(err)
			}
			attempt, err := store.LoadAttempt(context.Background(), reserved.Lease.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			abort := statestore.LeaseEmissionAbort{
				LeaseID: reserved.Lease.ID, ExpectedAttemptRevision: attempt.Revision,
				CloseReason: attemptstate.CloseReasonSuperseded, Reason: "too late",
				CommittedAt: commit.CommittedAt.Add(time.Second),
			}
			if _, err := store.AbortLeaseEmission(context.Background(), abort); !errors.Is(err, statestore.ErrLeaseEmissionConflict) {
				t.Fatalf("abort after emission error = %v", err)
			}
		})

		t.Run("cannot-emit-aborted", func(t *testing.T) {
			store := newStore(t)
			task := readyTask(t, store, "abort-no-emit")
			reservation := leaseReservation(t, store, task, "placement-a", leaseNow, leaseNow.Add(time.Minute))
			reserved, err := store.ReserveLease(context.Background(), reservation)
			if err != nil {
				t.Fatal(err)
			}
			commit := emissionCommit(t, store, reserved.Lease, reserved.Lease.IssuedAt.Add(time.Second))
			attempt, err := store.LoadAttempt(context.Background(), reserved.Lease.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.AbortLeaseEmission(context.Background(), statestore.LeaseEmissionAbort{
				LeaseID: reserved.Lease.ID, ExpectedAttemptRevision: attempt.Revision,
				CloseReason: attemptstate.CloseReasonSuperseded, Reason: "abort before emission",
				CommittedAt: commit.CommittedAt,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CommitLeaseEmission(context.Background(), commit); !errors.Is(err, statestore.ErrLeaseEmissionAborted) {
				t.Fatalf("emission after abort error = %v", err)
			}
		})
	})
}

func RunLeaseEmissionDurabilityContract(t *testing.T, factory DurableLeaseEmissionFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("DurableLeaseEmissionFactory.Open is nil")
	}

	t.Run("emitted-bytes-survive-restart", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		task := readyTask(t, store, "emit-durable")
		reservation := leaseReservation(t, store, task, "placement-emit", leaseNow, leaseNow.Add(time.Minute))
		reserved, err := store.ReserveLease(context.Background(), reservation)
		if err != nil {
			t.Fatal(err)
		}
		commit := emissionCommit(t, store, reserved.Lease, reserved.Lease.IssuedAt.Add(time.Second))
		emitted, err := store.CommitLeaseEmission(context.Background(), commit)
		if err != nil {
			t.Fatal(err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		defer closeReopened()
		got, err := reopened.LoadLeaseEmission(context.Background(), reserved.Lease.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertEmissionEqual(t, got, emitted.Record)
	})

	t.Run("abort-survives-restart-and-next-fence-advances", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		task := readyTask(t, store, "abort-durable")
		firstReq := leaseReservation(t, store, task, "placement-abort", leaseNow, leaseNow.Add(time.Minute))
		first, err := store.ReserveLease(context.Background(), firstReq)
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := store.LoadAttempt(context.Background(), first.Lease.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		abortTime := first.Lease.IssuedAt.Add(time.Second)
		if _, err := store.AbortLeaseEmission(context.Background(), statestore.LeaseEmissionAbort{
			LeaseID: first.Lease.ID, ExpectedAttemptRevision: attempt.Revision,
			CloseReason: attemptstate.CloseReasonSuperseded, Reason: "durable abort",
			CommittedAt: abortTime,
		}); err != nil {
			t.Fatal(err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		defer closeReopened()
		got, err := reopened.LoadLeaseEmission(context.Background(), first.Lease.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status() != leaseemission.StatusAborted {
			t.Fatalf("status after restart = %q", got.Status())
		}
		secondNow := abortTime.Add(time.Nanosecond)
		secondReq := leaseReservation(t, reopened, task, "placement-after-abort", secondNow, secondNow.Add(time.Minute))
		second, err := reopened.ReserveLease(context.Background(), secondReq)
		if err != nil {
			t.Fatal(err)
		}
		if second.Lease.FenceEpoch != first.Lease.FenceEpoch+1 {
			t.Fatalf("FenceEpoch after restart/abort = %d, want %d", second.Lease.FenceEpoch, first.Lease.FenceEpoch+1)
		}
	})
}

func emissionCommit(t testing.TB, store LeaseEmissionTestStore, lease leasestate.LeaseRecord, at time.Time) statestore.LeaseEmissionCommit {
	t.Helper()
	task, err := store.LoadTask(context.Background(), lease.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.LoadAttempt(context.Background(), lease.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	signed := []byte("signed-lease:" + lease.ID.String())
	return statestore.LeaseEmissionCommit{
		LeaseID: lease.ID,
		ExpectedTaskRevision: task.Revision,
		ExpectedAttemptRevision: attempt.Revision,
		ExpectedPolicyEpoch: lease.PolicyEpoch,
		DecisionID: "lease-decision-" + lease.ID.String(),
		TrustDomain: "local.example",
		AuthorityPrincipal: "control-plane/lse-1",
		KeyID: decisiontrust.KeyID("kid-lse-1"),
		Algorithm: decisiontrust.AlgorithmEdDSA,
		PayloadDigest: contract.SHA256([]byte("lease-payload:" + lease.ID.String())),
		EnvelopeDigest: contract.SHA256(signed),
		SignedEnvelope: signed,
		CommittedAt: at,
	}
}

func assertEmissionEqual(t testing.TB, got, want leaseemission.Record) {
	t.Helper()
	if got.LeaseID() != want.LeaseID() ||
		got.Status() != want.Status() ||
		got.DecisionID() != want.DecisionID() ||
		got.TrustDomain() != want.TrustDomain() ||
		got.AuthorityPrincipal() != want.AuthorityPrincipal() ||
		got.KeyID() != want.KeyID() ||
		got.Algorithm() != want.Algorithm() ||
		got.PayloadDigest() != want.PayloadDigest() ||
		got.EnvelopeDigest() != want.EnvelopeDigest() ||
		got.PolicyRevision() != want.PolicyRevision() ||
		got.PolicyEpoch() != want.PolicyEpoch() ||
		got.CloseReason() != want.CloseReason() ||
		got.Reason() != want.Reason() ||
		!got.CommittedAt().Equal(want.CommittedAt()) ||
		!bytes.Equal(got.SignedEnvelope(), want.SignedEnvelope()) {
		t.Fatalf("Lease emission mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
