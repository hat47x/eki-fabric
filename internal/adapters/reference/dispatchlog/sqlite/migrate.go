package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations[i] upgrades the schema from version i to version i+1. The applied
// version is kept in PRAGMA user_version. Migrations only ever append; a shipped
// migration is never edited.
var migrations = []string{
	// 1: one row per effect, holding the highest generation recorded for it.
	`CREATE TABLE dispatch_attempts (
	effect_id      TEXT    NOT NULL PRIMARY KEY,
	generation     INTEGER NOT NULL CHECK (generation >= 1),
	recorded_at_ns INTEGER NOT NULL
) STRICT`,
}

// migrate brings the database up to the version given by len(steps). It is
// idempotent, rejects a database written by a newer schema, and applies each
// step in its own transaction, so a failed step leaves the previous version in
// place.
func migrate(ctx context.Context, db *sql.DB, steps []string) error {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read dispatch log schema version: %w", err)
	}
	if version > len(steps) {
		return fmt.Errorf("%w: database is at version %d, this adapter supports up to %d", ErrUnsupportedSchema, version, len(steps))
	}

	for v := version; v < len(steps); v++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin schema migration to version %d: %w", v+1, err)
		}
		// Another process may have applied this step between the read above and
		// the write lock taken by BeginTx (the DSN uses _txlock=immediate). Read
		// the version again under the lock and skip a step that is already
		// recorded, so two processes opening an old database do not both run it.
		var current int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("re-read schema version before migration to version %d: %w", v+1, err)
		}
		if current > len(steps) {
			_ = tx.Rollback()
			return fmt.Errorf("%w: database is at version %d, this adapter supports up to %d", ErrUnsupportedSchema, current, len(steps))
		}
		if current > v {
			if err := tx.Rollback(); err != nil {
				return fmt.Errorf("release schema migration to version %d: %w", v+1, err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, steps[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply schema migration to version %d: %w", v+1, err)
		}
		// PRAGMA does not accept bound parameters. The value is an integer computed
		// here, never client input.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record schema version %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schema migration to version %d: %w", v+1, err)
		}
	}
	return nil
}
