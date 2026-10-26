// Package sqlite records Worker nonce claims in one local SQLite database.
// A true result is returned only after commit. This is replay protection, not
// evidence that a Task started, completed, or ran exactly once.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlite3 "modernc.org/sqlite"

	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/spi/replayguard"
)

var (
	ErrInvalidGuard  = errors.New("invalid durable replay guard configuration")
	ErrInvalidNonce  = errors.New("invalid nonce claim")
	ErrGuardCapacity = errors.New("replay guard is full")
)

type Clock interface{ Now() time.Time }

type Guard struct {
	db         *sql.DB
	clock      Clock
	maxEntries int
}

var _ replayguard.Guard = (*Guard)(nil)

// Open uses a dedicated local file for one Worker's replay namespace. All
// connections must agree on capacity. A full guard never evicts a live claim.
// The owner must protect this file from deletion, rollback, and untrusted SQL.
func Open(ctx context.Context, path string, clock Clock, maxEntries int) (*Guard, error) {
	if ctx == nil || path == "" || clock == nil || maxEntries <= 0 {
		return nil, ErrInvalidGuard
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve replay guard path: %w", err)
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("create replay guard file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	// Several processes may open a brand-new file at once. Switching it to WAL
	// and creating the schema take locks that busy_timeout does not always
	// wait for, so setup is retried while another opener holds them.
	deadline := time.Now().Add(openRetryWindow)
	for {
		g, err := openOnce(ctx, abs, clock, maxEntries)
		if err == nil || !isBusy(err) || !time.Now().Before(deadline) {
			return g, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(openRetryInterval):
		}
	}
}

const (
	openRetryWindow   = 10 * time.Second
	openRetryInterval = 10 * time.Millisecond
)

func openOnce(ctx context.Context, abs string, clock Clock, maxEntries int) (*Guard, error) {
	db, err := sql.Open("sqlite", dsn(abs))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	g := &Guard{db: db, clock: clock, maxEntries: maxEntries}
	if err := g.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return g, nil
}

// isBusy reports SQLITE_BUSY and SQLITE_LOCKED, including extended codes.
func isBusy(err error) bool {
	var se *sqlite3.Error
	if errors.As(err, &se) {
		code := se.Code() & 0xff
		return code == 5 || code == 6
	}
	return false
}

func (g *Guard) initialize(ctx context.Context) error {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS replay_guard_config (
 singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
 schema_version INTEGER NOT NULL CHECK(schema_version > 0),
 capacity INTEGER NOT NULL CHECK(typeof(capacity) = 'integer' AND capacity > 0))`,
		`CREATE TABLE IF NOT EXISTS nonce_claims (
 nonce_hash BLOB PRIMARY KEY NOT NULL CHECK(typeof(nonce_hash) = 'blob' AND length(nonce_hash) = 32),
 until_ns INTEGER NOT NULL CHECK(typeof(until_ns) = 'integer' AND until_ns > 0))`,
		`CREATE INDEX IF NOT EXISTS nonce_claim_expiry ON nonce_claims(until_ns)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO replay_guard_config VALUES (1, 1, ?)`, g.maxEntries); err != nil {
		return err
	}
	var version, capacity int
	if err := tx.QueryRowContext(ctx, `SELECT schema_version, capacity FROM replay_guard_config WHERE singleton = 1`).Scan(&version, &capacity); err != nil {
		return err
	}
	if version != 1 || capacity != g.maxEntries {
		return fmt.Errorf("%w: stored schema or capacity differs", ErrInvalidGuard)
	}
	return tx.Commit()
}

func (g *Guard) Close() error {
	if g == nil || g.db == nil {
		return ErrInvalidGuard
	}
	return g.db.Close()
}

// Claim purges expired records, checks replay and capacity, then commits the
// hash and expiry in one immediate transaction. Errors always return false;
// a commit error may still mean the claim was recorded, so fail closed.
func (g *Guard) Claim(ctx context.Context, nonce []byte, until time.Time) (bool, error) {
	if g == nil || g.db == nil || g.clock == nil {
		return false, ErrInvalidGuard
	}
	if ctx == nil || len(nonce) < decisionpayload.MinWorkOrderNonceBytes || len(nonce) > decisionpayload.MaxWorkOrderNonceBytes {
		return false, ErrInvalidNonce
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	digest := sha256.Sum256(nonce)
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Read the trusted clock after acquiring the write lock, not before waiting.
	now := g.clock.Now().UTC()
	until = until.UTC()
	if !validTime(now) || !validTime(until) || !until.After(now) {
		return false, ErrInvalidNonce
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM nonce_claims WHERE until_ns <= ?`, now.UnixNano()); err != nil {
		return false, err
	}
	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT until_ns FROM nonce_claims WHERE nonce_hash = ?`, digest[:]).Scan(&existing)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nonce_claims`).Scan(&count); err != nil {
		return false, err
	}
	if count >= g.maxEntries {
		return false, ErrGuardCapacity
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO nonce_claims(nonce_hash, until_ns) VALUES (?, ?)`, digest[:], until.UnixNano()); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func validTime(t time.Time) bool {
	return !t.IsZero() && t.UnixNano() > 0 && time.Unix(0, t.UnixNano()).UTC().Equal(t)
}

func dsn(path string) string {
	p := filepath.ToSlash(path)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p
	}
	p = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(p)
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Opaque: p, RawQuery: q.Encode()}).String()
}
