package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// attemptRowCase is one row written straight into the attempts table. A
// reject case must fail on a CHECK constraint, and each reject case violates
// exactly one CHECK, so removing that CHECK makes the case succeed.
type attemptRowCase struct {
	name        string
	phase       string
	worker      string
	lease       string // "" stores NULL
	closeReason string
	reject      bool
}

// dropAttemptBindingGuards removes the attempt triggers that refuse changes to
// identity and to phase, worker or lease. A test that corrupts a stored row on
// purpose needs them gone. Only the test database is affected.
func dropAttemptBindingGuards(t testing.TB, store *Store) {
	t.Helper()
	for _, name := range []string{"attempts_identity_immutable", "attempts_transition_guard"} {
		if _, err := store.db.Exec(`DROP TRIGGER ` + name); err != nil {
			t.Fatalf("drop trigger %s: %v", name, err)
		}
	}
}

// dropAttemptGuardTriggers removes every attempts trigger. The insert and
// transition triggers refuse most of the rows below before the CHECK
// constraints are evaluated, so they would hide a missing CHECK. Only the
// CHECK constraints are under test here.
func dropAttemptGuardTriggers(t testing.TB, store *Store) {
	t.Helper()
	for _, name := range []string{"attempts_insert_guard", "attempts_task_ir_insert"} {
		if _, err := store.db.Exec(`DROP TRIGGER ` + name); err != nil {
			t.Fatalf("drop trigger %s: %v", name, err)
		}
	}
	dropAttemptBindingGuards(t, store)
}

func TestAttemptCheckConstraintsRejectRowsOutsideTheLifecycle(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	task := newTaskRecord(t, "attempt-check-constraints")
	task.Phase = taskstate.TaskPhaseReady
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	dropAttemptGuardTriggers(t, store)

	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	lease := leaseID.String()

	cases := []attemptRowCase{
		{name: "created without Worker or Lease", phase: "created"},
		{name: "created bound to Worker", phase: "created", worker: "worker-a", reject: true},
		{name: "created bound to Lease", phase: "created", worker: "worker-a", lease: lease, reject: true},
		{name: "created with close reason", phase: "created", closeReason: "cancelled", reject: true},
		{name: "placing with Worker and no Lease", phase: "placing", worker: "worker-a"},
		{name: "placing without Worker", phase: "placing", reject: true},
		{name: "placing bound to Lease", phase: "placing", worker: "worker-a", lease: lease, reject: true},
		{name: "placing with close reason", phase: "placing", worker: "worker-a", closeReason: "cancelled", reject: true},
		{name: "closed with close reason", phase: "closed", closeReason: "cancelled"},
		{name: "closed without close reason", phase: "closed", reject: true},
		{name: "closed with Lease but no Worker", phase: "closed", lease: lease, closeReason: "cancelled", reject: true},
		{name: "closed with Worker and Lease", phase: "closed", worker: "worker-a", lease: lease, closeReason: "lease_expired"},
	}
	for _, phase := range []string{"assigned", "dispatched", "claimed", "verifying"} {
		cases = append(cases,
			attemptRowCase{name: phase + " with Worker and Lease", phase: phase, worker: "worker-a", lease: lease},
			attemptRowCase{name: phase + " without Lease", phase: phase, worker: "worker-a", reject: true},
			attemptRowCase{name: phase + " without Worker or Lease", phase: phase, reject: true},
			attemptRowCase{name: phase + " with close reason", phase: phase, worker: "worker-a", lease: lease, closeReason: "cancelled", reject: true},
		)
	}

	created := task.CreatedAt.UnixNano()
	for i, tc := range cases {
		tc := tc
		number := int64(i + 1)
		t.Run(tc.name, func(t *testing.T) {
			attemptID, err := ids.NewAttemptID()
			if err != nil {
				t.Fatal(err)
			}
			var leaseArg any
			if tc.lease != "" {
				leaseArg = tc.lease
			}
			_, err = store.db.Exec(`
INSERT INTO attempts (
	attempt_id, task_id, task_ir_digest, attempt_number, phase,
	worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
				attemptID.String(),
				task.ID.String(),
				task.TaskIRDigest.Hex(),
				number,
				tc.phase,
				tc.worker,
				leaseArg,
				tc.closeReason,
				created,
				created,
			)
			switch {
			case tc.reject && (err == nil || !strings.Contains(err.Error(), "CHECK constraint failed")):
				t.Fatalf("insert error = %v, want a CHECK constraint failure", err)
			case !tc.reject && err != nil:
				t.Fatalf("insert error = %v, want the row to be accepted", err)
			}
		})
	}
}

func TestTaskIRDigestGuardRejectsDirectRebind(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	task := newTaskRecord(t, "digest-guard")
	task.Phase = taskstate.TaskPhaseReady
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	// Writing the digest the row already has does not rebind the Task.
	if _, err := store.db.Exec(`UPDATE tasks SET task_ir_digest = ? WHERE task_id = ?`, task.TaskIRDigest.Hex(), task.ID.String()); err != nil {
		t.Fatalf("unchanged TaskIR digest update error = %v, want success", err)
	}

	rebound := contract.SHA256([]byte("digest-guard-rebound")).Hex()
	_, err := store.db.Exec(`UPDATE tasks SET task_ir_digest = ? WHERE task_id = ?`, rebound, task.ID.String())
	if err == nil || !strings.Contains(err.Error(), "TaskIR digest is immutable") {
		t.Fatalf("direct TaskIR digest rebind error = %v, want immutable digest failure", err)
	}

	got, err := store.LoadTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if got.TaskIRDigest != task.TaskIRDigest {
		t.Fatalf("rejected rebind changed TaskIR digest: got %s want %s", got.TaskIRDigest.Hex(), task.TaskIRDigest.Hex())
	}
}

func TestVersion4DatabaseGainsTaskDigestGuardWithoutLosingTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := openWithMigrations(context.Background(), path, migrations[:4])
	if err != nil {
		t.Fatalf("open v4 store: %v", err)
	}
	task := newTaskRecord(t, "v4-digest-guard")
	task.Phase = taskstate.TaskPhaseReady
	if err := old.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() on v4 error = %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("close v4 store: %v", err)
	}

	upgraded := openStore(t, path)
	defer upgraded.Close()
	got, err := upgraded.LoadTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("LoadTask() after v5 migration error = %v", err)
	}
	if got.ID != task.ID || got.TaskIRDigest != task.TaskIRDigest || got.Phase != task.Phase {
		t.Fatalf("Task changed across v4 -> v5 migration: got=%#v want=%#v", got, task)
	}

	rebound := contract.SHA256([]byte("v4-digest-guard-rebound")).Hex()
	_, err = upgraded.db.Exec(`UPDATE tasks SET task_ir_digest = ? WHERE task_id = ?`, rebound, task.ID.String())
	if err == nil || !strings.Contains(err.Error(), "TaskIR digest is immutable") {
		t.Fatalf("rebind after v5 migration error = %v, want immutable digest failure", err)
	}
}
