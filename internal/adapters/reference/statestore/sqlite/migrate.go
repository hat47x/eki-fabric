package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// migrations[i] upgrades the schema from version i to version i+1. The
// applied version is kept in PRAGMA user_version. Migrations only ever append;
// a shipped migration is never edited.
var migrations = []string{
	// 1: tasks
	`CREATE TABLE tasks (
	task_id                TEXT    NOT NULL PRIMARY KEY,
	task_ir_digest         TEXT    NOT NULL CHECK (length(task_ir_digest) = 64),
	phase                  TEXT    NOT NULL CHECK (phase IN ('admitted','ready','active','verifying','reconciling','held','terminal')),
	terminal_outcome       TEXT    NOT NULL CHECK (terminal_outcome IN ('','completed','cancelled','failed','exhausted','abandoned')),
	cancellation_requested INTEGER NOT NULL CHECK (cancellation_requested IN (0,1)),
	revision               INTEGER NOT NULL CHECK (revision >= 1),
	created_at_ns          INTEGER NOT NULL,
	updated_at_ns          INTEGER NOT NULL CHECK (updated_at_ns >= created_at_ns),
	CHECK ((phase = 'terminal') = (terminal_outcome <> ''))
) STRICT`,
}

// migrate brings the database to the latest version. It is idempotent, rejects
// a database written by a newer schema, and applies each step in its own
// transaction so a failed step leaves the previous version intact.
func migrate(ctx context.Context, db *sql.DB, steps []string) error {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read state store schema version: %w", err)
	}
	if version > len(steps) {
		return fmt.Errorf("%w: database is at version %d, this adapter supports up to %d", ErrUnsupportedSchema, version, len(steps))
	}

	for v := version; v < len(steps); v++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin schema migration to version %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, steps[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply schema migration to version %d: %w", v+1, err)
		}
		// PRAGMA does not accept bound parameters; the value is an integer we
		// computed, never client input.
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

// filepathToURI converts a filesystem path into the opaque part of a SQLite
// "file:" URI. SQLite needs a leading slash for Windows drive paths, and
// reserved characters must not be read as the start of the query string.
func filepathToURI(path string) string {
	p := filepath.ToSlash(path)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p
	}
	r := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")
	return r.Replace(p)
}
