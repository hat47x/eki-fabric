package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestWorkerRegistryContract(t *testing.T) {
	statestoretest.RunWorkerRegistryContract(t, func(t testing.TB) statestore.WorkerRegistryStore {
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

func TestWorkerRegistryDurabilityContract(t *testing.T) {
	statestoretest.RunWorkerRegistryDurabilityContract(t, statestoretest.DurableWorkerRegistryFactory{
		Open: func(t testing.TB, location string) (statestore.WorkerRegistryStore, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var registryTestNow = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

// The enrollment secret must not reach the database file, its write-ahead log
// or the rollback journal: only its SHA-256 is stored.
func TestEnrollmentSecretIsNeverWrittenToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store := openStore(t, path)
	svc, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: fixedClock{registryTestNow}})
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := workerenrollment.NewAdmin("root")
	cred, err := svc.IssueEnrollmentCredential(context.Background(), admin, workerenrollment.IssueRequest{TrustDomain: "team-a.example"})
	if err != nil {
		t.Fatal(err)
	}
	secret := cred.Secret.Reveal()
	random := strings.TrimPrefix(secret, workerregistry.SecretPrefix)
	if _, err := svc.ConsumeEnrollmentCredential(context.Background(), secret, "team-a.example"); err != nil {
		t.Fatal(err)
	}
	// Read the files while the store is open, so the WAL is included, and
	// again after Close, which checkpoints it.
	check := func(stage string) {
		t.Helper()
		var total int
		for _, suffix := range []string{"", "-wal", "-journal"} {
			data, err := os.ReadFile(path + suffix)
			if err != nil {
				continue
			}
			total += len(data)
			if strings.Contains(string(data), secret) || strings.Contains(string(data), random) {
				t.Fatalf("%s: the enrollment secret is in %s", stage, filepath.Base(path+suffix))
			}
		}
		if total == 0 {
			t.Fatalf("%s: read no database bytes", stage)
		}
	}
	check("open")
	var n int
	if err := store.db.QueryRow(`SELECT count(*) FROM enrollment_credentials WHERE secret_sha256 = ?`, cred.Secret.Hash().Hex()).Scan(&n); err != nil || n != 1 {
		t.Fatalf("digest row count = %d, %v", n, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	check("closed")
}

const insertWorkerSQL = `INSERT INTO workers (worker_identity, trust_domain, state, revision, credential_id, state_changed_by, created_at_ns, updated_at_ns)
VALUES (?, ?, 'registered', 1, ?, '', ?, ?)`

// The triggers and constraints must hold against a writer that bypasses the
// adapter, so each statement below goes straight to the database.
func TestWorkerRegistryConstraintsRefuseBypassingWriters(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	svc, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: fixedClock{registryTestNow}})
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := workerenrollment.NewAdmin("root")
	ctx := context.Background()
	issue := func() workerenrollment.IssuedCredential {
		c, err := svc.IssueEnrollmentCredential(ctx, admin, workerenrollment.IssueRequest{TrustDomain: "team-a.example"})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	enroll := func() workerregistry.Worker {
		w, err := svc.ConsumeEnrollmentCredential(ctx, issue().Secret.Reveal(), "team-a.example")
		if err != nil {
			t.Fatal(err)
		}
		return w
	}

	worker := enroll()
	revoked := enroll()
	if _, err := svc.SetWorkerState(ctx, admin, revoked.Identity, workerregistry.StateRevoked); err != nil {
		t.Fatal(err)
	}
	unused := issue()
	nowNS := registryTestNow.UnixNano()
	expiryNS := unused.ExpiresAt.UnixNano()

	refused := []struct {
		name string
		sql  string
		args []any
	}{
		{"revoked worker is final", `UPDATE workers SET state = 'registered', revision = revision + 1 WHERE worker_identity = ?`, []any{string(revoked.Identity)}},
		{"worker identity is immutable", `UPDATE workers SET worker_identity = 'wrk_00000000000000000000000000000001', revision = revision + 1 WHERE worker_identity = ?`, []any{string(worker.Identity)}},
		{"revision moves by one", `UPDATE workers SET state = 'disabled', revision = revision + 5 WHERE worker_identity = ?`, []any{string(worker.Identity)}},
		{"a worker is never deleted", `DELETE FROM workers WHERE worker_identity = ?`, []any{string(revoked.Identity)}},
		{"a credential is never deleted", `DELETE FROM enrollment_credentials WHERE credential_id = ?`, []any{string(unused.ID)}},
		{"a consumed credential is final", `UPDATE enrollment_credentials SET state = 'revoked', revoked_by = 'x', revoked_at_ns = 1 WHERE credential_id = ?`, []any{string(worker.CredentialID)}},
		{"a credential is consumed only by its worker", `UPDATE enrollment_credentials SET state = 'consumed', consumed_worker = 'wrk_00000000000000000000000000000002', consumed_at_ns = ? WHERE credential_id = ?`, []any{nowNS, string(unused.ID)}},
		{"a worker needs an issued credential", insertWorkerSQL, []any{"wrk_00000000000000000000000000000003", "team-a.example", string(worker.CredentialID), nowNS, nowNS}},
		{"a worker needs its credential's trust domain", insertWorkerSQL, []any{"wrk_00000000000000000000000000000004", "team-b.example", string(unused.ID), nowNS, nowNS}},
		{"a worker is not created after expiry", insertWorkerSQL, []any{"wrk_00000000000000000000000000000005", "team-a.example", string(unused.ID), expiryNS, expiryNS}},
		{"an identity is not issued twice", insertWorkerSQL, []any{string(revoked.Identity), "team-a.example", string(unused.ID), nowNS, nowNS}},
		{"a credential lifetime is at most seven days", `INSERT INTO enrollment_credentials (credential_id, secret_sha256, trust_domain, issued_by, issued_at_ns, expires_at_ns, state)
VALUES ('enr_00000000000000000000000000000006', ?, 'team-a.example', 'admin:root', 0, 604800000000001, 'issued')`, []any{strings.Repeat("a", 64)}},
	}
	for _, tc := range refused {
		if _, err := store.db.Exec(tc.sql, tc.args...); err == nil {
			t.Errorf("%s: the database accepted the statement", tc.name)
		}
	}

	// One credential admits one Worker: the first insert is allowed because the
	// credential is still issued, a second for the same credential never is.
	first, _ := ids.NewWorkerIdentity()
	second, _ := ids.NewWorkerIdentity()
	if _, err := store.db.Exec(insertWorkerSQL, string(first), "team-a.example", string(unused.ID), nowNS, nowNS); err != nil {
		t.Fatalf("first worker for an issued credential: %v", err)
	}
	if _, err := store.db.Exec(insertWorkerSQL, string(second), "team-a.example", string(unused.ID), nowNS, nowNS); err == nil {
		t.Fatal("two workers for one credential")
	}
}

func TestWorkerRegistryMigrationIsAppended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	older, err := openWithMigrations(context.Background(), path, migrations[:14])
	if err != nil {
		t.Fatal(err)
	}
	if err := older.Close(); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, path) // upgrades version 14 to the latest
	defer store.Close()
	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != len(migrations) {
		t.Fatalf("user_version = %d, %v; want %d", version, err, len(migrations))
	}
	if _, err := store.db.Exec(`SELECT count(*) FROM workers`); err != nil {
		t.Fatalf("workers table missing after upgrade: %v", err)
	}
}
