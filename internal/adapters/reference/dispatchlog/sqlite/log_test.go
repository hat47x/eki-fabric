package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/dispatchlogtest"
	"github.com/hat47x/eki-fabric/internal/core/dispatchguard"
	"github.com/hat47x/eki-fabric/internal/spi/dispatchlog"
)

func openLog(t testing.TB, path string) *Log {
	t.Helper()
	log, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	return log
}

func mustEffectID(t testing.TB, raw string) dispatchlog.EffectID {
	t.Helper()
	id, err := dispatchlog.ParseEffectID(raw)
	if err != nil {
		t.Fatalf("ParseEffectID(%q) error = %v", raw, err)
	}
	return id
}

func assertLatest(t testing.TB, log *Log, effect dispatchlog.EffectID, want uint64, wantOK bool) {
	t.Helper()
	got, ok, err := log.Latest(context.Background(), effect)
	if err != nil {
		t.Fatalf("Latest(%s) error = %v", effect, err)
	}
	if got != want || ok != wantOK {
		t.Fatalf("Latest(%s) = (%d, %t), want (%d, %t)", effect, got, ok, want, wantOK)
	}
}

func TestLogContract(t *testing.T) {
	dispatchlogtest.RunContract(t, func(t testing.TB) dispatchlog.Log {
		t.Helper()
		log := openLog(t, filepath.Join(t.TempDir(), "dispatch.db"))
		t.Cleanup(func() {
			if err := log.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		})
		return log
	})
}

func TestLogDurabilityContract(t *testing.T) {
	dispatchlogtest.RunDurabilityContract(t, dispatchlogtest.DurableFactory{
		Open: func(t testing.TB, location string) (dispatchlog.Log, func() error) {
			t.Helper()
			log := openLog(t, location)
			return log, log.Close
		},
	})
}

// A record must be in the database file before RecordAttempt returns. An
// independent connection must already see it while the first store is open,
// and it must survive a close and reopen with no extra sync or checkpoint.
func TestRecordIsCommittedBeforeRecordAttemptReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.db")
	ctx := context.Background()
	effect := mustEffectID(t, "durable-before-return")

	log := openLog(t, path)
	if err := log.RecordAttempt(ctx, effect, 7); err != nil {
		t.Fatalf("RecordAttempt() error = %v", err)
	}

	other, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	defer other.Close()
	var seen int64
	if err := other.QueryRowContext(ctx, `SELECT generation FROM dispatch_attempts WHERE effect_id = ?`, string(effect)).Scan(&seen); err != nil {
		t.Fatalf("read through second connection: %v", err)
	}
	if seen != 7 {
		t.Fatalf("second connection sees generation %d, want 7", seen)
	}

	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened := openLog(t, path)
	defer reopened.Close()
	assertLatest(t, reopened, effect, 7, true)
}

func TestConnectionSettings(t *testing.T) {
	log := openLog(t, filepath.Join(t.TempDir(), "dispatch.db"))
	defer log.Close()

	var journal string
	if err := log.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("PRAGMA journal_mode error = %v", err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journal)
	}

	var syncMode int
	if err := log.db.QueryRow(`PRAGMA synchronous`).Scan(&syncMode); err != nil {
		t.Fatalf("PRAGMA synchronous error = %v", err)
	}
	if syncMode != 2 { // 2 is FULL
		t.Fatalf("synchronous = %d, want 2 (FULL)", syncMode)
	}

	var foreignKeys int
	if err := log.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys error = %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	var busy int
	if err := log.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatalf("PRAGMA busy_timeout error = %v", err)
	}
	if busy != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busy)
	}
}

func TestSchemaVersionIsRecorded(t *testing.T) {
	log := openLog(t, filepath.Join(t.TempDir(), "dispatch.db"))
	defer log.Close()

	var version int
	if err := log.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d", version, len(migrations))
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.db")
	log := openLog(t, path)
	if _, err := log.db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err := Open(context.Background(), path)
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Open() error = %v, want ErrUnsupportedSchema", err)
	}
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("Open(\"\") error = nil, want error")
	}
}

func TestFailedMigrationRollsBackAndKeepsPreviousVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.db")
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

func TestSchemaRejectsNonPositiveGenerationEvenIfAdapterIsBypassed(t *testing.T) {
	log := openLog(t, filepath.Join(t.TempDir(), "dispatch.db"))
	defer log.Close()

	if _, err := log.db.Exec(`INSERT INTO dispatch_attempts (effect_id, generation, recorded_at_ns) VALUES ('zero', 0, 1)`); err == nil {
		t.Fatal("generation 0 inserted through direct SQL, want CHECK failure")
	}
}

// The upsert statement must change no row for an equal or lower generation,
// even when called directly, so the rule holds in SQL and not only in Go.
func TestRecordStatementChangesNothingForNonIncreasingGeneration(t *testing.T) {
	log := openLog(t, filepath.Join(t.TempDir(), "dispatch.db"))
	defer log.Close()
	ctx := context.Background()
	effect := mustEffectID(t, "sql-guard")

	if err := log.RecordAttempt(ctx, effect, 5); err != nil {
		t.Fatalf("RecordAttempt(5) error = %v", err)
	}
	for _, generation := range []int64{5, 4, 1} {
		res, err := log.db.ExecContext(ctx, recordAttemptSQL, string(effect), generation, time.Now().UnixNano())
		if err != nil {
			t.Fatalf("statement for generation %d error = %v", generation, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			t.Fatalf("RowsAffected() error = %v", err)
		}
		if n != 0 {
			t.Fatalf("statement for generation %d changed %d rows, want 0", generation, n)
		}
	}
	assertLatest(t, log, effect, 5, true)

	res, err := log.db.ExecContext(ctx, recordAttemptSQL, string(effect), 6, time.Now().UnixNano())
	if err != nil {
		t.Fatalf("statement for generation 6 error = %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("statement for generation 6 rows = %d, err = %v, want 1", n, err)
	}
	assertLatest(t, log, effect, 6, true)
}

// The guard sends only after the record is visible, and a repeated generation
// is refused without a second call.
func TestGuardSendsOnlyAfterRecordIsVisible(t *testing.T) {
	ctx := context.Background()
	log := openLog(t, filepath.Join(t.TempDir(), "dispatch.db"))
	defer log.Close()

	guard, err := dispatchguard.New(log)
	if err != nil {
		t.Fatalf("dispatchguard.New() error = %v", err)
	}
	effect := mustEffectID(t, "guard-sqlite")
	sends := 0

	err = guard.Send(ctx, effect, 1, func(ctx context.Context) error {
		sends++
		generation, ok, err := log.Latest(ctx, effect)
		if err != nil {
			return err
		}
		if !ok || generation != 1 {
			return errors.New("record is not visible inside send")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("first Send() error = %v", err)
	}

	err = guard.Send(ctx, effect, 1, func(context.Context) error {
		sends++
		return nil
	})
	if !errors.Is(err, dispatchlog.ErrAlreadyRecorded) {
		t.Fatalf("second Send() error = %v, want ErrAlreadyRecorded", err)
	}
	if sends != 1 {
		t.Fatalf("send was called %d times, want 1", sends)
	}
}

func TestDatabaseFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "dispatch.db")
	log := openLog(t, path)
	defer log.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("database file mode = %o, want no group/other access", perm)
	}
}
