package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
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
// even a writer that bypasses the adapter.
func TestAcceptedResultRowsAreImmutable(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
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
// Task never has two accepted results.
func TestAcceptedResultTableRefusesValuesNoDecisionCouldProduce(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
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
	task.Phase = taskstate.TaskPhaseReady
	if err := old.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
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
