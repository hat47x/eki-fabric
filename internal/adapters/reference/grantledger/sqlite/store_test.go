package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/grantledgertest"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/grant"
)

var _ grantledgertest.Ledger = (*Store)(nil)

const (
	testProvider    grant.Identity = "provider-alpha"
	testBeneficiary grant.Identity = "beneficiary-research"
)

const testDay = 24 * time.Hour

var (
	testIssuedAt = time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	testRevokeAt = testIssuedAt.Add(10 * testDay)
	testReq      = grant.Request{ResourceClass: "gpu-class-a", Percent: 1}
)

func openStore(t testing.TB, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	return s
}

// testGrant issues a grant whose digest depends only on the policy label, so two
// calls with the same label return grants that share a digest.
func testGrant(t testing.TB, policy string) grant.Grant {
	t.Helper()
	spec := grant.Spec{
		Provider:    testProvider,
		Beneficiary: testBeneficiary,
		Ceilings: []grant.ResourceCeiling{
			{Class: "gpu-class-a", Percent: 40},
			{Class: "cpu-class-b", Percent: 25},
		},
		IssuedAt:            testIssuedAt,
		ExpiresAt:           testIssuedAt.Add(30 * testDay),
		MaxConcurrentLeases: 4,
		Purpose:             grant.PurposePublicResearch,
		PolicyRevision:      contract.SHA256([]byte(policy)),
	}
	g, err := grant.Issue(testProvider, spec)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	return g
}

func expectError(t testing.TB, err, want error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		return
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func rowCount(t testing.TB, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM grant_revocations`).Scan(&n); err != nil {
		t.Fatalf("count revocations: %v", err)
	}
	return n
}

func exportOf(t testing.TB, s *Store) []grant.RecordedRevocation {
	t.Helper()
	records, err := s.ledger.Export()
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	return records
}

func sameExports(a, b []grant.RecordedRevocation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Grant != b[i].Grant ||
			a[i].Revocation.Reason != b[i].Revocation.Reason ||
			!a[i].Revocation.EffectiveAt.Equal(b[i].Revocation.EffectiveAt) {
			return false
		}
	}
	return true
}

func TestLedgerContract(t *testing.T) {
	grantledgertest.RunContract(t, func(t testing.TB) grantledgertest.Ledger {
		t.Helper()
		s := openStore(t, filepath.Join(t.TempDir(), "grant-ledger.db"))
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		})
		return s
	})
}

func TestLedgerDurabilityContract(t *testing.T) {
	grantledgertest.RunDurabilityContract(t, grantledgertest.DurableFactory{
		Open: func(t testing.TB, location string) (grantledgertest.Ledger, func() error) {
			t.Helper()
			s := openStore(t, location)
			return s, s.Close
		},
	})
}

// A revocation must be in the file before Revoke returns. An independent
// connection must already see it while the Store is open, and a reopen restores it.
func TestRevocationIsCommittedBeforeRevokeReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grant-ledger.db")
	ctx := context.Background()
	g := testGrant(t, "policy-a")

	s := openStore(t, path)
	if err := s.Revoke(ctx, g, testProvider, grant.ReasonCapacityReclaim, testRevokeAt); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	other, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	defer other.Close()
	var seen int
	if err := other.QueryRowContext(ctx, `SELECT count(*) FROM grant_revocations WHERE grant_digest = ?`, g.Digest().Hex()).Scan(&seen); err != nil {
		t.Fatalf("read through second connection: %v", err)
	}
	if seen != 1 {
		t.Fatalf("second connection sees %d rows for the grant, want 1", seen)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened := openStore(t, path)
	defer reopened.Close()
	expectError(t, reopened.Admit(g, testRevokeAt, testReq), grant.ErrGrantNotActive)
}

// Reopening must rebuild exactly the ledger that was written. The exports of the
// original and reopened Stores must match record for record.
func TestReopenedLedgerMatchesStoredRevocations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grant-ledger.db")
	ctx := context.Background()
	grants := []grant.Grant{testGrant(t, "policy-a"), testGrant(t, "policy-b"), testGrant(t, "policy-c")}

	s := openStore(t, path)
	for i, g := range grants {
		at := testRevokeAt.Add(time.Duration(i) * time.Hour)
		if err := s.Revoke(ctx, g, testProvider, grant.ReasonPolicyChange, at); err != nil {
			t.Fatalf("Revoke(%d) error = %v", i, err)
		}
	}
	before := exportOf(t, s)
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openStore(t, path)
	defer reopened.Close()
	after := exportOf(t, reopened)
	if len(before) != len(grants) || !sameExports(before, after) {
		t.Fatalf("reopened export = %+v, want %+v", after, before)
	}
}

// A failed write must not leave the revocation in memory. The rebuild from the
// file must show the grant as not revoked, and a later write must succeed.
func TestFailedWriteRebuildsLedgerFromFile(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "grant-ledger.db"))
	defer s.Close()
	g := testGrant(t, "policy-a")

	if _, err := s.db.Exec(`CREATE TRIGGER refuse_revocation BEFORE INSERT ON grant_revocations
BEGIN SELECT RAISE(ABORT, 'simulated write failure'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	err := s.Revoke(ctx, g, testProvider, grant.ReasonCapacityReclaim, testRevokeAt)
	if err == nil || errors.Is(err, grant.ErrAlreadyRevoked) {
		t.Fatalf("Revoke() error = %v, want the simulated write failure", err)
	}
	if n := rowCount(t, s); n != 0 {
		t.Fatalf("rows after failed write = %d, want 0", n)
	}
	if n := len(exportOf(t, s)); n != 0 {
		t.Fatalf("memory holds %d revocations after a failed write, want 0", n)
	}
	expectError(t, s.Admit(g, testRevokeAt, testReq), nil)

	if _, err := s.db.Exec(`DROP TRIGGER refuse_revocation`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	expectError(t, s.Revoke(ctx, g, testProvider, grant.ReasonCapacityReclaim, testRevokeAt), nil)
	expectError(t, s.Admit(g, testRevokeAt, testReq), grant.ErrGrantNotActive)
}

// A Store that has not seen another handle's revocation learns it when its own
// write is refused, and the refused write does not change the stored row.
func TestStaleHandleLearnsRevocationWhenItsWriteIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "grant-ledger.db")
	first := openStore(t, path)
	defer first.Close()
	second := openStore(t, path)
	defer second.Close()
	g := testGrant(t, "policy-a")

	if err := first.Revoke(ctx, g, testProvider, grant.ReasonCapacityReclaim, testRevokeAt); err != nil {
		t.Fatalf("first Revoke() error = %v", err)
	}
	expectError(t, second.Revoke(ctx, g, testProvider, grant.ReasonPolicyChange, testRevokeAt.Add(time.Hour)), grant.ErrAlreadyRevoked)
	expectError(t, second.Admit(g, testRevokeAt, testReq), grant.ErrGrantNotActive)

	if !sameExports(exportOf(t, first), exportOf(t, second)) {
		t.Fatalf("handles disagree after a refused write: first %+v, second %+v", exportOf(t, first), exportOf(t, second))
	}
	if n := rowCount(t, second); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

// A refused revocation changes neither memory nor the file.
func TestCoreRefusalWritesNothing(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "grant-ledger.db"))
	defer s.Close()
	g := testGrant(t, "policy-a")

	expectError(t, s.Revoke(ctx, g, testBeneficiary, grant.ReasonCapacityReclaim, testRevokeAt), grant.ErrRevokerMismatch)
	expectError(t, s.Revoke(ctx, g, testProvider, grant.ReasonCapacityReclaim, testIssuedAt.Add(-time.Second)), grant.ErrRevocationBeforeIssue)
	if n := rowCount(t, s); n != 0 {
		t.Fatalf("rows = %d, want 0", n)
	}
	if n := len(exportOf(t, s)); n != 0 {
		t.Fatalf("memory holds %d revocations, want 0", n)
	}
}

// Open must refuse a stored row that the core cannot accept. The checks are
// relaxed on the test connection only, so the bad row can be written at all.
func TestOpenRefusesCorruptStoredRows(t *testing.T) {
	g := testGrant(t, "policy-a")
	validDigest := g.Digest().Hex()
	cases := []struct {
		name         string
		digest       string
		effectiveSec int64
		effectiveNs  int64
		reason       string
	}{
		{name: "unknown reason", digest: validDigest, effectiveSec: testRevokeAt.Unix(), reason: "because-we-said-so"},
		{name: "effective before issuance", digest: validDigest, effectiveSec: testIssuedAt.Add(-time.Second).Unix(), reason: "capacity-reclaim"},
		{name: "uppercase digest", digest: strings.ToUpper(validDigest), effectiveSec: testRevokeAt.Unix(), reason: "capacity-reclaim"},
		{name: "nanoseconds out of range", digest: validDigest, effectiveSec: testRevokeAt.Unix(), effectiveNs: 1_000_000_000, reason: "capacity-reclaim"},
		{name: "zero effective time", digest: validDigest, effectiveSec: time.Time{}.Unix(), reason: "capacity-reclaim"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "grant-ledger.db")
			openStore(t, path).Close()

			db, err := sql.Open("sqlite", dsn(path))
			if err != nil {
				t.Fatalf("sql.Open() error = %v", err)
			}
			db.SetMaxOpenConns(1)
			if _, err := db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
				t.Fatalf("disable checks: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO grant_revocations
(grant_digest, issued_at_sec, issued_at_nsec, effective_at_sec, effective_at_nsec, reason)
VALUES (?, ?, 0, ?, ?, ?)`, tc.digest, testIssuedAt.Unix(), tc.effectiveSec, tc.effectiveNs, tc.reason); err != nil {
				t.Fatalf("insert corrupt row: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close writer: %v", err)
			}

			_, err = Open(context.Background(), path)
			expectError(t, err, ErrCorruptRecord)
		})
	}
}

// The schema refuses rows that break its own rules, even when the adapter is
// bypassed. The checks here are the ones the core does not repeat in SQL.
func TestSchemaRejectsInconsistentRowsEvenIfAdapterIsBypassed(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "grant-ledger.db"))
	defer s.Close()
	digest := testGrant(t, "policy-a").Digest().Hex()

	_, err := s.db.Exec(`INSERT INTO grant_revocations
(grant_digest, issued_at_sec, issued_at_nsec, effective_at_sec, effective_at_nsec, reason)
VALUES (?, ?, 0, ?, 0, 'capacity-reclaim')`, digest, testIssuedAt.Unix(), testIssuedAt.Add(-time.Second).Unix())
	if err == nil {
		t.Fatal("row with effective time before issuance inserted, want CHECK failure")
	}
	_, err = s.db.Exec(`INSERT INTO grant_revocations
(grant_digest, issued_at_sec, issued_at_nsec, effective_at_sec, effective_at_nsec, reason)
VALUES (?, ?, 0, ?, 0, 'capacity-reclaim')`, "abc", testIssuedAt.Unix(), testRevokeAt.Unix())
	if err == nil {
		t.Fatal("row with a short digest inserted, want CHECK failure")
	}
}

func TestClosedStoreRefusesCalls(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "grant-ledger.db"))
	g := testGrant(t, "policy-a")
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	expectError(t, s.Admit(g, testRevokeAt, testReq), ErrUnavailable)
	expectError(t, s.Revoke(ctx, g, testProvider, grant.ReasonCapacityReclaim, testRevokeAt), ErrUnavailable)
	_, err := s.PlanReclaim([]grant.Grant{g}, testRevokeAt, 0, grant.ReclaimConfig{Window: time.Hour, Slots: 4, MaxLeasesPerStop: 4})
	expectError(t, err, ErrUnavailable)
}

func TestPlanReclaimUsesRecordedRevocations(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "grant-ledger.db"))
	defer s.Close()
	a, b, c := testGrant(t, "policy-a"), testGrant(t, "policy-b"), testGrant(t, "policy-c")
	cfg := grant.ReclaimConfig{Window: time.Hour, Slots: 4, MaxLeasesPerStop: 4}

	expectError(t, s.Revoke(ctx, a, testProvider, grant.ReasonCapacityReclaim, testRevokeAt), nil)
	expectError(t, s.Revoke(ctx, b, testProvider, grant.ReasonCapacityReclaim, testRevokeAt), nil)

	plan, err := s.PlanReclaim([]grant.Grant{a, b}, testRevokeAt, 0, cfg)
	if err != nil || len(plan) != 2 {
		t.Fatalf("PlanReclaim() = %d assignments, error %v; want 2, nil", len(plan), err)
	}
	_, err = s.PlanReclaim([]grant.Grant{a, c}, testRevokeAt, 0, cfg)
	expectError(t, err, grant.ErrGrantNotRevoked)
}

func TestConnectionSettings(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "grant-ledger.db"))
	defer s.Close()

	var journal string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("PRAGMA journal_mode error = %v", err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journal)
	}

	var syncMode int
	if err := s.db.QueryRow(`PRAGMA synchronous`).Scan(&syncMode); err != nil {
		t.Fatalf("PRAGMA synchronous error = %v", err)
	}
	if syncMode != 2 { // 2 is FULL
		t.Fatalf("synchronous = %d, want 2 (FULL)", syncMode)
	}

	var busy int
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatalf("PRAGMA busy_timeout error = %v", err)
	}
	if busy != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busy)
	}
}

func TestSchemaVersionIsRecorded(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "grant-ledger.db"))
	defer s.Close()

	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d", version, len(migrations))
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grant-ledger.db")
	s := openStore(t, path)
	if _, err := s.db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err := Open(context.Background(), path)
	expectError(t, err, ErrUnsupportedSchema)
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("Open(\"\") error = nil, want error")
	}
}

func TestDatabaseFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "grant-ledger.db")
	s := openStore(t, path)
	defer s.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("database file mode = %o, want no group/other access", perm)
	}
}
