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
	placement_trust_domain        TEXT    NOT NULL CHECK (length(placement_trust_domain) BETWEEN 1 AND 256),
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
	UNIQUE (placement_trust_domain, placement_authority_principal, placement_decision_id),
	UNIQUE (task_id, fence_epoch)
) STRICT;
CREATE INDEX leases_by_task_expiry ON leases (task_id, expires_at_ns)`,
	// 4: durable Attempt lifecycle state
	`CREATE TABLE attempts (
	attempt_id       TEXT    NOT NULL PRIMARY KEY,
	task_id          TEXT    NOT NULL REFERENCES tasks (task_id),
	task_ir_digest   TEXT    NOT NULL CHECK (length(task_ir_digest) = 64),
	attempt_number   INTEGER NOT NULL CHECK (attempt_number >= 1),
	phase            TEXT    NOT NULL CHECK (phase IN ('created','placing','assigned','dispatched','claimed','verifying','closed')),
	worker_identity  TEXT    NOT NULL CHECK (length(worker_identity) <= 256),
	lease_id         TEXT,
	close_reason     TEXT    NOT NULL CHECK (close_reason IN ('','result_accepted','result_rejected','execution_failure_confirmed','worker_lost','lease_expired','reclaimed','cancelled','superseded','deadline','budget_exhausted','unknown_external_effect','policy_revoked','stale_attempt')),
	revision         INTEGER NOT NULL CHECK (revision >= 1),
	created_at_ns    INTEGER NOT NULL,
	updated_at_ns    INTEGER NOT NULL CHECK (updated_at_ns >= created_at_ns),
	UNIQUE (task_id, attempt_number),
	CHECK (phase <> 'created' OR (worker_identity = '' AND lease_id IS NULL)),
	CHECK (phase <> 'placing' OR (worker_identity <> '' AND lease_id IS NULL)),
	CHECK (phase NOT IN ('assigned','dispatched','claimed','verifying') OR (worker_identity <> '' AND lease_id IS NOT NULL)),
	CHECK (phase <> 'closed' OR close_reason <> ''),
	CHECK (phase = 'closed' OR close_reason = ''),
	CHECK (lease_id IS NULL OR worker_identity <> '')
) STRICT;
CREATE INDEX attempts_by_task_phase ON attempts (task_id, phase);
CREATE TRIGGER attempts_insert_guard
BEFORE INSERT ON attempts
WHEN NEW.phase <> 'created'
  OR NEW.revision <> 1
  OR NOT EXISTS (
	SELECT 1 FROM tasks
	WHERE task_id = NEW.task_id
	  AND task_ir_digest = NEW.task_ir_digest
	  AND cancellation_requested = 0
	  AND phase IN ('ready','active')
  )
BEGIN
	SELECT RAISE(ABORT, 'attempt is not a valid initial state for current Task');
END;
CREATE TRIGGER leases_attempt_insert
BEFORE INSERT ON leases
WHEN NOT EXISTS (
	SELECT 1 FROM attempts
	WHERE attempt_id = NEW.attempt_id
	  AND task_id = NEW.task_id
	  AND task_ir_digest = NEW.task_ir_digest
	  AND worker_identity = NEW.worker_identity
	  AND phase = 'placing'
	  AND lease_id IS NULL
)
BEGIN
	SELECT RAISE(ABORT, 'lease does not bind a current placing Attempt');
END;
CREATE TRIGGER leases_attempt_assign
AFTER INSERT ON leases
BEGIN
	UPDATE attempts
	SET phase = 'assigned',
	    lease_id = NEW.lease_id,
	    revision = revision + 1,
	    updated_at_ns = CASE
		WHEN updated_at_ns < NEW.issued_at_ns THEN NEW.issued_at_ns
		ELSE updated_at_ns
	    END
	WHERE attempt_id = NEW.attempt_id
	  AND phase = 'placing'
	  AND lease_id IS NULL;
END;
CREATE TRIGGER attempts_task_ir_insert
BEFORE INSERT ON attempts
WHEN NEW.task_ir_digest <> (SELECT task_ir_digest FROM tasks WHERE task_id = NEW.task_id)
BEGIN
	SELECT RAISE(ABORT, 'attempt TaskIR digest differs from parent Task');
END;
CREATE TRIGGER attempts_identity_immutable
BEFORE UPDATE OF task_id, task_ir_digest, attempt_number, created_at_ns ON attempts
WHEN OLD.task_id <> NEW.task_id
  OR OLD.task_ir_digest <> NEW.task_ir_digest
  OR OLD.attempt_number <> NEW.attempt_number
  OR OLD.created_at_ns <> NEW.created_at_ns
BEGIN
	SELECT RAISE(ABORT, 'attempt identity fields are immutable');
END;
CREATE TRIGGER attempts_transition_guard
BEFORE UPDATE OF phase, worker_identity, lease_id, close_reason, revision, updated_at_ns ON attempts
WHEN NEW.revision <> OLD.revision + 1
  OR NEW.updated_at_ns < OLD.updated_at_ns
  OR NOT (
	(OLD.phase = 'created' AND NEW.phase IN ('placing','closed'))
	OR (OLD.phase = 'placing' AND NEW.phase IN ('placing','assigned','closed'))
	OR (OLD.phase = 'assigned' AND NEW.phase IN ('dispatched','closed'))
	OR (OLD.phase = 'dispatched' AND NEW.phase IN ('claimed','closed'))
	OR (OLD.phase = 'claimed' AND NEW.phase IN ('verifying','closed'))
	OR (OLD.phase = 'verifying' AND NEW.phase = 'closed')
  )
  OR (
	OLD.worker_identity <> NEW.worker_identity
	AND NOT (
		(OLD.phase = 'created' AND NEW.phase = 'placing')
		OR (OLD.phase = 'placing' AND NEW.phase = 'placing')
	)
  )
  OR (OLD.lease_id IS NOT NULL AND NEW.lease_id IS NOT OLD.lease_id)
  OR (
	OLD.lease_id IS NULL AND NEW.lease_id IS NOT NULL
	AND NOT (OLD.phase = 'placing' AND NEW.phase = 'assigned')
  )
BEGIN
	SELECT RAISE(ABORT, 'invalid attempt transition');
END`,
	// 5: durable Policy Epoch / revision status for Lease issuance fencing
	`ALTER TABLE leases ADD COLUMN policy_epoch INTEGER NOT NULL DEFAULT 0 CHECK (policy_epoch >= 0);
CREATE TABLE policy_history (
	epoch           INTEGER NOT NULL PRIMARY KEY CHECK (epoch >= 1),
	revision        TEXT    NOT NULL CHECK (length(revision) BETWEEN 1 AND 128),
	status          TEXT    NOT NULL CHECK (status IN ('active','revoked')),
	activated_at_ns INTEGER NOT NULL,
	revoked_at_ns   INTEGER,
	revoke_reason   TEXT    NOT NULL CHECK (length(revoke_reason) <= 512),
	CHECK (
		(status = 'active' AND revoked_at_ns IS NULL AND revoke_reason = '')
		OR
		(status = 'revoked' AND revoked_at_ns IS NOT NULL AND revoked_at_ns >= activated_at_ns AND length(revoke_reason) >= 1)
	)
) STRICT;
CREATE TABLE policy_current (
	singleton INTEGER NOT NULL PRIMARY KEY CHECK (singleton = 1),
	epoch     INTEGER NOT NULL UNIQUE REFERENCES policy_history (epoch)
) STRICT;
CREATE TRIGGER policy_current_insert_guard
BEFORE INSERT ON policy_current
WHEN NEW.epoch <> 1
  OR NOT EXISTS (
	SELECT 1 FROM policy_history
	WHERE epoch = NEW.epoch AND status = 'active'
  )
BEGIN
	SELECT RAISE(ABORT, 'initial policy must be active at epoch 1');
END;
CREATE TRIGGER policy_current_update_guard
BEFORE UPDATE OF epoch ON policy_current
WHEN NEW.epoch <> OLD.epoch + 1
  OR NOT EXISTS (
	SELECT 1 FROM policy_history
	WHERE epoch = NEW.epoch
  )
BEGIN
	SELECT RAISE(ABORT, 'policy epoch must advance by one');
END;
CREATE TRIGGER policy_history_no_revoked_reactivation
BEFORE INSERT ON policy_history
WHEN NEW.status = 'active'
  AND EXISTS (
	SELECT 1 FROM policy_history
	WHERE revision = NEW.revision AND status = 'revoked'
  )
BEGIN
	SELECT RAISE(ABORT, 'revoked policy revision cannot be reactivated');
END`,
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
