package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestResultAcceptanceContract(t *testing.T) {
	statestoretest.RunResultAcceptanceContract(t, func(t testing.TB) statestoretest.ResultAcceptanceTestStore {
		t.Helper()
		store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		})
		return store
	})
}

func TestResultAcceptanceDurabilityContract(t *testing.T) {
	statestoretest.RunResultAcceptanceDurabilityContract(t, statestoretest.DurableResultAcceptanceFactory{
		Open: func(t testing.TB, location string) (statestoretest.ResultAcceptanceTestStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

const insertAcceptedResult = `
INSERT INTO accepted_results (task_id, attempt_id, claim_digest, output_digest, assurance_level, policy_digest, verifiers, accepted_at_ns)
VALUES (?, ?, ?, ?, ?, ?, '["verifier-a"]', 1000)`

// An accepted result is evidence of a decision. Nothing may rewrite or remove it,
// even a writer that bypasses the adapter. The v12 schema is used so that rows
// can be written without the Task, Attempt, and Lease the v13 fence guard needs.
func TestAcceptedResultRowsAreImmutable(t *testing.T) {
	store := openStoreWithMigrations(t, filepath.Join(t.TempDir(), "state.db"), migrations[:12])
	defer store.Close()
	digest := contract.SHA256([]byte("value")).Hex()
	if _, err := store.db.Exec(insertAcceptedResult, "tsk_1", "atm_1", digest, digest, "RA-1", digest); err != nil {
		t.Fatalf("insert accepted result: %v", err)
	}
	for name, statement := range map[string]string{
		"update": `UPDATE accepted_results SET assurance_level = 'RA-2' WHERE task_id = 'tsk_1'`,
		"delete": `DELETE FROM accepted_results WHERE task_id = 'tsk_1'`,
	} {
		if _, err := store.db.ExecContext(context.Background(), statement); err == nil ||
			!strings.Contains(err.Error(), "AcceptedResult is immutable") {
			t.Fatalf("%s error = %v, want an immutability failure", name, err)
		}
	}
}

// The table refuses values that could not have come from a real decision, and a
// Task never has two accepted results. The v12 schema is used for the same
// reason as above.
func TestAcceptedResultTableRefusesValuesNoDecisionCouldProduce(t *testing.T) {
	store := openStoreWithMigrations(t, filepath.Join(t.TempDir(), "state.db"), migrations[:12])
	defer store.Close()
	digest := contract.SHA256([]byte("value")).Hex()
	cases := map[string]struct {
		level, claim string
	}{
		"level RA-0":       {level: "RA-0", claim: digest},
		"level RA-3":       {level: "RA-3", claim: digest},
		"short claim hash": {level: "RA-1", claim: "abc"},
	}
	for name, tc := range cases {
		_, err := store.db.Exec(insertAcceptedResult, "tsk_"+name, "atm_"+name, tc.claim, digest, tc.level, digest)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("%s: error = %v, want a CHECK constraint failure", name, err)
		}
	}
	if _, err := store.db.Exec(insertAcceptedResult, "tsk_dup", "atm_dup1", digest, digest, "RA-1", digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(insertAcceptedResult, "tsk_dup", "atm_dup2", digest, digest, "RA-1", digest); err == nil {
		t.Fatal("a second accepted result for one Task was stored")
	}
	if _, err := store.db.Exec(insertAcceptedResult, "tsk_other", "atm_dup1", digest, digest, "RA-1", digest); err == nil {
		t.Fatal("one Attempt was accepted for two Tasks")
	}
}

// A database written before accepted results existed keeps its Tasks and gains
// the table, so acceptance can start on an upgraded store.
func TestVersion9DatabaseGainsAcceptedResultsWithoutLosingTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := openWithMigrations(context.Background(), path, migrations[:9])
	if err != nil {
		t.Fatalf("open v9 store: %v", err)
	}
	if _, err := old.db.Exec(`SELECT 1 FROM accepted_results`); err == nil {
		t.Fatal("the v9 schema already has accepted_results")
	}
	task := newTaskRecord(t, "v9-accepted-results")
	if err := old.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	// A Task is created admitted; ready is a separate write (#365).
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.CreatedAt.Add(time.Second)
	if err := old.CompareAndSwapTask(context.Background(), task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}
	task = ready
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded := openStore(t, path)
	defer upgraded.Close()
	var version int
	if err := upgraded.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != len(migrations) {
		t.Fatalf("user_version = %d, %v, want %d", version, err, len(migrations))
	}
	got, err := upgraded.LoadTask(context.Background(), task.ID)
	if err != nil || got.ID != task.ID || got.Phase != task.Phase {
		t.Fatalf("Task after upgrade = %+v, %v", got, err)
	}
	if _, ok, err := upgraded.LoadAcceptedResult(context.Background(), task.ID); err != nil || ok {
		t.Fatalf("LoadAcceptedResult() = %v, %v, want none", ok, err)
	}
}

// A writer that bypasses the adapter cannot store an AcceptedResult for an
// Attempt whose Lease fence was superseded by a later placement of the Task
// (V3). The same row is accepted by the guard while the Attempt still holds
// the latest fence, so the guard refuses staleness, not every insert.
func TestAcceptedResultFenceGuardRefusesSupersededAttempt(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	policy, err := policystate.NewActive(1, "policy-42", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(ctx, 0, policy); err != nil {
		t.Fatal(err)
	}
	task := newTaskRecord(t, "fence-guard")
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	if err := store.CompareAndSwapTask(ctx, task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}

	place := func(number uint64, decision string, issued time.Time) (attemptstate.AttemptRecord, leasestate.LeaseRecord) {
		t.Helper()
		attemptID, err := ids.NewAttemptID()
		if err != nil {
			t.Fatal(err)
		}
		leaseID, err := ids.NewLeaseID()
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := attemptstate.New(attemptID, ready.ID, ready.TaskIRDigest, number, ready.UpdatedAt)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateAttempt(ctx, attempt); err != nil {
			t.Fatal(err)
		}
		placing := attempt
		placing.Phase = attemptstate.AttemptPhasePlacing
		placing.WorkerIdentity = "worker-a"
		placing.Revision++
		if err := store.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, placing); err != nil {
			t.Fatal(err)
		}
		expires := issued.Add(time.Minute)
		reserved, err := store.ReserveLease(ctx, statestore.LeaseReservation{
			LeaseID: leaseID, TaskID: ready.ID, TaskIRDigest: ready.TaskIRDigest,
			ExpectedTaskRevision: ready.Revision, AttemptID: attemptID, ExpectedAttemptRevision: placing.Revision,
			WorkerIdentity: "worker-a", PlacementTrustDomain: "local.example",
			PlacementAuthorityPrincipal: "control-plane/plc-1", PlacementDecisionID: decision,
			PlacementPayloadDigest: contract.SHA256([]byte(decision)), PlacementExpiresAt: expires.Add(time.Minute),
			PolicyRevision: "policy-42", ExpectedPolicyEpoch: 1, IssuedAt: issued, ExpiresAt: expires,
		})
		if err != nil {
			t.Fatal(err)
		}
		assigned, err := store.LoadAttempt(ctx, attemptID)
		if err != nil {
			t.Fatal(err)
		}
		return assigned, reserved.Lease
	}

	a1, lease1 := place(1, "placement-a1", now)
	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: ready.ID, TaskIRDigest: ready.TaskIRDigest, AttemptID: a1.ID,
		WorkerIdentity: "worker-a", LeaseID: lease1.ID, FenceEpoch: lease1.FenceEpoch,
		WorkOrderDecision: "workorder-a1", ExecutorDigest: contract.SHA256([]byte("executor")),
		Nonce: []byte("0123456789abcdef"), Status: resultclaim.StatusCompleted,
		OutputDigest: contract.SHA256([]byte("hello")), OutputBytes: 5,
		StartedAt: now, EndedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyResultClaim(ctx, statestore.ResultClaimAcceptance{Claim: claim, Record: true}); err != nil {
		t.Fatal(err)
	}

	insert := func() error {
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		_, err = tx.Exec(insertAcceptedResult, ready.ID.String(), a1.ID.String(),
			claim.Digest().Hex(), claim.OutputDigest().Hex(), "RA-1", contract.SHA256([]byte("policy")).Hex())
		return err
	}
	if err := insert(); err != nil {
		t.Fatalf("insert while A1 holds the latest fence: %v", err)
	}

	if _, lease2 := place(2, "placement-a2", lease1.ExpiresAt.Add(time.Minute)); lease2.FenceEpoch <= lease1.FenceEpoch {
		t.Fatalf("A2 fence %d does not supersede A1 fence %d", lease2.FenceEpoch, lease1.FenceEpoch)
	}
	if err := insert(); err == nil || !strings.Contains(err.Error(), "latest fence") {
		t.Fatalf("insert for superseded A1 error = %v, want the fence guard", err)
	}
}
