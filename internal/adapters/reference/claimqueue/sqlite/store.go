// Package sqlite preserves the Worker's pending ResultClaims and retry state.
// It does not assert that an Outcome was truthful or accepted by the Control Plane.
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
	"time"

	_ "modernc.org/sqlite"

	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var ErrInvalidEntry = claimqueue.ErrInvalidEntry

type Store struct{ db *sql.DB }

var _ claimqueue.Store = (*Store)(nil)

const columns = `attempt_id, claim_digest, claim_json, queued_at_ns, attempts, next_at_ns, last_error`

func Open(ctx context.Context, path string) (*Store, error) {
	if ctx == nil || path == "" {
		return nil, fmt.Errorf("%w: context and dedicated database path required", claimqueue.ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(abs))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initialize(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS claim_queue_meta (singleton INTEGER PRIMARY KEY CHECK(singleton=1), schema_version INTEGER NOT NULL)`,
		`INSERT OR IGNORE INTO claim_queue_meta VALUES(1,1)`,
		`CREATE TABLE IF NOT EXISTS pending_claims (
 attempt_id TEXT PRIMARY KEY NOT NULL,
 claim_digest TEXT NOT NULL, claim_json BLOB NOT NULL,
 queued_at_ns INTEGER NOT NULL CHECK(typeof(queued_at_ns)='integer' AND queued_at_ns>0),
 attempts INTEGER NOT NULL CHECK(typeof(attempts)='integer' AND attempts BETWEEN 0 AND 4294967295),
 next_at_ns INTEGER NOT NULL CHECK(typeof(next_at_ns)='integer' AND next_at_ns>0), last_error TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS pending_claim_due ON pending_claims(next_at_ns,attempt_id)`,
		`CREATE TRIGGER IF NOT EXISTS pending_claim_immutable BEFORE UPDATE OF attempt_id,claim_digest,claim_json,queued_at_ns ON pending_claims BEGIN SELECT RAISE(ABORT,'queued claim is immutable'); END`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT schema_version FROM claim_queue_meta WHERE singleton=1`).Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("%w: unsupported queue schema", claimqueue.ErrInvalidConfig)
	}
	return tx.Commit()
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return claimqueue.ErrInvalidConfig
	}
	return s.db.Close()
}

func (s *Store) check(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil {
		return claimqueue.ErrInvalidConfig
	}
	return ctx.Err()
}

type scanner interface{ Scan(...any) error }

func scanEntry(row scanner) (claimqueue.Entry, error) {
	var entry claimqueue.Entry
	var id, digest string
	var raw []byte
	var queued, next int64
	if err := row.Scan(&id, &digest, &raw, &queued, &entry.Attempts, &next, &entry.LastError); err != nil {
		return claimqueue.Entry{}, err
	}
	claim, err := decodeClaim(raw)
	if err != nil {
		return claimqueue.Entry{}, fmt.Errorf("%w: decode stored claim: %v", ErrInvalidEntry, err)
	}
	if claim.AttemptID().String() != id || claim.Digest().String() != digest {
		return claimqueue.Entry{}, fmt.Errorf("%w: stored claim binding differs", ErrInvalidEntry)
	}
	entry.Claim, entry.QueuedAt, entry.NextAt = claim, time.Unix(0, queued).UTC(), time.Unix(0, next).UTC()
	return entry, validate(entry)
}

func (s *Store) Load(ctx context.Context, id ids.AttemptID) (claimqueue.Entry, bool, error) {
	if err := s.check(ctx); err != nil {
		return claimqueue.Entry{}, false, err
	}
	entry, err := scanEntry(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM pending_claims WHERE attempt_id=?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return claimqueue.Entry{}, false, nil
	}
	return entry, err == nil, err
}

func (s *Store) Save(ctx context.Context, entry claimqueue.Entry) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validate(entry); err != nil {
		return err
	}
	raw, err := encodeClaim(entry.Claim)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEntry, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := entry.Claim.AttemptID().String()
	existing, err := scanEntry(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM pending_claims WHERE attempt_id=?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO pending_claims (`+columns+`) VALUES(?,?,?,?,?,?,?)`,
			id, entry.Claim.Digest().String(), raw, entry.QueuedAt.UnixNano(), entry.Attempts, entry.NextAt.UnixNano(), entry.LastError)
	case err != nil:
		return err
	case !existing.Claim.Equal(entry.Claim):
		return claimqueue.ErrConflict
	case entry.Attempts == 0:
		// Replaying an initial Enqueue must not reset retry state after restart.
		return nil
	case !entry.QueuedAt.Equal(existing.QueuedAt) || entry.Attempts < existing.Attempts:
		return claimqueue.ErrConflict
	case entry.Attempts == existing.Attempts:
		if !entry.NextAt.Equal(existing.NextAt) || entry.LastError != existing.LastError {
			return claimqueue.ErrConflict
		}
		return nil
	default:
		_, err = tx.ExecContext(ctx, `UPDATE pending_claims SET attempts=?,next_at_ns=?,last_error=? WHERE attempt_id=?`,
			entry.Attempts, entry.NextAt.UnixNano(), entry.LastError, id)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]claimqueue.Entry, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if !validTime(now) || limit <= 0 {
		return nil, ErrInvalidEntry
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM pending_claims WHERE next_at_ns<=? ORDER BY next_at_ns,attempt_id LIMIT ?`, now.UTC().UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []claimqueue.Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id ids.AttemptID, digest string) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM pending_claims WHERE attempt_id=? AND claim_digest=?`, id.String(), digest)
	return err
}

func validate(entry claimqueue.Entry) error {
	if !entry.Claim.Valid() || !validTime(entry.QueuedAt) || !validTime(entry.NextAt) {
		return ErrInvalidEntry
	}
	return nil
}

func validTime(t time.Time) bool {
	return !t.IsZero() && t.UnixNano() > 0 && time.Unix(0, t.UnixNano()).UTC().Equal(t.UTC())
}

func dsn(path string) string {
	p := filepath.ToSlash(path)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p
	}
	p = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(p)
	q := url.Values{}
	for _, pragma := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"} {
		q.Add("_pragma", pragma)
	}
	q.Add("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Opaque: p, RawQuery: q.Encode()}).String()
}
