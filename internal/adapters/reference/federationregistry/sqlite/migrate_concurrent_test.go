package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// concurrentMigrationSteps records each step in a table, so a test can count
// how many times a step ran. CREATE TABLE has no IF NOT EXISTS, as in the real
// steps: running the first step twice fails.
var concurrentMigrationSteps = []string{
	`CREATE TABLE applied (step INTEGER NOT NULL)`,
	`INSERT INTO applied (step) VALUES (2)`,
	`INSERT INTO applied (step) VALUES (3)`,
}

func openRawForMigration(t testing.TB, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func assertEachStepAppliedOnce(t testing.TB, db *sql.DB) {
	t.Helper()
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != len(concurrentMigrationSteps) {
		t.Fatalf("user_version = %d, want %d", version, len(concurrentMigrationSteps))
	}
	rows, err := db.Query(`SELECT step FROM applied ORDER BY step`)
	if err != nil {
		t.Fatalf("read applied steps: %v", err)
	}
	defer rows.Close()
	var got []int
	for rows.Next() {
		var step int
		if err := rows.Scan(&step); err != nil {
			t.Fatalf("scan applied step: %v", err)
		}
		got = append(got, step)
	}
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("applied steps = %v, want [2 3] (each step once)", got)
	}
}

// TestMigrateSkipsStepsAppliedByAnotherProcessAfterTheVersionRead reproduces
// the window between the version read and the write lock: a second connection
// reads version 0, waits for the lock, and must not run the steps the first
// connection committed in the meantime.
func TestMigrateSkipsStepsAppliedByAnotherProcessAfterTheVersionRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrate.db")
	ctx := context.Background()
	first := openRawForMigration(t, path)
	second := openRawForMigration(t, path)

	// The first connection takes the write lock and holds it.
	tx, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- migrate(ctx, second, concurrentMigrationSteps) }()

	// Give the second connection time to read version 0 and block on the lock.
	// A shorter wait only makes the test less likely to catch a regression.
	time.Sleep(300 * time.Millisecond)

	for _, step := range concurrentMigrationSteps {
		if _, err := tx.ExecContext(ctx, step); err != nil {
			t.Fatalf("apply step: %v", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA user_version = 3`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("migrate() on a database another process already migrated error = %v", err)
	}
	assertEachStepAppliedOnce(t, second)
}

func TestMigrateConcurrentOpensApplyEachStepOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrate.db")
	ctx := context.Background()
	const openers = 6
	dbs := make([]*sql.DB, openers)
	for i := range dbs {
		dbs[i] = openRawForMigration(t, path)
		if i == 0 {
			// A database written by an older binary is already in WAL mode.
			// Connect once first, so the concurrent part below starts from that
			// state and exercises only the schema steps. Switching a brand-new
			// file to WAL from several connections at once is a separate matter.
			if err := dbs[0].PingContext(ctx); err != nil {
				t.Fatalf("PingContext() error = %v", err)
			}
		}
	}

	start := make(chan struct{})
	errs := make(chan error, openers)
	var wg sync.WaitGroup
	for _, db := range dbs {
		wg.Add(1)
		go func(db *sql.DB) {
			defer wg.Done()
			<-start
			errs <- migrate(ctx, db, concurrentMigrationSteps)
		}(db)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate() error = %v", err)
		}
	}
	assertEachStepAppliedOnce(t, dbs[0])
}
