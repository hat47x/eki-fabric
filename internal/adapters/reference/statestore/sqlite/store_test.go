package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func openStore(t testing.TB, path string) *Store {
	t.Helper()
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	return store
}

func TestTaskStoreContract(t *testing.T) {
	statestoretest.RunTaskStoreContract(t, func(t testing.TB) statestore.TaskStore {
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

func TestDurabilityContract(t *testing.T) {
	statestoretest.RunDurabilityContract(t, statestoretest.DurableFactory{
		Open: func(t testing.TB, location string) (statestore.TaskStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

func TestAttemptStoreContract(t *testing.T) {
	statestoretest.RunAttemptStoreContract(t, func(t testing.TB) statestoretest.TaskAttemptStore {
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

func TestAttemptDurabilityContract(t *testing.T) {
	statestoretest.RunAttemptDurabilityContract(t, statestoretest.DurableAttemptFactory{
		Open: func(t testing.TB, location string) (statestoretest.TaskAttemptStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

func TestLeaseStoreContract(t *testing.T) {
	statestoretest.RunLeaseStoreContract(t, func(t testing.TB) statestoretest.TaskLeaseStore {
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

func TestLeaseBypassContract(t *testing.T) {
	statestoretest.RunLeaseBypassContract(t, statestoretest.LeaseBypassFactory{
		New: func(t testing.TB) statestoretest.TaskLeaseStore {
			t.Helper()
			store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Errorf("Close() error = %v", err)
				}
			})
			return store
		},
		RebindTaskIRDigest: func(t testing.TB, store statestoretest.TaskLeaseStore, id ids.TaskID, digest contract.Digest) {
			t.Helper()
			// From schema version 7 on, the schema refuses this write. The bypass
			// contract checks the Attempt binding behind that guard, so the guard
			// is removed here to simulate a repair that bypasses the schema. The
			// guard itself is tested in migrate_test.go.
			s := store.(*Store)
			if _, err := s.db.Exec(`DROP TRIGGER tasks_task_ir_digest_immutable`); err != nil {
				t.Fatalf("drop TaskIR digest guard: %v", err)
			}
			if _, err := s.db.Exec(`UPDATE tasks SET task_ir_digest = ? WHERE task_id = ?`, digest.Hex(), id.String()); err != nil {
				t.Fatalf("rebind TaskIR digest through SQL: %v", err)
			}
		},
		OverwriteAttempt: func(t testing.TB, store statestoretest.TaskLeaseStore, record attemptstate.AttemptRecord) {
			t.Helper()
			s := store.(*Store)
			dropAttemptBindingGuards(t, s)
			var lease any
			if record.LeaseID != "" {
				lease = record.LeaseID.String()
			}
			if _, err := s.db.Exec(`
UPDATE attempts
SET task_id = ?, task_ir_digest = ?, worker_identity = ?, lease_id = ?
WHERE attempt_id = ?`,
				record.TaskID.String(),
				record.TaskIRDigest.Hex(),
				record.WorkerIdentity,
				lease,
				record.ID.String(),
			); err != nil {
				t.Fatalf("overwrite Attempt binding through SQL: %v", err)
			}
		},
	})
}

func TestLeaseDurabilityContract(t *testing.T) {
	statestoretest.RunLeaseDurabilityContract(t, statestoretest.DurableLeaseFactory{
		Open: func(t testing.TB, location string) (statestoretest.TaskLeaseStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

func TestLeaseEmissionStoreContract(t *testing.T) {
	statestoretest.RunLeaseEmissionContract(t, func(t testing.TB) statestoretest.LeaseEmissionTestStore {
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

func TestLeaseEmissionDurabilityContract(t *testing.T) {
	statestoretest.RunLeaseEmissionDurabilityContract(t, statestoretest.DurableLeaseEmissionFactory{
		Open: func(t testing.TB, location string) (statestoretest.LeaseEmissionTestStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

func TestLeaseSigningContract(t *testing.T) {
	statestoretest.RunLeaseSigningContract(t, func(t testing.TB) statestoretest.LeaseEmissionTestStore {
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

func TestLeaseSigningDurabilityContract(t *testing.T) {
	statestoretest.RunLeaseSigningDurabilityContract(t, statestoretest.DurableLeaseEmissionFactory{
		Open: func(t testing.TB, location string) (statestoretest.LeaseEmissionTestStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

func TestWorkOrderSigningContract(t *testing.T) {
	statestoretest.RunWorkOrderSigningContract(t, func(t testing.TB) statestoretest.WorkOrderSigningTestStore {
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

func TestWorkOrderSigningDurabilityContract(t *testing.T) {
	statestoretest.RunWorkOrderSigningDurabilityContract(t, statestoretest.DurableWorkOrderSigningFactory{
		Open: func(t testing.TB, location string) (statestoretest.WorkOrderSigningTestStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

func TestPolicyStoreContract(t *testing.T) {
	statestoretest.RunPolicyStoreContract(t, func(t testing.TB) statestore.PolicyStore {
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

func TestPolicyDurabilityContract(t *testing.T) {
	statestoretest.RunPolicyDurabilityContract(t, statestoretest.DurablePolicyFactory{
		Open: func(t testing.TB, location string) (statestore.PolicyStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("Open(\"\") error = nil, want error")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	record := newTaskRecord(t, "idempotent")

	first := openStore(t, path)
	if err := first.CreateTask(context.Background(), record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Opening an already migrated database must not re-run or reset anything.
	for i := 0; i < 3; i++ {
		store := openStore(t, path)
		if _, err := store.LoadTask(context.Background(), record.ID); err != nil {
			t.Fatalf("LoadTask() after reopen %d error = %v", i, err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
}

func TestVersion2DatabaseUpgradesToLeaseSchemaWithoutLosingTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := openWithMigrations(context.Background(), path, migrations[:2])
	if err != nil {
		t.Fatalf("open v2 store: %v", err)
	}
	record := newTaskRecord(t, "v2-upgrade")
	if err := old.CreateTask(context.Background(), record); err != nil {
		t.Fatalf("CreateTask() on v2 error = %v", err)
	}
	// A Task is created admitted; ready is a separate write (#365).
	ready := record
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = record.CreatedAt.Add(time.Second)
	if err := old.CompareAndSwapTask(context.Background(), record.ID, record.Revision, ready); err != nil {
		t.Fatalf("CreateTask() on v2 error = %v", err)
	}
	record = ready
	if err := old.Close(); err != nil {
		t.Fatalf("close v2 store: %v", err)
	}

	upgraded := openStore(t, path)
	defer upgraded.Close()
	got, err := upgraded.LoadTask(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("LoadTask() after v3 migration error = %v", err)
	}
	if got.ID != record.ID || got.TaskIRDigest != record.TaskIRDigest || got.Phase != record.Phase {
		t.Fatalf("Task changed across v2 -> v3 migration: got=%#v want=%#v", got, record)
	}
}

func TestVersion3DatabaseUpgradesWithoutLosingLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := openWithMigrations(context.Background(), path, migrations[:3])
	if err != nil {
		t.Fatalf("open v3 store: %v", err)
	}

	task := newTaskRecord(t, "v3-upgrade")
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
	attemptID, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	placementDigest := contract.SHA256([]byte("placement-v3-upgrade"))
	if _, err := old.db.Exec(`
INSERT INTO lease_fences (task_id, last_fence_epoch) VALUES (?, ?)`,
		task.ID.String(), 1); err != nil {
		t.Fatalf("seed v3 fence: %v", err)
	}
	if _, err := old.db.Exec(`
INSERT INTO leases (
	lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
	placement_trust_domain, placement_authority_principal, placement_decision_id,
	placement_payload_digest, placement_expires_at_ns, policy_revision,
	fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		leaseID.String(),
		task.ID.String(),
		task.TaskIRDigest.Hex(),
		attemptID.String(),
		"worker-a",
		"local.example",
		"control-plane/plc-1",
		"placement-v3-upgrade",
		placementDigest.Hex(),
		now.Add(2*time.Minute).UnixNano(),
		"policy-42",
		1,
		0,
		now.UnixNano(),
		now.Add(time.Minute).UnixNano(),
	); err != nil {
		t.Fatalf("seed v3 lease: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded := openStore(t, path)
	defer upgraded.Close()

	gotLease, err := upgraded.LoadLease(context.Background(), leaseID)
	if err != nil {
		t.Fatalf("LoadLease() after v4 migration error = %v", err)
	}
	if gotLease.ID != leaseID || gotLease.FenceEpoch != 1 ||
		gotLease.AttemptID != attemptID || gotLease.PlacementPayloadDigest != placementDigest {
		t.Fatalf("Lease changed across v3 -> current migration: %#v", gotLease)
	}
	if gotLease.PolicyFenced() || gotLease.PolicyEpoch != 0 {
		t.Fatalf("pre-v5 Lease unexpectedly gained policy authority: %#v", gotLease)
	}
	if _, err := upgraded.LoadCurrentPolicy(context.Background()); !errors.Is(err, statestore.ErrPolicyUnavailable) {
		t.Fatalf("migration invented current Policy from historical Lease: %v", err)
	}

	newAttemptID := mustAttemptIDForSQLite(t)
	attempt, err := attemptstate.New(newAttemptID, task.ID, task.TaskIRDigest, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := upgraded.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatalf("CreateAttempt() after v4 migration error = %v", err)
	}
	if _, err := upgraded.LoadAttempt(context.Background(), attempt.ID); err != nil {
		t.Fatalf("LoadAttempt() after v4 migration error = %v", err)
	}
}

func TestAttemptSchemaRejectsBypassedInvalidTransitions(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	task := newTaskRecord(t, "attempt-schema-guard")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	// A Task is created admitted; ready is a separate write (#365).
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.CreatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}
	task = ready
	attempt, err := attemptstate.New(mustAttemptIDForSQLite(t), task.ID, task.TaskIRDigest, 1, task.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}

	if _, err := store.db.Exec(`
INSERT INTO attempts (
	attempt_id, task_id, task_ir_digest, attempt_number, phase,
	worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
) VALUES (?, ?, ?, ?, 'placing', 'worker-x', NULL, '', 1, ?, ?)`,
		mustAttemptIDForSQLite(t).String(),
		task.ID.String(),
		task.TaskIRDigest.Hex(),
		99,
		task.CreatedAt.Add(3*time.Second).UnixNano(),
		task.CreatedAt.Add(3*time.Second).UnixNano(),
	); err == nil {
		t.Fatal("non-initial Attempt inserted through direct SQL")
	}

	closedAt := attempt.UpdatedAt.Add(time.Second).UnixNano()
	if _, err := store.db.Exec(`
UPDATE attempts
SET phase = 'closed', close_reason = 'cancelled', revision = 2, updated_at_ns = ?
WHERE attempt_id = ?`, closedAt, attempt.ID.String()); err != nil {
		t.Fatalf("valid created -> closed bypass transition failed: %v", err)
	}

	if _, err := store.db.Exec(`
UPDATE attempts
SET phase = 'placing', worker_identity = 'worker-a', close_reason = '', revision = 3, updated_at_ns = ?
WHERE attempt_id = ?`, closedAt+1, attempt.ID.String()); err == nil {
		t.Fatal("closed Attempt reopened through direct SQL")
	}

	second, err := attemptstate.New(mustAttemptIDForSQLite(t), task.ID, task.TaskIRDigest, 2, task.CreatedAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
UPDATE attempts
SET phase = 'placing', worker_identity = 'worker-b', revision = 3, updated_at_ns = ?
WHERE attempt_id = ?`, second.UpdatedAt.Add(time.Second).UnixNano(), second.ID.String()); err == nil {
		t.Fatal("Attempt revision jumped through direct SQL")
	}
	if _, err := store.db.Exec(`
UPDATE attempts SET attempt_number = 99 WHERE attempt_id = ?`, second.ID.String()); err == nil {
		t.Fatal("Attempt immutable identity changed through direct SQL")
	}
}

func TestLeaseSchemaAssignsAttemptAndRejectsWrongBinding(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	ensureSQLitePolicy(t, store, "policy-42")

	task := newTaskRecord(t, "lease-attempt-trigger")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	// A Task is created admitted; ready is a separate write (#365).
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.CreatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}
	task = ready
	placing := createPlacingAttemptForSQLite(t, store, task, 1, "worker-a")
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	payloadDigest := contract.SHA256([]byte("trigger-placement"))
	if _, err := store.db.Exec(`
INSERT INTO lease_fences (task_id, last_fence_epoch) VALUES (?, ?)`,
		task.ID.String(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
INSERT INTO leases (
	lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
	placement_trust_domain, placement_authority_principal, placement_decision_id,
	placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
	fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		leaseID.String(),
		task.ID.String(),
		task.TaskIRDigest.Hex(),
		placing.ID.String(),
		placing.WorkerIdentity,
		"local.example",
		"control-plane/plc-1",
		"trigger-placement",
		payloadDigest.Hex(),
		now.Add(2*time.Minute).UnixNano(),
		"policy-42",
		1,
		1,
		0,
		now.UnixNano(),
		now.Add(time.Minute).UnixNano(),
	); err != nil {
		t.Fatalf("direct valid Lease insert error = %v", err)
	}
	assigned, err := store.LoadAttempt(context.Background(), placing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Phase != attemptstate.AttemptPhaseAssigned ||
		assigned.LeaseID != leaseID ||
		assigned.Revision != placing.Revision+1 {
		t.Fatalf("Lease insert did not atomically assign Attempt: %#v", assigned)
	}

	other := createPlacingAttemptForSQLite(t, store, task, 2, "worker-b")
	otherLease, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
INSERT INTO leases (
	lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
	placement_trust_domain, placement_authority_principal, placement_decision_id,
	placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
	fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		otherLease.String(),
		task.ID.String(),
		task.TaskIRDigest.Hex(),
		other.ID.String(),
		"worker-not-current",
		"local.example",
		"control-plane/plc-1",
		"trigger-placement-wrong-worker",
		contract.SHA256([]byte("wrong-worker")).Hex(),
		now.Add(3*time.Minute).UnixNano(),
		"policy-42",
		1,
		2,
		0,
		now.Add(time.Minute).UnixNano(),
		now.Add(2*time.Minute).UnixNano(),
	); err == nil {
		t.Fatal("Lease with wrong Worker binding inserted through direct SQL")
	}
	current, err := store.LoadAttempt(context.Background(), other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != attemptstate.AttemptPhasePlacing || current.LeaseID != "" || current.Revision != other.Revision {
		t.Fatalf("failed Lease insert mutated Attempt: %#v", current)
	}
}

func TestPolicySchemaRejectsDirectMutationAndInvalidTransitions(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	p1 := ensureSQLitePolicy(t, store, "policy-42")

	if _, err := store.db.Exec(`
UPDATE policy_history SET revision = 'tampered' WHERE epoch = ?`, int64(p1.Epoch)); err == nil {
		t.Fatal("policy history UPDATE bypassed append-only guard")
	}
	if _, err := store.db.Exec(`
DELETE FROM policy_history WHERE epoch = ?`, int64(p1.Epoch)); err == nil {
		t.Fatal("policy history DELETE bypassed append-only guard")
	}
	if _, err := store.db.Exec(`
DELETE FROM policy_current WHERE singleton = 1`); err == nil {
		t.Fatal("current policy pointer was deleted through direct SQL")
	}

	if _, err := store.db.Exec(`
INSERT INTO policy_history (
	epoch, revision, status, activated_at_ns, revoked_at_ns, revoke_reason
) VALUES (?, ?, 'revoked', ?, ?, ?)`,
		int64(p1.Epoch+1),
		"policy-other",
		p1.ActivatedAt.UnixNano(),
		p1.ActivatedAt.Add(time.Minute).UnixNano(),
		"wrong revision",
	); err == nil {
		t.Fatal("direct SQL inserted revocation for a non-current revision")
	}

	if _, err := store.db.Exec(`
INSERT INTO policy_history (
	epoch, revision, status, activated_at_ns, revoked_at_ns, revoke_reason
) VALUES (?, ?, 'active', ?, NULL, '')`,
		int64(p1.Epoch+2),
		"policy-skip",
		p1.ActivatedAt.Add(time.Minute).UnixNano(),
	); err == nil {
		t.Fatal("direct SQL skipped policy epoch")
	}

	p2, err := policystate.NewActive(p1.Epoch+1, "policy-43", p1.ActivatedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), p1.Epoch, p2); err != nil {
		t.Fatalf("advance to replacement policy: %v", err)
	}
	if _, err := store.db.Exec(`
INSERT INTO policy_history (
	epoch, revision, status, activated_at_ns, revoked_at_ns, revoke_reason
) VALUES (?, ?, 'active', ?, NULL, '')`,
		int64(p2.Epoch+1),
		p1.Revision,
		p2.ActivatedAt.Add(time.Minute).UnixNano(),
	); err == nil {
		t.Fatal("direct SQL reactivated a superseded policy revision")
	}
	current, err := store.LoadCurrentPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if current.Epoch != p2.Epoch || current.Revision != p2.Revision || current.Status != policystate.StatusActive {
		t.Fatalf("historical rollback attempt changed current policy: %#v", current)
	}
}

func TestLeaseSchemaRejectsStalePolicyIfAdapterIsBypassed(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	p1 := ensureSQLitePolicy(t, store, "policy-42")
	task := newTaskRecord(t, "lease-policy-trigger")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	// A Task is created admitted; ready is a separate write (#365).
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.CreatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}
	task = ready
	placing := createPlacingAttemptForSQLite(t, store, task, 1, "worker-a")

	p2, err := policystate.NewActive(p1.Epoch+1, "policy-43", p1.ActivatedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), p1.Epoch, p2); err != nil {
		t.Fatal(err)
	}

	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if _, err := store.db.Exec(`
INSERT INTO leases (
	lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
	placement_trust_domain, placement_authority_principal, placement_decision_id,
	placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
	fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		leaseID.String(),
		task.ID.String(),
		task.TaskIRDigest.Hex(),
		placing.ID.String(),
		placing.WorkerIdentity,
		"local.example",
		"control-plane/plc-1",
		"stale-policy-direct-insert",
		contract.SHA256([]byte("stale-policy-direct-insert")).Hex(),
		now.Add(2*time.Minute).UnixNano(),
		p1.Revision,
		int64(p1.Epoch),
		1,
		0,
		now.UnixNano(),
		now.Add(time.Minute).UnixNano(),
	); err == nil {
		t.Fatal("direct SQL inserted Lease under stale Policy Epoch")
	}

	current, err := store.LoadAttempt(context.Background(), placing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != attemptstate.AttemptPhasePlacing || current.LeaseID != "" || current.Revision != placing.Revision {
		t.Fatalf("stale policy insert mutated Attempt: %#v", current)
	}
}

func ensureSQLitePolicy(t testing.TB, store *Store, revision string) policystate.State {
	t.Helper()
	current, err := store.LoadCurrentPolicy(context.Background())
	if err == nil {
		if current.Revision != revision || current.Status != policystate.StatusActive {
			t.Fatalf("current policy = %#v, want active %q", current, revision)
		}
		return current
	}
	if !errors.Is(err, statestore.ErrPolicyUnavailable) {
		t.Fatal(err)
	}
	initial, err := policystate.NewActive(1, revision, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), 0, initial); err != nil {
		t.Fatal(err)
	}
	return initial
}

func mustAttemptIDForSQLite(t testing.TB) ids.AttemptID {
	t.Helper()
	id, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func createPlacingAttemptForSQLite(t testing.TB, store *Store, task taskstate.TaskRecord, number uint64, worker string) attemptstate.AttemptRecord {
	t.Helper()
	attempt, err := attemptstate.New(
		mustAttemptIDForSQLite(t),
		task.ID,
		task.TaskIRDigest,
		number,
		task.UpdatedAt.Add(time.Duration(number)*time.Nanosecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	placing := attempt
	placing.Phase = attemptstate.AttemptPhasePlacing
	placing.WorkerIdentity = worker
	placing.Revision++
	placing.UpdatedAt = attempt.UpdatedAt.Add(time.Nanosecond)
	if err := store.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, placing); err != nil {
		t.Fatal(err)
	}
	return placing
}

func TestFenceEpochSurvivesLeaseHistoryGarbageCollection(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	ensureSQLitePolicy(t, store, "policy-42")

	task := newTaskRecord(t, "lease-history-gc")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	// A Task is created admitted; ready is a separate write (#365).
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.CreatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, ready); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	task = ready
	attempt1 := createPlacingAttemptForSQLite(t, store, task, 1, "worker-a")
	lease1, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	first, err := store.ReserveLease(context.Background(), statestore.LeaseReservation{
		LeaseID:                     lease1,
		TaskID:                      task.ID,
		TaskIRDigest:                task.TaskIRDigest,
		ExpectedTaskRevision:        task.Revision,
		AttemptID:                   attempt1.ID,
		ExpectedAttemptRevision:     attempt1.Revision,
		WorkerIdentity:              attempt1.WorkerIdentity,
		PlacementTrustDomain:        "local.example",
		PlacementAuthorityPrincipal: "control-plane/plc-1",
		PlacementDecisionID:         "placement-gc-1",
		PlacementPayloadDigest:      contract.SHA256([]byte("placement-gc-1")),
		PlacementExpiresAt:          now.Add(2 * time.Minute),
		PolicyRevision:              "policy-42",
		ExpectedPolicyEpoch:         1,
		IssuedAt:                    now,
		ExpiresAt:                   now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("first ReserveLease() error = %v", err)
	}
	if first.Lease.FenceEpoch != 1 {
		t.Fatalf("first fence = %d, want 1", first.Lease.FenceEpoch)
	}

	assigned, err := store.LoadAttempt(context.Background(), attempt1.ID)
	if err != nil {
		t.Fatal(err)
	}
	closed := assigned
	closed.Phase = attemptstate.AttemptPhaseClosed
	closed.CloseReason = attemptstate.CloseReasonLeaseExpired
	closed.Revision++
	closed.UpdatedAt = first.Lease.ExpiresAt
	if closed.UpdatedAt.Before(assigned.UpdatedAt) {
		closed.UpdatedAt = assigned.UpdatedAt
	}
	if err := store.CompareAndSwapAttempt(context.Background(), assigned.ID, assigned.Revision, closed); err != nil {
		t.Fatalf("close expired Attempt before Lease GC: %v", err)
	}

	// Simulate a future retention job that drops expired Lease history. The
	// Attempt keeps the Lease ID as audit provenance while lease_fences keeps
	// the monotonic anti-replay epoch independently of Lease history.
	if _, err := store.db.Exec(`DELETE FROM leases WHERE lease_id = ?`, first.Lease.ID.String()); err != nil {
		t.Fatalf("delete expired Lease history: %v", err)
	}

	attempt2 := createPlacingAttemptForSQLite(t, store, task, 2, "worker-b")
	lease2, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	secondNow := first.Lease.ExpiresAt.Add(time.Nanosecond)
	second, err := store.ReserveLease(context.Background(), statestore.LeaseReservation{
		LeaseID:                     lease2,
		TaskID:                      task.ID,
		TaskIRDigest:                task.TaskIRDigest,
		ExpectedTaskRevision:        task.Revision,
		AttemptID:                   attempt2.ID,
		ExpectedAttemptRevision:     attempt2.Revision,
		WorkerIdentity:              attempt2.WorkerIdentity,
		PlacementTrustDomain:        "local.example",
		PlacementAuthorityPrincipal: "control-plane/plc-1",
		PlacementDecisionID:         "placement-gc-2",
		PlacementPayloadDigest:      contract.SHA256([]byte("placement-gc-2")),
		PlacementExpiresAt:          secondNow.Add(2 * time.Minute),
		PolicyRevision:              "policy-42",
		ExpectedPolicyEpoch:         1,
		IssuedAt:                    secondNow,
		ExpiresAt:                   secondNow.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("second ReserveLease() error = %v", err)
	}
	if second.Lease.FenceEpoch != 2 {
		t.Fatalf("fence after Lease history GC = %d, want 2", second.Lease.FenceEpoch)
	}
}

func TestSchemaVersionIsRecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store := openStore(t, path)
	defer store.Close()

	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d", version, len(migrations))
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store := openStore(t, path)
	if _, err := store.db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err := Open(context.Background(), path)
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Open() error = %v, want ErrUnsupportedSchema", err)
	}
}

func TestFailedMigrationRollsBackAndKeepsPreviousVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	steps := []string{
		`CREATE TABLE a (id INTEGER PRIMARY KEY)`,
		// Creates a table and then fails, so the whole step must roll back.
		`CREATE TABLE b (id INTEGER PRIMARY KEY); CREATE TABLE a (id INTEGER PRIMARY KEY)`,
	}
	if err := migrate(context.Background(), db, steps); err == nil {
		t.Fatal("migrate() error = nil, want failure on the second step")
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 1 {
		t.Fatalf("user_version = %d, want 1 (first step kept, second rolled back)", version)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'b'`).Scan(&n); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if n != 0 {
		t.Fatal("table b exists after the failed step; migration was not rolled back")
	}
}

func TestSchemaRejectsInvalidRowsEvenIfAdapterIsBypassed(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	digest := contract.SHA256([]byte("check")).Hex()
	cases := map[string]string{
		"unknown phase":                `INSERT INTO tasks VALUES ('t1', '` + digest + `', 'bogus', '', 0, 1, 1, 1)`,
		"terminal without outcome":     `INSERT INTO tasks VALUES ('t2', '` + digest + `', 'terminal', '', 0, 1, 1, 1)`,
		"outcome on non-terminal task": `INSERT INTO tasks VALUES ('t3', '` + digest + `', 'ready', 'completed', 0, 1, 1, 1)`,
		"zero revision":                `INSERT INTO tasks VALUES ('t4', '` + digest + `', 'ready', '', 0, 0, 1, 1)`,
		"updated before created":       `INSERT INTO tasks VALUES ('t5', '` + digest + `', 'ready', '', 0, 1, 2, 1)`,
		"short digest":                 `INSERT INTO tasks VALUES ('t6', 'abcd', 'ready', '', 0, 1, 1, 1)`,
	}
	for name, stmt := range cases {
		if _, err := store.db.Exec(stmt); err == nil {
			t.Errorf("%s: insert succeeded, want constraint failure", name)
		}
	}
}

func TestSQLInjectionLikeIDIsTreatedAsData(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	record := newTaskRecord(t, "injection")
	if err := store.CreateTask(context.Background(), record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	_, err := store.LoadTask(context.Background(), ids.TaskID("x' OR '1'='1"))
	if !errors.Is(err, statestore.ErrNotFound) {
		t.Fatalf("LoadTask() error = %v, want ErrNotFound", err)
	}
}

func TestConcurrentReadersAndWriterSmoke(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	record := newTaskRecord(t, "concurrent-rw")
	if err := store.CreateTask(context.Background(), record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	const writes = 50
	var wg sync.WaitGroup
	errs := make(chan error, 8)

	wg.Add(1)
	go func() {
		defer wg.Done()
		current := record
		for i := 0; i < writes; i++ {
			next := current
			next.Revision = current.Revision + 1
			next.UpdatedAt = current.UpdatedAt.Add(time.Second)
			if err := store.CompareAndSwapTask(context.Background(), current.ID, current.Revision, next); err != nil {
				errs <- err
				return
			}
			current = next
		}
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last uint64
			for i := 0; i < 200; i++ {
				got, err := store.LoadTask(context.Background(), record.ID)
				if err != nil {
					errs <- err
					return
				}
				if got.Revision < last {
					errs <- errors.New("revision went backwards for a reader")
					return
				}
				last = got.Revision
			}
		}()
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent access error = %v", err)
	}

	got, err := store.LoadTask(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if got.Revision != record.Revision+writes {
		t.Fatalf("final revision = %d, want %d", got.Revision, record.Revision+writes)
	}
}

func TestCreateTaskIsAtomicAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	record := newTaskRecord(t, "terminal")
	record.CancellationRequested = true

	store := openStore(t, path)
	if err := store.CreateTask(context.Background(), record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	// The completed phase is only written by the accepted result (#365), so
	// restart continuity is shown with the cancelled outcome instead.
	terminal := record
	terminal.Phase = taskstate.TaskPhaseTerminal
	terminal.TerminalOutcome = taskstate.TerminalOutcomeCancelled
	terminal.Revision++
	terminal.UpdatedAt = record.CreatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), record.ID, record.Revision, terminal); err != nil {
		t.Fatalf("CompareAndSwapTask() error = %v", err)
	}
	record = terminal
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openStore(t, path)
	defer reopened.Close()
	got, err := reopened.LoadTask(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if got.Phase != taskstate.TaskPhaseTerminal ||
		got.TerminalOutcome != taskstate.TerminalOutcomeCancelled ||
		!got.CancellationRequested ||
		got.TaskIRDigest != record.TaskIRDigest ||
		!got.CreatedAt.Equal(record.CreatedAt) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", got, record)
	}
}

func TestDatabaseFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "state.db")
	store := openStore(t, path)
	defer store.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("database file mode = %o, want no group/other access", perm)
	}
}

func TestPathWithReservedCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dir with space")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// "?" is legal in a POSIX file name but not on Windows, where the file
	// system rejects it before the driver ever sees the DSN. Use characters
	// that are legal on the running platform while still requiring URI
	// escaping; TestDSNEscapesReservedCharacters covers "?" everywhere.
	name := "state#1?.db"
	if runtime.GOOS == "windows" {
		name = "state#1%.db"
	}
	path := filepath.Join(dir, name)

	record := newTaskRecord(t, "reserved-path")
	store := openStore(t, path)
	if err := store.CreateTask(context.Background(), record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected database at %q: %v", path, err)
	}

	reopened := openStore(t, path)
	defer reopened.Close()
	if _, err := reopened.LoadTask(context.Background(), record.ID); err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
}

// TestDSNEscapesReservedCharacters checks the escaping rule independently of
// the host file system. A file name can contain "?" or "#" on POSIX platforms,
// and SQLite reads an unescaped "?" as the start of the query string. The test
// therefore asserts on the generated DSN for paths that no single platform
// needs to be able to create.
func TestDSNEscapesReservedCharacters(t *testing.T) {
	paths := []struct{ path, posix, windows string }{
		{"/var/lib/eki/state#1?.db", "/var/lib/eki/state#1?.db", "/var/lib/eki/state#1?.db"},
		{`C:\work\eki\state#1?.db`, `/C:\work\eki\state#1?.db`, "/C:/work/eki/state#1?.db"},
		{`relative dir/state%201#2?.db`, `relative dir/state%201#2?.db`, `relative dir/state%201#2?.db`},
		{`/tmp/eki/state\copy#1?.db`, `/tmp/eki/state\copy#1?.db`, "/tmp/eki/state/copy#1?.db"},
	}
	for _, row := range paths {
		t.Run(row.path, func(t *testing.T) {
			path := row.path
			u, err := url.Parse(dsn(path))
			if err != nil {
				t.Fatalf("url.Parse(dsn(%q)) error = %v", path, err)
			}
			if u.Scheme != "file" {
				t.Fatalf("scheme = %q, want file", u.Scheme)
			}
			opaque := u.Opaque
			if opaque == "" {
				opaque = u.Path
			}
			got, err := url.PathUnescape(opaque)
			if err != nil {
				t.Fatalf("PathUnescape(%q) error = %v", opaque, err)
			}
			// Backslash is a separator on Windows, but a literal filename
			// character on POSIX. Escaping must preserve the host's path.
			want := row.posix
			if runtime.GOOS == "windows" {
				want = row.windows
			}
			if got != want {
				t.Fatalf("opaque path = %q, want %q", got, want)
			}
			if q := u.Query(); q.Get("_txlock") != "immediate" {
				t.Fatalf("_txlock = %q, want immediate", q.Get("_txlock"))
			}
		})
	}
}

func newTaskRecord(t testing.TB, seed string) taskstate.TaskRecord {
	t.Helper()
	id, err := ids.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() error = %v", err)
	}
	record, err := taskstate.NewTask(id, contract.SHA256([]byte(seed)), time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	return record
}

func TestAssignmentContract(t *testing.T) {
	statestoretest.RunAssignmentContract(t, func(t testing.TB) statestoretest.AssignmentTestStore {
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

// TestAssignmentListingDropsExpiredLeases pins the expiry filter the listing
// applies: a Lease whose validity window closed on the wall clock is not
// listed, because the Worker refuses expired material anyway and a stale row
// would crowd out live ones.
func TestAssignmentListingDropsExpiredLeases(t *testing.T) {
	statestoretest.RunAssignmentExpiryProbe(t, func(t testing.TB) statestoretest.AssignmentTestStore {
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

// TestAssignmentRevocationContract pins the Control Plane's reclaim over the
// sqlite adapter, including the version-17 delivery_revocations table and its
// immutability triggers (ADR-0030 §2.2).
func TestAssignmentRevocationContract(t *testing.T) {
	statestoretest.RunAssignmentRevocationContract(t, func(t testing.TB) statestoretest.RevocationTestStore {
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

// TestAssignmentHeartbeatContract pins the polling Worker's liveness report
// over the sqlite adapter, including the version-18 assignment_heartbeats
// table (ADR-0030 §2.2).
func TestAssignmentHeartbeatContract(t *testing.T) {
	statestoretest.RunAssignmentHeartbeatContract(t, func(t testing.TB) statestoretest.HeartbeatTestStore {
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
