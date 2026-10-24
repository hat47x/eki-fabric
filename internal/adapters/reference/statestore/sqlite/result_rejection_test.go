package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestResultRejectionContract(t *testing.T) {
	statestoretest.RunResultRejectionContract(t, func(t testing.TB) statestoretest.ResultRejectionTestStore {
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

func TestResultRejectionDurabilityContract(t *testing.T) {
	statestoretest.RunResultRejectionDurabilityContract(t, statestoretest.DurableResultRejectionFactory{
		Open: func(t testing.TB, location string) (statestoretest.ResultRejectionTestStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

// A writer that bypasses the adapter cannot store a rejection for an Attempt
// that does not exist or is not verifying, and cannot rewrite or remove one.
func TestRejectedResultRowsAreGuardedAndImmutable(t *testing.T) {
	ctx := context.Background()
	digest := contract.SHA256([]byte("value")).Hex()
	const insert = `
INSERT INTO rejected_results (attempt_id, task_id, claim_digest, output_digest, policy_digest,
	reason, dissent, verifiers, excluded, rejected_at_ns)
VALUES ('atm_1', 'tsk_1', ?, ?, ?, 'authority-rejected', '[{"verifier":"a","check":4,"observed":"x"}]', '[]', '[]', 1000)`

	guarded := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer guarded.Close()
	if _, err := guarded.db.ExecContext(ctx, insert, digest, digest, digest); err == nil ||
		!strings.Contains(err.Error(), "verifying Attempt") {
		t.Fatalf("insert without a verifying Attempt error = %v, want the guard", err)
	}

	// Immutability and the CHECK constraints are checked in a scratch store
	// whose insert guard is dropped, so a row can be written without first
	// building a Task, a Lease, and a verifying Attempt.
	scratch := openStore(t, filepath.Join(t.TempDir(), "scratch.db"))
	defer scratch.Close()
	if _, err := scratch.db.ExecContext(ctx, `DROP TRIGGER rejected_results_attempt_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := scratch.db.ExecContext(ctx, insert, digest, digest, digest); err != nil {
		t.Fatalf("insert into scratch store: %v", err)
	}
	for name, statement := range map[string]string{
		"update": `UPDATE rejected_results SET reason = 'authority-rejected' WHERE attempt_id = 'atm_1'`,
		"delete": `DELETE FROM rejected_results WHERE attempt_id = 'atm_1'`,
	} {
		if _, err := scratch.db.ExecContext(ctx, statement); err == nil ||
			!strings.Contains(err.Error(), "rejected result is immutable") {
			t.Fatalf("%s error = %v, want an immutability failure", name, err)
		}
	}
	for name, dissent := range map[string]string{"empty dissent": `[]`, "not json": `nope`} {
		_, err := scratch.db.ExecContext(ctx, `
INSERT INTO rejected_results (attempt_id, task_id, claim_digest, output_digest, policy_digest,
	reason, dissent, verifiers, excluded, rejected_at_ns)
VALUES ('atm_`+strings.ReplaceAll(name, " ", "_")+`', 'tsk_1', ?, ?, ?, 'authority-rejected', ?, '[]', '[]', 1000)`,
			digest, digest, digest, dissent)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("%s: error = %v, want a CHECK constraint failure", name, err)
		}
	}
}
