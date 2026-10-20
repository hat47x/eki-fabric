package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var baseTime = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)

func claim(t *testing.T, n int) resultclaim.Claim {
	t.Helper()
	attemptID, err := ids.ParseAttemptID(fmt.Sprintf("atm_%032d", n))
	if err != nil {
		t.Fatal(err)
	}
	record, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID:       ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest: contract.SHA256([]byte("taskir")), AttemptID: attemptID,
		WorkerIdentity: "worker-a", LeaseID: ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch: 7, WorkOrderDecision: "wor-001",
		ExecutorDigest: contract.SHA256([]byte("executable")),
		Nonce:          []byte("0123456789abcdef"), Status: resultclaim.StatusReclaimed,
		ExitCode: 0, OutputBytes: 10,
		StartedAt: baseTime.Add(-time.Minute), EndedAt: baseTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func entry(t *testing.T, n int, nextAt time.Time) claimqueue.Entry {
	t.Helper()
	return claimqueue.Entry{Claim: claim(t, n), QueuedAt: baseTime, NextAt: nextAt}
}

func TestSaveLoadAndDue(t *testing.T) {
	store := New()
	ctx := context.Background()
	first := entry(t, 1, baseTime.Add(time.Second))
	second := entry(t, 2, baseTime)
	for _, e := range []claimqueue.Entry{first, second} {
		if err := store.Save(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	loaded, ok, err := store.Load(ctx, first.Claim.AttemptID())
	if err != nil || !ok || !loaded.Claim.Equal(first.Claim) {
		t.Fatalf("Load() = %+v, %v, %v", loaded, ok, err)
	}
	due, err := store.Due(ctx, baseTime, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].Claim.AttemptID() != second.Claim.AttemptID() {
		t.Fatalf("due = %+v, want only the entry at baseTime", due)
	}
	due, err = store.Due(ctx, baseTime.Add(time.Second), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("Due(limit 1) = %d, %v; want 1", len(due), err)
	}
}

func TestDeleteRequiresTheSameDigest(t *testing.T) {
	store := New()
	ctx := context.Background()
	e := entry(t, 1, baseTime)
	if err := store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, e.Claim.AttemptID(), "sha256:other"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Load(ctx, e.Claim.AttemptID()); !ok {
		t.Fatal("Delete removed an entry with a different digest")
	}
	if err := store.Delete(ctx, e.Claim.AttemptID(), e.Claim.Digest().String()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Load(ctx, e.Claim.AttemptID()); ok {
		t.Fatal("Delete kept the entry")
	}
}

func TestSaveRejectsInvalidEntries(t *testing.T) {
	store := New()
	ctx := context.Background()
	cases := map[string]claimqueue.Entry{
		"zero claim": {},
		"queued at":  {Claim: claim(t, 1), NextAt: baseTime},
		"next at":    {Claim: claim(t, 2), QueuedAt: baseTime},
	}
	for name, e := range cases {
		if err := store.Save(ctx, e); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("%s: Save() error = %v, want %v", name, err, ErrInvalidEntry)
		}
	}
	if _, err := store.Due(ctx, baseTime, 0); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("zero limit error = %v, want %v", err, ErrInvalidEntry)
	}
}

func TestContextCancellation(t *testing.T) {
	store := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.Load(ctx, ids.AttemptID("atm_1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load() error = %v, want context.Canceled", err)
	}
}
