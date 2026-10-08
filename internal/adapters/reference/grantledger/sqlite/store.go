// Package sqlite is a durable grant revocation ledger backed by one local SQLite
// file. It keeps a core grant.Ledger in memory, restores that ledger from the file
// on Open, and writes each revocation to the file before it reports success.
//
// Revoke checks the revocation in the core first, then inserts one row in an
// immediate transaction, and returns nil only after the commit succeeds. The
// grant digest is the table's primary key, so the database refuses a second
// revocation of the same grant, even when another handle on the file recorded it
// first. A refused write rebuilds the in-memory ledger from the rows, so memory
// never gets ahead of the file.
//
// Open reads every row and restores it through the core. The core refuses a row
// that is malformed or contradicts itself, and then Open fails, so a damaged file
// is never loaded in part.
//
// Single writer. Run one Store per file. Two Stores may share a file. The database
// lock orders their writes, and the primary key refuses a duplicate. But a Store
// learns of another Store's revocations only when its own write is refused, or
// when it is opened again. Admit reads the in-memory ledger, so a stale Store can
// admit a grant that another Store has already revoked, until then. This package
// does not prevent that.
//
// The file holds decision metadata. It is created readable only by its owner, and
// nothing other than this package should write to it. Anyone who can delete rows
// from it can undo revocations, and this package cannot detect that.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/grant"
)

var (
	// ErrUnsupportedSchema is returned when the database was written by a newer
	// schema version than this adapter understands.
	ErrUnsupportedSchema = errors.New("unsupported grant ledger schema version")
	// ErrCorruptRecord is returned by Open when a stored row cannot be restored
	// into the core ledger.
	ErrCorruptRecord = errors.New("corrupt grant ledger record")
	// ErrUnavailable is returned by every call on a Store that can no longer be
	// trusted to match the file, and on a Store that has been closed. Open the
	// file again to continue.
	ErrUnavailable = errors.New("grant ledger store is unavailable")
)

const insertRevocationSQL = `
INSERT INTO grant_revocations
    (grant_digest, issued_at_sec, issued_at_nsec, effective_at_sec, effective_at_nsec, reason)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (grant_digest) DO NOTHING`

const selectRevocationsSQL = `
SELECT grant_digest, issued_at_sec, issued_at_nsec, effective_at_sec, effective_at_nsec, reason
FROM grant_revocations
ORDER BY grant_digest`

// Store is a durable grant revocation ledger. Its methods are safe for concurrent
// use by goroutines in one process.
type Store struct {
	db *sql.DB

	mu     sync.Mutex
	ledger *grant.Ledger
	// broken is set when the in-memory ledger can no longer be trusted, either
	// because a rebuild from the file failed or because the Store was closed.
	// Every later call returns it, so nothing is admitted or revoked against a
	// ledger that may not match the file.
	broken error
}

// Open opens or creates the grant ledger database at path, brings its schema up
// to date, and restores every stored revocation into memory. The path must be on
// a local filesystem. Open fails if any stored row is malformed or contradicts
// itself.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite grant ledger: path is required")
	}
	if err := ensureFile(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite grant ledger: %w", err)
	}
	// One connection serializes writes within this process. Across processes, the
	// immediate transaction takes the database write lock.
	db.SetMaxOpenConns(1)

	if err := migrate(ctx, db, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}
	ledger, err := loadLedger(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, ledger: ledger}, nil
}

// Close releases the database and makes every later call fail with
// ErrUnavailable. Committed revocations are already durable.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken == nil {
		s.broken = fmt.Errorf("%w: store is closed", ErrUnavailable)
	}
	return s.db.Close()
}

// Revoke records that the provider named by g stops admitting leases under g at
// effectiveAt. The core checks the revocation first, and a refused revocation
// changes nothing. Revoke returns nil only after the record is committed. A grant
// that already has a revocation, recorded by this Store or by another handle on
// the same file, is refused with grant.ErrAlreadyRevoked.
//
// When the write fails, the in-memory ledger is rebuilt from the file, so a
// revocation that was not stored does not stay in effect in memory. If the commit
// itself reports an error after the record reached the file, the rebuild finds
// that record, so the revocation stays in effect, and a later call returns
// grant.ErrAlreadyRevoked.
func (s *Store) Revoke(ctx context.Context, g grant.Grant, actor grant.Identity, reason grant.ReasonCategory, effectiveAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken != nil {
		return s.broken
	}

	// The core checks every rule before it changes the ledger, so a refused
	// revocation leaves the ledger as it was.
	if err := s.ledger.Revoke(g, actor, reason, effectiveAt); err != nil {
		return err
	}
	if err := s.insert(ctx, g, reason, effectiveAt); err != nil {
		if rebuildErr := s.rebuild(); rebuildErr != nil {
			return rebuildErr
		}
		return err
	}
	return nil
}

// Admit decides whether req may start under g at the decision time at, using the
// revocations this Store holds in memory. It delegates to grant.Admit. See the
// package comment for the single-writer assumption that it relies on.
func (s *Store) Admit(g grant.Grant, at time.Time, req grant.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken != nil {
		return s.broken
	}
	return grant.Admit(g, s.ledger, at, req)
}

// PlanReclaim plans the stop instants for grants revoked at effectiveAt, using the
// revocations this Store holds in memory. It delegates to grant.PlanReclaim.
func (s *Store) PlanReclaim(revoked []grant.Grant, effectiveAt time.Time, runningLeases int, cfg grant.ReclaimConfig) ([]grant.ReclaimAssignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken != nil {
		return nil, s.broken
	}
	return grant.PlanReclaim(s.ledger, revoked, effectiveAt, runningLeases, cfg)
}

// insert writes one revocation row in an immediate transaction and commits it.
// It returns grant.ErrAlreadyRevoked when the digest is already stored. The
// constraint on the primary key enforces that, not a check in Go.
func (s *Store) insert(ctx context.Context, g grant.Grant, reason grant.ReasonCategory, effectiveAt time.Time) error {
	d := g.Digest()
	issued := g.IssuedAt()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record revocation of grant %s: %w", d, err)
	}
	defer func() { _ = tx.Rollback() }()

	// Unix seconds and nanoseconds identify the same instant whatever the location
	// of the time value, so the stored values match the core's normalized time.
	res, err := tx.ExecContext(ctx, insertRevocationSQL,
		d.Hex(),
		issued.Unix(), int64(issued.Nanosecond()),
		effectiveAt.Unix(), int64(effectiveAt.Nanosecond()),
		string(reason),
	)
	if err != nil {
		return fmt.Errorf("record revocation of grant %s: %w", d, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("record revocation of grant %s: %w", d, err)
	}
	if n == 0 {
		// The conflict clause changed nothing, so the digest is already stored.
		return grant.ErrAlreadyRevoked
	}
	if n != 1 {
		return fmt.Errorf("record revocation of grant %s: %d rows changed, want 1", d, n)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revocation of grant %s: %w", d, err)
	}
	return nil
}

// rebuild replaces the in-memory ledger with one restored from the file. If that
// fails, the Store is marked broken, and every later call fails.
func (s *Store) rebuild() error {
	ledger, err := loadLedger(context.Background(), s.db)
	if err != nil {
		s.broken = fmt.Errorf("%w: rebuild from file: %w", ErrUnavailable, err)
		return s.broken
	}
	s.ledger = ledger
	return nil
}

// loadLedger restores every stored revocation into a new core ledger. The core
// checks each record, and the first record it refuses fails the whole load.
func loadLedger(ctx context.Context, db *sql.DB) (*grant.Ledger, error) {
	rows, err := db.QueryContext(ctx, selectRevocationsSQL)
	if err != nil {
		return nil, fmt.Errorf("read grant revocations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ledger := &grant.Ledger{}
	for rows.Next() {
		var r revocationRow
		if err := rows.Scan(&r.digest, &r.issuedSec, &r.issuedNsec, &r.effectiveSec, &r.effectiveNsec, &r.reason); err != nil {
			return nil, fmt.Errorf("read grant revocation row: %w", err)
		}
		if err := r.restore(ledger); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read grant revocations: %w", err)
	}
	return ledger, nil
}

// revocationRow holds the columns of one grant_revocations row.
type revocationRow struct {
	digest        string
	issuedSec     int64
	issuedNsec    int64
	effectiveSec  int64
	effectiveNsec int64
	reason        string
}

// restore converts the row and hands it to the core. A row the core cannot
// accept is corrupt. The error does not echo the stored values.
func (r revocationRow) restore(ledger *grant.Ledger) error {
	digest, err := contract.ParseSHA256(r.digest)
	if err != nil || digest.Hex() != r.digest {
		return fmt.Errorf("%w: grant digest is not canonical sha256 hex", ErrCorruptRecord)
	}
	issuedAt, err := unixTime(r.issuedSec, r.issuedNsec)
	if err != nil {
		return fmt.Errorf("%w: issuance time: %w", ErrCorruptRecord, err)
	}
	effectiveAt, err := unixTime(r.effectiveSec, r.effectiveNsec)
	if err != nil {
		return fmt.Errorf("%w: effective time: %w", ErrCorruptRecord, err)
	}
	rev := grant.Revocation{Reason: grant.ReasonCategory(r.reason), EffectiveAt: effectiveAt}
	if err := ledger.RestoreRevocation(digest, issuedAt, rev); err != nil {
		return fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	return nil
}

// unixTime converts stored seconds and nanoseconds into a UTC time. The
// nanoseconds must be in range, because time.Unix would otherwise carry an
// out-of-range value into the seconds without reporting it.
func unixTime(sec, nsec int64) (time.Time, error) {
	if nsec < 0 || nsec > 999_999_999 {
		return time.Time{}, errors.New("nanoseconds outside 0..999999999")
	}
	return time.Unix(sec, nsec).UTC(), nil
}

// ensureFile creates the database file with owner-only permissions before the
// driver opens it, because the file holds internal decision metadata. An existing
// file is left unchanged.
func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("prepare sqlite grant ledger file: %w", err)
	}
	return f.Close()
}

// dsn builds the connection string. synchronous=FULL keeps each committed
// transaction durable under WAL across power loss.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "foreign_keys(1)")
	// Every transaction takes the write lock when it begins, so a read followed
	// by a write cannot interleave with another writer.
	q.Add("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Opaque: filepathToURI(path), RawQuery: q.Encode()}).String()
}

// filepathToURI converts a filesystem path into the opaque part of a SQLite
// "file:" URI. SQLite needs a leading slash for Windows drive paths, and reserved
// characters must not start the query string.
func filepathToURI(path string) string {
	p := filepath.ToSlash(path)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p
	}
	r := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")
	return r.Replace(p)
}
