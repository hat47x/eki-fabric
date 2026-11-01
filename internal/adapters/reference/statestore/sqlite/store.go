// Package sqlite is the M0 reference durable State Store adapter.
//
// It implements statestore.TaskStore on a local SQLite database file, as
// decided in ADR-0003. The schema here is this adapter's persistence schema
// only; it is not an external contract and rows are never exposed directly.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var (
	// ErrUnsupportedSchema is returned when the database was written by a
	// newer schema version than this adapter understands.
	ErrUnsupportedSchema = errors.New("unsupported state store schema version")
	// ErrCorruptRecord is returned when a stored row cannot be turned back
	// into a valid TaskRecord.
	ErrCorruptRecord = errors.New("corrupt state store record")
)

// Store is a durable statestore.TaskStore backed by one local SQLite file.
type Store struct {
	db *sql.DB

	// listingNow is the clock the assignment listing reads; time.Now unless a
	// test replaces it through SetListingClock.
	listingNow atomic.Pointer[func() time.Time]
}

// Open opens or creates the database at path and brings its schema up to
// date. The path must be on a local filesystem (ADR-0003 §6).
func Open(ctx context.Context, path string) (*Store, error) {
	return openWithMigrations(ctx, path, migrations)
}

// openWithMigrations is Open with an explicit migration list, so tests can
// create a database at an older schema version.
func openWithMigrations(ctx context.Context, path string, steps []string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite state store: path is required")
	}
	if err := ensureFile(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite state store: %w", err)
	}
	// SQLite allows one writer at a time. A single connection keeps the
	// adapter's transaction boundaries simple and avoids SQLITE_BUSY between
	// our own goroutines (ADR-0003 §9).
	db.SetMaxOpenConns(1)

	if err := migrate(ctx, db, steps); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database. Committed data is already durable.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateTask(ctx context.Context, record taskstate.TaskRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, `
INSERT INTO tasks (task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (task_id) DO NOTHING`,
		string(record.ID),
		record.TaskIRDigest.Hex(),
		string(record.Phase),
		string(record.TerminalOutcome),
		boolToInt(record.CancellationRequested),
		int64(record.Revision),
		record.CreatedAt.UTC().UnixNano(),
		record.UpdatedAt.UTC().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("create task %s: %w", record.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("create task %s: %w", record.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: task %s", statestore.ErrAlreadyExists, record.ID)
	}
	return nil
}

func (s *Store) LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error) {
	if err := ctx.Err(); err != nil {
		return taskstate.TaskRecord{}, err
	}

	row := s.db.QueryRowContext(ctx, `
SELECT task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns
FROM tasks WHERE task_id = ?`, string(id))
	record, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return taskstate.TaskRecord{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, id)
	}
	if err != nil {
		return taskstate.TaskRecord{}, err
	}
	return record, nil
}

func (s *Store) CompareAndSwapTask(ctx context.Context, id ids.TaskID, expectedRevision uint64, next taskstate.TaskRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if next.ID != id {
		return fmt.Errorf("%w: next task ID does not match target", statestore.ErrConflict)
	}
	if next.Revision != expectedRevision+1 {
		return fmt.Errorf("%w: next revision %d does not follow expected revision %d", statestore.ErrConflict, next.Revision, expectedRevision)
	}
	if err := next.Validate(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compare-and-swap task %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	// Read the current record inside the transaction so the transition is
	// judged against the same revision the update is based on.
	current, err := scanTask(tx.QueryRowContext(ctx, `
SELECT task_id, task_ir_digest, phase, terminal_outcome, cancellation_requested, revision, created_at_ns, updated_at_ns
FROM tasks WHERE task_id = ?`, string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: task %s", statestore.ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return fmt.Errorf("%w: task %s is at revision %d, expected %d", statestore.ErrConflict, id, current.Revision, expectedRevision)
	}
	if current.TaskIRDigest != next.TaskIRDigest {
		return fmt.Errorf("%w: task %s TaskIR digest is immutable", statestore.ErrConflict, id)
	}
	if err := taskstate.ValidateTransition(current, next); err != nil {
		return err
	}

	// The creation time is immutable and the digest is the Task identity's
	// content binding, so neither is rewritten by a state transition. The
	// digest is part of the WHERE clause so that a rebinding attempt matches
	// no row and is reported as a conflict below, instead of being silently
	// dropped.
	res, err := tx.ExecContext(ctx, `
UPDATE tasks
SET phase = ?, terminal_outcome = ?, cancellation_requested = ?, revision = ?, updated_at_ns = ?
WHERE task_id = ? AND revision = ? AND task_ir_digest = ?`,
		string(next.Phase),
		string(next.TerminalOutcome),
		boolToInt(next.CancellationRequested),
		int64(next.Revision),
		next.UpdatedAt.UTC().UnixNano(),
		string(id),
		int64(expectedRevision),
		next.TaskIRDigest.Hex(),
	)
	if err != nil {
		return fmt.Errorf("compare-and-swap task %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("compare-and-swap task %s: %w", id, err)
	}

	if n == 0 {
		var (
			current       int64
			currentDigest string
		)
		err := tx.QueryRowContext(ctx, `SELECT revision, task_ir_digest FROM tasks WHERE task_id = ?`, string(id)).Scan(&current, &currentDigest)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task %s", statestore.ErrNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("compare-and-swap task %s: %w", id, err)
		}
		if current == int64(expectedRevision) && currentDigest != next.TaskIRDigest.Hex() {
			return fmt.Errorf("%w: task %s TaskIR digest is immutable", statestore.ErrConflict, id)
		}
		return fmt.Errorf("%w: task %s is at revision %d, expected %d", statestore.ErrConflict, id, current, expectedRevision)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("compare-and-swap task %s: %w", id, err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (taskstate.TaskRecord, error) {
	var cols taskColumns
	if err := row.Scan(&cols.id, &cols.digestHex, &cols.phase, &cols.outcome, &cols.cancel, &cols.revision, &cols.createdNS, &cols.updatedNS); err != nil {
		return taskstate.TaskRecord{}, err
	}
	return cols.record()
}

// taskColumns holds the raw columns of one tasks row.
type taskColumns struct {
	id, digestHex, phase, outcome  string
	cancel                         int64
	revision, createdNS, updatedNS int64
}

// record converts the row into a validated TaskRecord. A row that cannot be
// converted is corrupt, not a Client error.
func (c taskColumns) record() (taskstate.TaskRecord, error) {
	digest, err := contract.ParseSHA256(c.digestHex)
	if err != nil {
		return taskstate.TaskRecord{}, fmt.Errorf("%w: task %s: %v", ErrCorruptRecord, c.id, err)
	}
	if c.revision < 1 {
		return taskstate.TaskRecord{}, fmt.Errorf("%w: task %s: revision %d", ErrCorruptRecord, c.id, c.revision)
	}

	record := taskstate.TaskRecord{
		ID:                    ids.TaskID(c.id),
		TaskIRDigest:          digest,
		Phase:                 taskstate.TaskPhase(c.phase),
		TerminalOutcome:       taskstate.TerminalOutcome(c.outcome),
		CancellationRequested: c.cancel != 0,
		Revision:              uint64(c.revision),
		CreatedAt:             time.Unix(0, c.createdNS).UTC(),
		UpdatedAt:             time.Unix(0, c.updatedNS).UTC(),
	}
	if err := record.Validate(); err != nil {
		return taskstate.TaskRecord{}, fmt.Errorf("%w: task %s: %v", ErrCorruptRecord, c.id, err)
	}
	return record, nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ensureFile creates the database file with owner-only permissions before the
// driver opens it, because the file holds Task and internal decision
// metadata (ADR-0003 §11). An existing file is left unchanged.
func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("prepare sqlite state store file: %w", err)
	}
	return f.Close()
}

// dsn builds the connection string. synchronous=FULL keeps the durability of
// each committed transaction under WAL across power loss, which the "commit
// before dispatch" rule depends on (ADR-0003 §10).
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "foreign_keys(1)")
	// Every transaction takes the write lock up front, so a read-then-write
	// sequence (idempotency lookup, quota count, insert) cannot interleave with
	// another writer.
	q.Add("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Opaque: filepathToURI(path), RawQuery: q.Encode()}).String()
}

var _ statestore.TaskStore = (*Store)(nil)
