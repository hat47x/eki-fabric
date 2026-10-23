package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestRecoveryStoreContract(t *testing.T) {
	statestoretest.RunRecoveryStoreContract(t, func(t testing.TB) statestoretest.RecoveryTestStore {
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

func TestRecoveryDurabilityContract(t *testing.T) {
	statestoretest.RunRecoveryDurabilityContract(t, statestoretest.DurableRecoveryFactory{
		Open: func(t testing.TB, location string) (statestoretest.RecoveryTestStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

const insertPermitRow = `
INSERT INTO recovery_permits (previous_attempt_id, task_id, task_ir_digest, previous_attempt_number, next_attempt_number,
	policy_revision, sequence, issued_at_ns, not_before_ns, expires_at_ns, estimated_transfer_bytes, estimated_compute_millis,
	permit_digest, spent_by)
SELECT 'atm_p', task_id, task_ir_digest, 1, 2, 7, 1, 1, 2, 3, '10', '10', ?, NULL FROM tasks LIMIT 1`

// The recovery tables protect themselves against a writer that bypasses the
// adapter: a Permit is used once and never rewritten, a committed pass is fixed,
// and the sequence only moves forward by one.
func TestRecoveryTablesRefuseRewrites(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	ctx := context.Background()

	task := newTaskRecord(t, "recovery-tables")
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	digest := contract.SHA256([]byte("permit")).Hex()
	if _, err := store.db.Exec(insertPermitRow, digest); err != nil {
		t.Fatalf("insert Permit: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO recovery_releases (sequence, fingerprint) VALUES (1, ?)`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO recovery_window (singleton, policy_revision, window_start_ns, released, transfer_bytes, compute_millis, last_sequence)
		VALUES (1, 7, 1, 1, '10', '10', 1)`); err != nil {
		t.Fatal(err)
	}

	refused := map[string]string{
		"a Permit with a later expiry": `UPDATE recovery_permits SET expires_at_ns = expires_at_ns + 1`,
		"a Permit with another digest": `UPDATE recovery_permits SET permit_digest = '` + contract.SHA256([]byte("x")).Hex() + `'`,
		"a Permit marked unused again": `UPDATE recovery_permits SET spent_by = NULL`,
		"a Permit removed":             `DELETE FROM recovery_permits`,
		"a committed pass changed":     `UPDATE recovery_releases SET fingerprint = '` + contract.SHA256([]byte("y")).Hex() + `'`,
		"a committed pass removed":     `DELETE FROM recovery_releases`,
		"a sequence that skips":        `UPDATE recovery_window SET last_sequence = 3`,
		"a sequence that goes back":    `UPDATE recovery_window SET last_sequence = 0`,
		"the window removed":           `DELETE FROM recovery_window`,
	}
	for name, statement := range refused {
		if _, err := store.db.Exec(statement); err == nil {
			t.Fatalf("%s: the statement succeeded", name)
		}
	}

	// The one change a Permit accepts: used for the first time.
	if _, err := store.db.Exec(`UPDATE recovery_permits SET spent_by = 'atm_n'`); err != nil {
		t.Fatalf("marking a Permit used: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE recovery_permits SET spent_by = 'atm_other'`); err == nil ||
		!strings.Contains(err.Error(), "written once and used once") {
		t.Fatalf("using a Permit twice error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE recovery_window SET last_sequence = 2, released = 2`); err != nil {
		t.Fatalf("moving the sequence forward by one: %v", err)
	}
}

// A row edited behind the adapter's back is not trusted: the stored digest binds
// the fields.
func TestEditedPermitRowIsRefusedWhenRead(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	ctx := context.Background()
	task := newTaskRecord(t, "edited-permit")
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(insertPermitRow, contract.SHA256([]byte("permit")).Hex()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadRecoveryPermit(ctx, "atm_p"); err == nil {
		t.Fatal("a Permit row whose digest does not match its fields was accepted")
	}
}

// A database written before the recovery state existed keeps its Tasks and gains
// empty recovery tables, so recovery can start on an upgraded store.
func TestVersion11DatabaseGainsRecoveryStateWithoutLosingTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := openWithMigrations(context.Background(), path, migrations[:11])
	if err != nil {
		t.Fatalf("open v11 store: %v", err)
	}
	if _, err := old.db.Exec(`SELECT 1 FROM recovery_pending`); err == nil {
		t.Fatal("the v11 schema already has recovery_pending")
	}
	task := newTaskRecord(t, "v11-recovery")
	if err := old.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded := openStore(t, path)
	defer upgraded.Close()
	if got, err := upgraded.LoadTask(context.Background(), task.ID); err != nil || got.ID != task.ID {
		t.Fatalf("Task after upgrade = %+v, %v", got, err)
	}
	if pending, err := upgraded.LoadRecoveryPending(context.Background()); err != nil || len(pending) != 0 {
		t.Fatalf("LoadRecoveryPending() = %+v, %v, want none", pending, err)
	}
	if _, ok, err := upgraded.LoadRecoveryWindow(context.Background()); err != nil || ok {
		t.Fatalf("LoadRecoveryWindow() = %v, %v, want none", ok, err)
	}
}
