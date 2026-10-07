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
	// 2: admissions
	`CREATE TABLE admissions (
	task_id             TEXT    NOT NULL PRIMARY KEY REFERENCES tasks (task_id),
	principal           TEXT    NOT NULL CHECK (length(principal) BETWEEN 1 AND 256),
	idempotency_key     TEXT    NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
	request_fingerprint TEXT    NOT NULL CHECK (length(request_fingerprint) = 64),
	max_attempts        INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 32),
	UNIQUE (principal, idempotency_key)
) STRICT;
CREATE INDEX admissions_by_principal ON admissions (principal)`,
	// 3: durable lease reservations / fencing epochs
	`CREATE TABLE lease_fences (
	task_id          TEXT    NOT NULL PRIMARY KEY REFERENCES tasks (task_id),
	last_fence_epoch INTEGER NOT NULL CHECK (last_fence_epoch >= 1)
) STRICT;
CREATE TABLE leases (
	lease_id                      TEXT    NOT NULL PRIMARY KEY,
	task_id                       TEXT    NOT NULL REFERENCES tasks (task_id),
	task_ir_digest                TEXT    NOT NULL CHECK (length(task_ir_digest) = 64),
	attempt_id                    TEXT    NOT NULL,
	worker_identity               TEXT    NOT NULL CHECK (length(worker_identity) BETWEEN 1 AND 256),
	placement_authority_principal TEXT    NOT NULL CHECK (length(placement_authority_principal) BETWEEN 1 AND 256),
	placement_decision_id         TEXT    NOT NULL CHECK (length(placement_decision_id) BETWEEN 1 AND 128),
	placement_payload_digest      TEXT    NOT NULL CHECK (length(placement_payload_digest) = 64),
	placement_expires_at_ns       INTEGER NOT NULL,
	policy_revision               TEXT    NOT NULL CHECK (length(policy_revision) BETWEEN 1 AND 128),
	fence_epoch                   INTEGER NOT NULL CHECK (fence_epoch >= 1),
	renewal_sequence              INTEGER NOT NULL CHECK (renewal_sequence >= 0),
	issued_at_ns                  INTEGER NOT NULL,
	expires_at_ns                 INTEGER NOT NULL CHECK (expires_at_ns > issued_at_ns),
	CHECK (placement_expires_at_ns >= expires_at_ns),
	UNIQUE (placement_authority_principal, placement_decision_id),
	UNIQUE (task_id, fence_epoch)
) STRICT;
CREATE INDEX leases_by_task_expiry ON leases (task_id, expires_at_ns)`,
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
