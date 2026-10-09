package sqlite

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/federationregistrytest"
	"github.com/hat47x/eki-fabric/internal/core/federation"
)

var testNow = time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)

// testAuthority is a trust domain with its bundle authority key pair.
type testAuthority struct {
	domain federation.TrustDomain
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
}

func testKey(seed byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	return priv.Public().(ed25519.PublicKey), priv
}

func newTestAuthority(t testing.TB, name string, seed byte) testAuthority {
	t.Helper()
	domain, err := federation.ParseTrustDomain(name)
	mustNil(t, "ParseTrustDomain", err)
	pub, priv := testKey(seed)
	return testAuthority{domain: domain, pub: pub, priv: priv}
}

// bundle returns a bundle of the authority and its authority signature.
func (a testAuthority) bundle(t testing.TB, sequence uint64, validatedAt time.Time, keys ...federation.BundleKey) (federation.Bundle, []byte) {
	t.Helper()
	b, err := federation.NewBundle(a.domain, sequence, validatedAt, keys)
	mustNil(t, "NewBundle", err)
	input, err := federation.BundleSigningInput(b)
	mustNil(t, "BundleSigningInput", err)
	return b, ed25519.Sign(a.priv, input)
}

func (a testAuthority) link(t testing.TB, purposes ...federation.Purpose) federation.Link {
	t.Helper()
	link, err := federation.NewLink(a.domain, a.pub, purposes...)
	mustNil(t, "NewLink", err)
	return link
}

var (
	k1Pub, k1Priv = testKey(0x11)
	k2Pub, k2Priv = testKey(0x12)
	outsiderPub, _ = testKey(0x13)
)

func key1() federation.BundleKey { return federation.BundleKey{ID: "k1", PublicKey: k1Pub} }
func key2() federation.BundleKey { return federation.BundleKey{ID: "k2", PublicKey: k2Pub} }

func openStore(t testing.TB, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path, federationregistrytest.MaxBundleAge)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	return s
}

func mustNil(t testing.TB, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// seedAlpha federates alpha with k1 and k2 at sequence 1.
func seedAlpha(t testing.TB, s *Store, a testAuthority) {
	t.Helper()
	b, sig := a.bundle(t, 1, testNow.Add(-time.Hour), key1(), key2())
	mustNil(t, "Federate(alpha)", s.Federate(a.link(t, federation.PurposeTaskSubmission), b, sig))
}

// seedMoved also moves alpha to sequence 2, which is bound with k1 alone.
func seedMoved(t testing.TB, s *Store, a testAuthority) {
	t.Helper()
	seedAlpha(t, s, a)
	b, sig := a.bundle(t, 2, testNow.Add(-30*time.Minute), key1())
	mustNil(t, "InstallBundle(alpha, 2)", s.InstallBundle(b, sig))
}

// faultyStore adds failure injection to Store. The failures are SQL triggers that
// abort every write to the registry tables. Reads are not affected.
type faultyStore struct {
	*Store
}

var failingTables = []string{
	"federation_domains",
	"federation_sequence_guard",
	"federation_link_purposes",
	"federation_bundle_keys",
}

var failingOps = []string{"INSERT", "UPDATE", "DELETE"}

func (f faultyStore) FailWrites(t testing.TB) {
	t.Helper()
	for _, table := range failingTables {
		for _, op := range failingOps {
			stmt := "CREATE TRIGGER fail_" + op + "_" + table + " BEFORE " + op + " ON " + table +
				" BEGIN SELECT RAISE(ABORT, 'injected write failure'); END"
			if _, err := f.db.Exec(stmt); err != nil {
				t.Fatalf("install write failure on %s: %v", table, err)
			}
		}
	}
}

func (f faultyStore) RestoreWrites(t testing.TB) {
	t.Helper()
	for _, table := range failingTables {
		for _, op := range failingOps {
			if _, err := f.db.Exec("DROP TRIGGER IF EXISTS fail_" + op + "_" + table); err != nil {
				t.Fatalf("remove write failure on %s: %v", table, err)
			}
		}
	}
}

func TestStoreDurabilityContract(t *testing.T) {
	federationregistrytest.RunDurabilityContract(t, federationregistrytest.DurableFactory{
		Open: func(t testing.TB, location string) (federationregistrytest.Registry, func() error) {
			s := openStore(t, location)
			return s, s.Close
		},
	})
}

func TestStorePersistFailureContract(t *testing.T) {
	federationregistrytest.RunPersistFailureContract(t, federationregistrytest.FaultyFactory{
		Open: func(t testing.TB, location string) federationregistrytest.Faulty {
			return faultyStore{openStore(t, location)}
		},
	})
}

// TestOpenRefusesTamperedRows changes the stored rows in ways that the schema
// alone does not forbid, and checks that Open refuses the stored state.
func TestOpenRefusesTamperedRows(t *testing.T) {
	a := newTestAuthority(t, "alpha.example", 0xA1)
	outsider := outsiderPub

	type statement struct {
		sql  string
		args []any
	}
	cases := map[string]struct {
		statements []statement
		want       error // nil means any refusal
	}{
		"authority signature replaced": {
			[]statement{{`UPDATE federation_domains SET bundle_signature = zeroblob(64)`, nil}},
			federation.ErrBundleSignature,
		},
		"stored key replaced by another valid key": {
			[]statement{{`UPDATE federation_bundle_keys SET public_key = ? WHERE key_id = 'k1'`, []any{[]byte(outsider)}}},
			federation.ErrBundleSignature,
		},
		"stored key row removed": {
			[]statement{{`DELETE FROM federation_bundle_keys WHERE key_id = 'k1'`, nil}},
			federation.ErrInvalidBundle,
		},
		"guard row ahead of the installed bundle": {
			[]statement{{`UPDATE federation_sequence_guard SET highest_sequence = 3`, nil}},
			federation.ErrRollback,
		},
		"guard row removed": {
			[]statement{{`DELETE FROM federation_sequence_guard`, nil}},
			nil,
		},
		"bundle row rewound with its trigger removed": {
			[]statement{
				{`DROP TRIGGER federation_bundle_sequence_monotonic`, nil},
				{`UPDATE federation_domains SET bundle_sequence = 1`, nil},
			},
			federation.ErrBundleSignature,
		},
		"pinned authority key replaced with its trigger removed": {
			[]statement{
				{`DROP TRIGGER federation_authority_key_is_fixed`, nil},
				{`UPDATE federation_domains SET authority_key = ?`, []any{[]byte(outsider)}},
			},
			federation.ErrBundleSignature,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "federation.db")
			s := openStore(t, path)
			seedMoved(t, s, a)
			for _, st := range tc.statements {
				if _, err := s.db.Exec(st.sql, st.args...); err != nil {
					t.Fatalf("tamper %q: %v", st.sql, err)
				}
			}
			mustNil(t, "close", s.Close())

			reopened, err := Open(context.Background(), path, federationregistrytest.MaxBundleAge)
			if err == nil {
				_ = reopened.Close()
				t.Fatal("Open accepted tampered rows")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Open error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestSchemaRefusesDirectInvalidWrites checks that the schema rejects writes that
// would break the rules, even when they bypass the Store.
func TestSchemaRefusesDirectInvalidWrites(t *testing.T) {
	a := newTestAuthority(t, "alpha.example", 0xA1)
	s := openStore(t, filepath.Join(t.TempDir(), "federation.db"))
	defer func() { _ = s.Close() }()
	seedMoved(t, s, a)
	mustNil(t, "Revoke(alpha)", s.Revoke(a.domain, testNow.Add(-time.Minute)))

	cases := map[string]struct {
		sql  string
		args []any
	}{
		"bundle sequence rewound":        {`UPDATE federation_domains SET bundle_sequence = 1`, nil},
		"highest sequence rewound":       {`UPDATE federation_sequence_guard SET highest_sequence = 1`, nil},
		"authority key changed":          {`UPDATE federation_domains SET authority_key = ?`, []any{[]byte(outsiderPub)}},
		"revocation undone":              {`UPDATE federation_domains SET revoked = 0, revoked_at = NULL`, nil},
		"revocation without a time":      {`UPDATE federation_domains SET revoked_at = NULL`, nil},
		"purpose outside the vocabulary": {`INSERT INTO federation_link_purposes (domain, purpose) VALUES ('alpha.example', 'wildcard')`, nil},
		"empty key ID":                   {`UPDATE federation_bundle_keys SET key_id = ''`, nil},
		"short authority key":            {`UPDATE federation_domains SET authority_key = zeroblob(31)`, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.db.Exec(tc.sql, tc.args...); err == nil {
				t.Fatalf("%q was accepted", tc.sql)
			}
		})
	}
}

// TestStaleStoreCannotOverwriteNewerChange opens two stores on one file. The stale
// one still believes that sequence 1 is bound, so the core accepts a sequence 2 that
// differs from the stored one. The compare-and-set update must refuse that write,
// and the stale store must reload the stored bundle.
func TestStaleStoreCannotOverwriteNewerChange(t *testing.T) {
	a := newTestAuthority(t, "alpha.example", 0xA1)
	path := filepath.Join(t.TempDir(), "federation.db")

	writer := openStore(t, path)
	defer func() { _ = writer.Close() }()
	seedAlpha(t, writer, a)

	stale := openStore(t, path)
	defer func() { _ = stale.Close() }()

	stored, sig := a.bundle(t, 2, testNow.Add(-time.Minute), key1())
	mustNil(t, "InstallBundle on writer", writer.InstallBundle(stored, sig))

	other, otherSig := a.bundle(t, 2, testNow.Add(-time.Minute), key2())
	if err := stale.InstallBundle(other, otherSig); err == nil {
		t.Fatal("stale store overwrote the newer bundle")
	}
	b, err := stale.Binding(a.domain)
	mustNil(t, "Binding after reload", err)
	if b.Digest() != stored.Digest() {
		t.Fatalf("stale store shows digest %s after reload, want the stored digest %s", b.Digest(), stored.Digest())
	}
}

// TestStoreFailsClosedWhenStorageCannotBeReloaded removes a table, so that both the
// write and the reload that follows it fail. The Store must then refuse every call.
func TestStoreFailsClosedWhenStorageCannotBeReloaded(t *testing.T) {
	a := newTestAuthority(t, "alpha.example", 0xA1)
	s := openStore(t, filepath.Join(t.TempDir(), "federation.db"))
	defer func() { _ = s.Close() }()
	seedMoved(t, s, a)

	if _, err := s.db.Exec(`DROP TABLE federation_bundle_keys`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	next, sig := a.bundle(t, 3, testNow.Add(-time.Minute), key1())
	if err := s.InstallBundle(next, sig); err == nil {
		t.Fatal("InstallBundle succeeded although storage is unreadable")
	}
	if _, err := s.Binding(a.domain); err == nil {
		t.Fatal("Binding answered after the Store lost its storage")
	}
	if _, err := s.Verify(federation.Identity{}, federation.PurposeTaskSubmission, testNow); err == nil {
		t.Fatal("Verify answered after the Store lost its storage")
	}
}

func TestClosedStoreRefusesCalls(t *testing.T) {
	a := newTestAuthority(t, "alpha.example", 0xA1)
	s := openStore(t, filepath.Join(t.TempDir(), "federation.db"))
	seedAlpha(t, s, a)
	mustNil(t, "close", s.Close())
	mustNil(t, "second close", s.Close())

	if _, err := s.Binding(a.domain); !errors.Is(err, errClosed) {
		t.Fatalf("Binding after Close error = %v, want errClosed", err)
	}
	b, sig := a.bundle(t, 2, testNow, key1())
	if err := s.InstallBundle(b, sig); !errors.Is(err, errClosed) {
		t.Fatalf("InstallBundle after Close error = %v, want errClosed", err)
	}
}

func TestSchemaVersionAndConnectionSettings(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "federation.db"))
	defer func() { _ = s.Close() }()

	var journal string
	mustNil(t, "journal_mode", s.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal))
	if journal != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journal)
	}
	var syncMode int
	mustNil(t, "synchronous", s.db.QueryRow(`PRAGMA synchronous`).Scan(&syncMode))
	if syncMode != 2 {
		t.Fatalf("synchronous = %d, want 2 (FULL)", syncMode)
	}
	var fk int
	mustNil(t, "foreign_keys", s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk))
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}
	var version int
	mustNil(t, "user_version", s.db.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d", version, len(migrations))
	}
}

func TestOpenRejectsBadArgumentsAndNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "federation.db")

	if _, err := Open(ctx, "", federationregistrytest.MaxBundleAge); err == nil {
		t.Fatal("Open accepted an empty path")
	}
	if _, err := Open(ctx, path, 0); !errors.Is(err, federation.ErrInvalidMaxBundleAge) {
		t.Fatalf("Open with zero max age error = %v, want ErrInvalidMaxBundleAge", err)
	}

	s := openStore(t, path)
	if _, err := s.db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatalf("set version: %v", err)
	}
	mustNil(t, "close", s.Close())
	if _, err := Open(ctx, path, federationregistrytest.MaxBundleAge); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Open with newer schema error = %v, want ErrUnsupportedSchema", err)
	}
}
