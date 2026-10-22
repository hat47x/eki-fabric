package sqlite

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var baseTime = time.Date(2026, 10, 21, 0, 0, 0, 123, time.UTC)

func claim(t testing.TB, n int, output uint64, status resultclaim.Status) resultclaim.Claim {
	t.Helper()
	exitCode := -7
	var outputDigest contract.Digest
	if status == resultclaim.StatusCompleted {
		exitCode = 0
		outputDigest = contract.SHA256([]byte("output"))
	}
	c, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		OutputDigest: outputDigest,
		TaskID:       ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest: contract.SHA256([]byte("taskir")), AttemptID: ids.AttemptID(fmt.Sprintf("atm_%032d", n)),
		WorkerIdentity: "worker-あ", LeaseID: ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch: math.MaxUint64, WorkOrderDecision: "wor-001", ExecutorDigest: contract.SHA256([]byte("executor")),
		Nonce: []byte("0123456789abcdef0123456789abcdef"), Status: status, OutputBytes: output, ExitCode: exitCode,
		StartedAt: baseTime.Add(-time.Minute), EndedAt: baseTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func entry(t testing.TB, n int) claimqueue.Entry {
	return claimqueue.Entry{Claim: claim(t, n, math.MaxUint64, resultclaim.StatusReclaimed), QueuedAt: baseTime, NextAt: baseTime}
}

func openStore(t testing.TB, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func assertEntry(t testing.TB, got, want claimqueue.Entry) {
	t.Helper()
	if !got.Claim.Equal(want.Claim) || !got.QueuedAt.Equal(want.QueuedAt) || !got.NextAt.Equal(want.NextAt) || got.Attempts != want.Attempts || got.LastError != want.LastError {
		t.Fatalf("entry did not roundtrip: got=%+v want=%+v", got, want)
	}
}

func TestAllClaimStatusesAndRetryStateSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue#1%.db")
	if runtime.GOOS != "windows" {
		path += "?"
	}
	s := openStore(t, path)
	statuses := []resultclaim.Status{resultclaim.StatusCompleted, resultclaim.StatusFailed, resultclaim.StatusReclaimed, resultclaim.StatusDeadlineExceeded, resultclaim.StatusCancelled, resultclaim.StatusOutputExceeded, resultclaim.StatusRunnerStuck}
	var saved []claimqueue.Entry
	for i, status := range statuses {
		value := entry(t, i+1)
		value.Claim = claim(t, i+1, math.MaxUint64, status)
		value.Attempts, value.LastError, value.NextAt = math.MaxUint32, "synthetic transport failure", baseTime.Add(time.Minute)
		if err := s.Save(context.Background(), value); err != nil {
			t.Fatal(err)
		}
		saved = append(saved, value)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, path)
	for _, want := range saved {
		got, found, err := s.Load(context.Background(), want.Claim.AttemptID())
		if err != nil || !found {
			t.Fatalf("Load = %v, %v", found, err)
		}
		assertEntry(t, got, want)
	}
}

func TestInitialReplayNeverResetsProgressAndStaleUpdatesAreRefused(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "progress.db"))
	initial := entry(t, 1)
	if err := s.Save(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	retry := initial
	retry.Attempts, retry.NextAt, retry.LastError = 2, baseTime.Add(time.Minute), "offline"
	if err := s.Save(context.Background(), retry); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	stale := retry
	stale.Attempts = 1
	if err := s.Save(context.Background(), stale); !errors.Is(err, claimqueue.ErrConflict) {
		t.Fatalf("stale update = %v", err)
	}
	stale = retry
	stale.NextAt = baseTime
	if err := s.Save(context.Background(), stale); !errors.Is(err, claimqueue.ErrConflict) {
		t.Fatalf("diverging update = %v", err)
	}
	got, _, err := s.Load(context.Background(), initial.Claim.AttemptID())
	if err != nil {
		t.Fatal(err)
	}
	assertEntry(t, got, retry)
}

func TestConflictingConcurrentSavesPreserveFirstClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.db")
	stores := []*Store{openStore(t, path), openStore(t, path)}
	values := []claimqueue.Entry{entry(t, 1), entry(t, 1)}
	values[1].Claim = claim(t, 1, 42, resultclaim.StatusFailed)
	var wg sync.WaitGroup
	errorsByWriter := make([]error, 2)
	for i := range stores {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errorsByWriter[i] = stores[i].Save(context.Background(), values[i]) }(i)
	}
	wg.Wait()
	winner := -1
	for i, err := range errorsByWriter {
		if err == nil {
			if winner != -1 {
				t.Fatal("both conflicting claims were saved")
			}
			winner = i
		} else if !errors.Is(err, claimqueue.ErrConflict) {
			t.Fatal(err)
		}
	}
	if winner == -1 {
		t.Fatal("neither claim was saved")
	}
	got, _, err := stores[0].Load(context.Background(), values[0].Claim.AttemptID())
	if err != nil {
		t.Fatal(err)
	}
	assertEntry(t, got, values[winner])
}

func TestDueOrderingLimitAndDigestBoundDeletion(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "due.db"))
	for _, n := range []int{3, 2, 1} {
		value := entry(t, n)
		if n == 3 {
			value.NextAt = baseTime.Add(time.Minute)
		}
		if err := s.Save(context.Background(), value); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Due(context.Background(), baseTime, 1)
	if err != nil || len(got) != 1 || got[0].Claim.AttemptID() != entry(t, 1).Claim.AttemptID() {
		t.Fatalf("limited due = %+v, %v", got, err)
	}
	got, err = s.Due(context.Background(), baseTime, 10)
	if err != nil || len(got) != 2 || got[1].Claim.AttemptID() != entry(t, 2).Claim.AttemptID() {
		t.Fatalf("ordered due = %+v, %v", got, err)
	}
	first := entry(t, 1)
	if err := s.Delete(context.Background(), first.Claim.AttemptID(), "wrong digest"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.Load(context.Background(), first.Claim.AttemptID()); err != nil || !found {
		t.Fatal("wrong digest deleted a claim")
	}
	if err := s.Delete(context.Background(), first.Claim.AttemptID(), first.Claim.Digest().String()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.Load(context.Background(), first.Claim.AttemptID()); err != nil || found {
		t.Fatalf("deleted claim found=%v err=%v", found, err)
	}
}

func TestFailedSaveAndCorruptReadsFailClosed(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "failure.db"))
	if _, err := s.db.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON pending_claims BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	value := entry(t, 1)
	if err := s.Save(context.Background(), value); err == nil {
		t.Fatal("failed insert reported success")
	}
	if _, found, err := s.Load(context.Background(), value.Claim.AttemptID()); found || err != nil {
		t.Fatalf("failed insert persisted: %v, %v", found, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_insert`); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE pending_claims SET claim_digest='corrupt'`); err == nil {
		t.Fatal("immutable claim update was accepted")
	}
	if _, err := s.db.Exec(`DROP TRIGGER pending_claim_immutable; UPDATE pending_claims SET claim_digest='corrupt'`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.Load(context.Background(), value.Claim.AttemptID()); found || !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("corrupt Load = %v, %v", found, err)
	}
	if got, err := s.Due(context.Background(), baseTime, 10); got != nil || !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("corrupt Due = %+v, %v", got, err)
	}
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type senderFunc func(context.Context, resultclaim.Claim) error

func (f senderFunc) Send(ctx context.Context, claim resultclaim.Claim) error { return f(ctx, claim) }

func TestQueueRestartsAfterLostAcknowledgementWithoutLosingClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue-restart.db")
	clock := &fakeClock{now: baseTime}
	want := entry(t, 1).Claim
	calls := 0
	sender := senderFunc(func(_ context.Context, got resultclaim.Claim) error {
		calls++
		if !got.Equal(want) {
			t.Fatal("delivery changed claim content")
		}
		if calls == 1 {
			return errors.New("synthetic acknowledgement lost after receipt")
		}
		return nil
	})
	newQueue := func(store *Store) *claimqueue.Queue {
		q, err := claimqueue.New(claimqueue.Config{Store: store, Sender: sender, Clock: clock, BaseDelay: time.Second, MaxDelay: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	s := openStore(t, path)
	q := newQueue(s)
	if _, err := q.Enqueue(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if report, err := q.DeliverDue(context.Background(), 10); err != nil || report.Retried != 1 {
		t.Fatalf("first delivery = %+v, %v", report, err)
	}
	before, _, err := s.Load(context.Background(), want.AttemptID())
	if err != nil || before.Attempts != 1 || !before.NextAt.After(baseTime) {
		t.Fatalf("retry state = %+v, %v", before, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, path)
	q = newQueue(s)
	if already, err := q.Enqueue(context.Background(), want); err != nil || !already {
		t.Fatalf("restart enqueue = %v, %v", already, err)
	}
	after, _, err := s.Load(context.Background(), want.AttemptID())
	if err != nil {
		t.Fatal(err)
	}
	assertEntry(t, after, before)
	if report, err := q.DeliverDue(context.Background(), 10); err != nil || report.Delivered != 0 || calls != 1 {
		t.Fatalf("premature retry = %+v, %v", report, err)
	}
	clock.now = before.NextAt
	if report, err := q.DeliverDue(context.Background(), 10); err != nil || report.Delivered != 1 || calls != 2 {
		t.Fatalf("restart retry = %+v, %v", report, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, path)
	if _, found, err := s.Load(context.Background(), want.AttemptID()); err != nil || found {
		t.Fatalf("acknowledged claim resurrected: %v, %v", found, err)
	}
}

func TestRetryCounterNeverWrapsToInitialEnqueue(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "overflow.db"))
	value := entry(t, 1)
	value.Attempts = math.MaxUint32
	if err := s.Save(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	q, err := claimqueue.New(claimqueue.Config{Store: s, Sender: senderFunc(func(context.Context, resultclaim.Claim) error { return errors.New("offline") }), Clock: &fakeClock{baseTime}, BaseDelay: time.Second, MaxDelay: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if report, err := q.DeliverDue(context.Background(), 1); !errors.Is(err, claimqueue.ErrInvalidEntry) || report.Retried != 0 {
		t.Fatalf("exhausted counter = %+v, %v", report, err)
	}
	got, _, err := s.Load(context.Background(), value.Claim.AttemptID())
	if err != nil {
		t.Fatal(err)
	}
	assertEntry(t, got, value)
}

func TestInvalidRequestsAndUnavailableStoreDoNotSucceed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.db")
	s := openStore(t, path)
	valid := entry(t, 1)
	for _, value := range []claimqueue.Entry{{}, {Claim: valid.Claim, QueuedAt: baseTime}, {Claim: valid.Claim, QueuedAt: time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC), NextAt: baseTime}} {
		if err := s.Save(context.Background(), value); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("invalid entry = %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, bad := range []context.Context{nil, ctx} {
		if err := s.Save(bad, valid); err == nil {
			t.Fatal("invalid context saved a claim")
		}
		if _, found, err := s.Load(bad, valid.Claim.AttemptID()); found || err == nil {
			t.Fatalf("invalid Load context = %v, %v", found, err)
		}
		if _, err := s.Due(bad, baseTime, 1); err == nil {
			t.Fatal("invalid Due context succeeded")
		}
		if err := s.Delete(bad, valid.Claim.AttemptID(), valid.Claim.Digest().String()); err == nil {
			t.Fatal("invalid Delete context succeeded")
		}
	}
	if _, err := s.Due(context.Background(), baseTime, 0); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("invalid limit = %v", err)
	}
	if _, err := s.db.Exec(`UPDATE claim_queue_meta SET schema_version=2`); err != nil {
		t.Fatal(err)
	}
	if next, err := Open(context.Background(), path); next != nil || !errors.Is(err, claimqueue.ErrInvalidConfig) {
		t.Fatalf("unknown schema = %v, %v", next, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), valid); err == nil {
		t.Fatal("closed database saved a claim")
	}
}
