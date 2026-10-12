package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// taskDigestGuard is the trigger that refuses a changed tasks.task_ir_digest.
const taskDigestGuard = "tasks_task_ir_digest_immutable"

// taskDigestGuardFromEarlierBuild is the guard exactly as a build between the
// branch consolidation (PR #98) and migration 7 created it: a plain CREATE
// TRIGGER inside migration 5. It is frozen here on purpose. Migration 7 may be
// reworded later, but a database those builds wrote keeps this text.
const taskDigestGuardFromEarlierBuild = `CREATE TRIGGER tasks_task_ir_digest_immutable
BEFORE UPDATE OF task_ir_digest ON tasks
WHEN OLD.task_ir_digest <> NEW.task_ir_digest
BEGIN
	SELECT RAISE(ABORT, 'task TaskIR digest is immutable');
END`

// userVersion reads PRAGMA user_version of an open store.
func userVersion(t testing.TB, store *Store) int {
	t.Helper()
	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return version
}

// countTriggers returns how many triggers named name the database holds.
func countTriggers(t testing.TB, store *Store, name string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatalf("count trigger %s: %v", name, err)
	}
	return n
}

// requireTaskUnchanged loads the Task and requires every field to equal want.
// when says which step the load follows, for the failure message.
func requireTaskUnchanged(t testing.TB, store *Store, want taskstate.TaskRecord, when string) {
	t.Helper()
	got, err := store.LoadTask(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("LoadTask() %s error = %v", when, err)
	}
	if got.ID != want.ID ||
		got.TaskIRDigest != want.TaskIRDigest ||
		got.Phase != want.Phase ||
		got.TerminalOutcome != want.TerminalOutcome ||
		got.CancellationRequested != want.CancellationRequested ||
		got.Revision != want.Revision ||
		!got.CreatedAt.Equal(want.CreatedAt) ||
		!got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("Task changed %s: got=%#v want=%#v", when, got, want)
	}
}

// requireDigestRebindRefused writes a different TaskIR digest straight into
// the Task row, bypassing the adapter, and requires the schema to refuse it
// and the stored digest to stay as it was.
func requireDigestRebindRefused(t testing.TB, store *Store, task taskstate.TaskRecord, when string) {
	t.Helper()
	rebound := contract.SHA256([]byte("rebound:" + task.ID.String())).Hex()
	_, err := store.db.Exec(`UPDATE tasks SET task_ir_digest = ? WHERE task_id = ?`, rebound, task.ID.String())
	if err == nil || !strings.Contains(err.Error(), "TaskIR digest is immutable") {
		t.Fatalf("direct TaskIR digest rebind %s error = %v, want immutable digest failure", when, err)
	}
	requireTaskUnchanged(t, store, task, "after the refused rebind "+when)
}

func TestVersion4DatabaseUpgradesToTheTaskDigestGuard(t *testing.T) {
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
	if got := userVersion(t, upgraded); got != len(migrations) {
		t.Fatalf("user_version after upgrade from v4 = %d, want %d (latest)", got, len(migrations))
	}
	requireTaskUnchanged(t, upgraded, task, "across the v4 -> latest migration")
	requireDigestRebindRefused(t, upgraded, task, "after the v4 -> latest migration")
}

// TestVersion5DatabaseUpgradesToTheTaskDigestGuard covers the database that
// the shipped version 5 produced: it records the policy epoch and has no digest
// guard. Migration 5 must stay as it shipped, so the guard has to arrive in a
// later migration that this database has not applied yet.
func TestVersion5DatabaseUpgradesToTheTaskDigestGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old := openStoreWithMigrations(t, path, migrations[:5])
	if got := userVersion(t, old); got != 5 {
		t.Fatalf("fixture user_version = %d, want 5", got)
	}
	// Without this check the fixture could already carry the guard, and the
	// test would pass without any migration adding it.
	if n := countTriggers(t, old, taskDigestGuard); n != 0 {
		t.Fatalf("version 5 fixture has %d %s trigger(s), want 0: it must be the shipped schema", n, taskDigestGuard)
	}
	task := newTaskRecord(t, "v5-digest-guard")
	task.Phase = taskstate.TaskPhaseReady
	if err := old.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() on v5 error = %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("close v5 store: %v", err)
	}

	upgraded := openStore(t, path)
	defer upgraded.Close()
	if got := userVersion(t, upgraded); got != len(migrations) {
		t.Fatalf("user_version after upgrade from v5 = %d, want %d (latest)", got, len(migrations))
	}
	requireTaskUnchanged(t, upgraded, task, "across the v5 -> latest migration")
	if n := countTriggers(t, upgraded, taskDigestGuard); n != 1 {
		t.Fatalf("%s trigger count after upgrade from v5 = %d, want 1", taskDigestGuard, n)
	}
	requireDigestRebindRefused(t, upgraded, task, "after the v5 -> latest migration")
}

// TestVersion6DatabaseThatAlreadyHasTheDigestGuardUpgrades covers the database
// a build with the guard inside migration 5 created: it is at version 6 and
// already holds the trigger. Migration 7 adds the same trigger, so it must not
// fail on the existing one and must leave exactly one.
func TestVersion6DatabaseThatAlreadyHasTheDigestGuardUpgrades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old := openStoreWithMigrations(t, path, migrations[:6])
	if got := userVersion(t, old); got != 6 {
		t.Fatalf("fixture user_version = %d, want 6", got)
	}
	// The guard is created by hand below; a fixture that already has it would
	// not simulate the earlier build.
	if n := countTriggers(t, old, taskDigestGuard); n != 0 {
		t.Fatalf("version 6 fixture has %d %s trigger(s) before the hand-made one, want 0", n, taskDigestGuard)
	}
	if _, err := old.db.Exec(taskDigestGuardFromEarlierBuild); err != nil {
		t.Fatalf("create the digest guard as the earlier build did: %v", err)
	}
	if n := countTriggers(t, old, taskDigestGuard); n != 1 {
		t.Fatalf("fixture %s trigger count = %d, want 1", taskDigestGuard, n)
	}
	task := newTaskRecord(t, "v6-digest-guard")
	task.Phase = taskstate.TaskPhaseReady
	if err := old.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() on v6 error = %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("close v6 store: %v", err)
	}

	upgraded, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() of a v6 database that already has the digest guard error = %v, want success", err)
	}
	defer upgraded.Close()
	if got := userVersion(t, upgraded); got != len(migrations) {
		t.Fatalf("user_version after upgrade from v6 = %d, want %d (latest)", got, len(migrations))
	}
	requireTaskUnchanged(t, upgraded, task, "across the v6 -> latest migration")
	if n := countTriggers(t, upgraded, taskDigestGuard); n != 1 {
		t.Fatalf("%s trigger count after upgrade from v6 = %d, want exactly 1", taskDigestGuard, n)
	}
	requireDigestRebindRefused(t, upgraded, task, "after the v6 -> latest migration")
}

// revisionTimeCase is one direct row write for the revision and time CHECK
// constraints. Each reject case violates exactly one of them, and each accepted
// case is a valid row, so the outcome comes from the value under test alone.
type revisionTimeCase struct {
	name     string
	revision int64
	updated  int64
	reject   bool
}

// assertCheckOutcome requires a rejected row to fail on a CHECK constraint, and
// an accepted row to insert without error.
func assertCheckOutcome(t testing.TB, err error, reject bool) {
	t.Helper()
	switch {
	case reject && (err == nil || !strings.Contains(err.Error(), "CHECK constraint failed")):
		t.Fatalf("insert error = %v, want a CHECK constraint failure", err)
	case !reject && err != nil:
		t.Fatalf("insert error = %v, want the row to be accepted", err)
	}
}

// TestRevisionAndTimeChecksRejectDirectTaskRows checks the tasks table CHECKs
// that require revision >= 1 and updated_at not earlier than created_at. No
// trigger guards tasks inserts, so only these CHECKs can refuse the row.
func TestRevisionAndTimeChecksRejectDirectTaskRows(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	created := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC).UnixNano()
	cases := []revisionTimeCase{
		{name: "revision 1 with updated after created is accepted", revision: 1, updated: created + 1},
		{name: "revision 1 with updated equal to created is accepted", revision: 1, updated: created},
		{name: "revision 0 is rejected", revision: 0, updated: created, reject: true},
		{name: "updated before created is rejected", revision: 1, updated: created - 1, reject: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			id, err := ids.NewTaskID()
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.db.Exec(`
INSERT INTO tasks (
	task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested,
	revision, created_at_ns, updated_at_ns
) VALUES (?, ?, 'ready', '', 0, ?, ?, ?)`,
				id.String(),
				contract.SHA256([]byte("task-check-"+tc.name)).Hex(),
				tc.revision,
				created,
				tc.updated,
			)
			assertCheckOutcome(t, err, tc.reject)
		})
	}
}

// TestRevisionAndTimeChecksRejectDirectAttemptRows checks the same two CHECKs on
// the attempts table. The insert guard only admits a first Attempt at revision
// 1, so it is dropped here and the CHECK constraints decide each row.
func TestRevisionAndTimeChecksRejectDirectAttemptRows(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	task := newTaskRecord(t, "attempt-revision-time")
	task.Phase = taskstate.TaskPhaseReady
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	dropAttemptGuardTriggers(t, store)

	created := task.CreatedAt.UnixNano()
	cases := []revisionTimeCase{
		{name: "revision 1 with updated after created is accepted", revision: 1, updated: created + 1},
		{name: "revision 1 with updated equal to created is accepted", revision: 1, updated: created},
		{name: "revision 0 is rejected", revision: 0, updated: created, reject: true},
		{name: "updated before created is rejected", revision: 1, updated: created - 1, reject: true},
	}
	for i, tc := range cases {
		tc := tc
		number := int64(i + 1)
		t.Run(tc.name, func(t *testing.T) {
			attemptID, err := ids.NewAttemptID()
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.db.Exec(`
INSERT INTO attempts (
	attempt_id, task_id, task_ir_digest, attempt_number, phase,
	worker_identity, lease_id, close_reason, revision, created_at_ns, updated_at_ns
) VALUES (?, ?, ?, ?, 'created', '', NULL, '', ?, ?, ?)`,
				attemptID.String(),
				task.ID.String(),
				task.TaskIRDigest.Hex(),
				number,
				tc.revision,
				created,
				tc.updated,
			)
			assertCheckOutcome(t, err, tc.reject)
		})
	}
}
