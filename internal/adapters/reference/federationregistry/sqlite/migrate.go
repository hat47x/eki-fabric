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
	// 1: the federation state. federation_domains holds the link and the installed
	// bundle of each trust domain, and the revocation. federation_sequence_guard
	// holds the highest bundle sequence ever accepted, in its own row, so that a
	// rewind of the bundle row alone is visible when the state is restored.
	// Purposes and keys are child rows.
	`CREATE TABLE federation_domains (
	domain              TEXT    NOT NULL PRIMARY KEY,
	authority_key       BLOB    NOT NULL UNIQUE CHECK (length(authority_key) = 32),
	bundle_sequence     INTEGER NOT NULL CHECK (bundle_sequence >= 1),
	bundle_validated_at TEXT    NOT NULL,
	bundle_signature    BLOB    NOT NULL CHECK (length(bundle_signature) = 64),
	revoked             INTEGER NOT NULL CHECK (revoked IN (0, 1)),
	revoked_at          TEXT,
	CHECK ((revoked = 0 AND revoked_at IS NULL) OR (revoked = 1 AND revoked_at IS NOT NULL))
) STRICT;
CREATE TABLE federation_sequence_guard (
	domain           TEXT    NOT NULL PRIMARY KEY REFERENCES federation_domains (domain),
	highest_sequence INTEGER NOT NULL CHECK (highest_sequence >= 1)
) STRICT;
CREATE TABLE federation_link_purposes (
	domain  TEXT NOT NULL REFERENCES federation_domains (domain),
	purpose TEXT NOT NULL CHECK (purpose IN ('task-submission', 'executor-participation', 'data-class-access', 'compute-grant-use')),
	PRIMARY KEY (domain, purpose)
) STRICT;
CREATE TABLE federation_bundle_keys (
	domain     TEXT NOT NULL REFERENCES federation_domains (domain),
	key_id     TEXT NOT NULL CHECK (length(key_id) BETWEEN 1 AND 128),
	public_key BLOB NOT NULL CHECK (length(public_key) = 32),
	PRIMARY KEY (domain, key_id),
	UNIQUE (domain, public_key)
) STRICT;
CREATE TRIGGER federation_bundle_sequence_monotonic
BEFORE UPDATE OF bundle_sequence ON federation_domains
WHEN NEW.bundle_sequence < OLD.bundle_sequence
BEGIN
	SELECT RAISE(ABORT, 'federation bundle sequence must not decrease');
END;
CREATE TRIGGER federation_highest_sequence_monotonic
BEFORE UPDATE OF highest_sequence ON federation_sequence_guard
WHEN NEW.highest_sequence < OLD.highest_sequence
BEGIN
	SELECT RAISE(ABORT, 'federation highest sequence must not decrease');
END;
CREATE TRIGGER federation_revocation_is_final
BEFORE UPDATE OF revoked, revoked_at ON federation_domains
WHEN OLD.revoked = 1 AND (NEW.revoked <> 1 OR NEW.revoked_at IS NOT OLD.revoked_at)
BEGIN
	SELECT RAISE(ABORT, 'federation revocation must not be undone or changed');
END;
CREATE TRIGGER federation_authority_key_is_fixed
BEFORE UPDATE OF authority_key ON federation_domains
WHEN NEW.authority_key IS NOT OLD.authority_key
BEGIN
	SELECT RAISE(ABORT, 'federation authority key is fixed at federation time');
END`,
}

// migrate brings the database up to the version given by len(steps). It is
// idempotent, rejects a database written by a newer schema, and applies each
// step in its own transaction, so a failed step leaves the previous version in
// place.
func migrate(ctx context.Context, db *sql.DB, steps []string) error {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read federation registry schema version: %w", err)
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
