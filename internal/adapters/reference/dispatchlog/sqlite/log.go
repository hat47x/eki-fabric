// Package sqlite is a durable dispatchlog.Log backed by one local SQLite file.
//
// Each RecordAttempt runs in one immediate transaction and returns nil only
// after the commit succeeds. The database uses WAL mode with synchronous=FULL,
// so a record reported as committed survives a process crash or a power loss on
// a local disk that honors flush requests. A network filesystem, or a disk whose
// cache ignores flushes, can lose a commit that this adapter reported as
// successful. This adapter cannot detect that case.
//
// The strictly increasing rule is enforced by the SQL statement itself, not
// only by Go code. The file must hold only dispatch records. Anyone who can
// write to it with SQL can delete rows, and this adapter cannot prevent that.
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

	"github.com/hat47x/eki-fabric/internal/spi/dispatchlog"
)

// ErrUnsupportedSchema is returned when the database was written by a newer
// schema version than this adapter understands.
var ErrUnsupportedSchema = errors.New("unsupported dispatch log schema version")

// Log is a durable dispatchlog.Log. It is safe for concurrent use.
type Log struct {
	db *sql.DB
}

var _ dispatchlog.Log = (*Log)(nil)

// Open opens or creates the dispatch log database at path and brings its schema
// up to date. The path must be on a local filesystem. The file should be used
// only for dispatch records.
func Open(ctx context.Context, path string) (*Log, error) {
	if path == "" {
		return nil, errors.New("sqlite dispatch log: path is required")
	}
	if err := ensureFile(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite dispatch log: %w", err)
	}
	// One connection serializes writes within this process. Across processes,
	// the immediate transaction takes the database write lock.
	db.SetMaxOpenConns(1)

	if err := migrate(ctx, db, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Log{db: db}, nil
}

// Close releases the database. Committed records are already durable.
func (l *Log) Close() error {
	return l.db.Close()
}

// recordAttemptSQL inserts the first record for an effect, or moves the stored
// generation forward when the new generation is greater. For an equal or lower
// generation, the WHERE clause changes no row, so the affected row count shows
// whether the record was written.
const recordAttemptSQL = `
INSERT INTO dispatch_attempts (effect_id, generation, recorded_at_ns)
VALUES (?, ?, ?)
ON CONFLICT (effect_id) DO UPDATE
SET generation = excluded.generation,
    recorded_at_ns = excluded.recorded_at_ns
WHERE excluded.generation > dispatch_attempts.generation`

// RecordAttempt implements dispatchlog.Log.
func (l *Log) RecordAttempt(ctx context.Context, effect dispatchlog.EffectID, generation uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := effect.Validate(); err != nil {
		return err
	}
	if err := dispatchlog.ValidateGeneration(generation); err != nil {
		return err
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record dispatch attempt for effect %s: %w", effect, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, recordAttemptSQL, string(effect), int64(generation), time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("record dispatch attempt for effect %s: %w", effect, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("record dispatch attempt for effect %s: %w", effect, err)
	}
	if n == 0 {
		// Nothing was written, so the stored generation is at least the requested one.
		var latest int64
		err = tx.QueryRowContext(ctx, `SELECT generation FROM dispatch_attempts WHERE effect_id = ?`, string(effect)).Scan(&latest)
		if err != nil {
			return fmt.Errorf("read latest dispatch attempt for effect %s: %w", effect, err)
		}
		if latest == int64(generation) {
			return fmt.Errorf("%w: effect %s generation %d", dispatchlog.ErrAlreadyRecorded, effect, generation)
		}
		return fmt.Errorf("%w: effect %s generation %d is not above recorded %d", dispatchlog.ErrNotIncreasing, effect, generation, latest)
	}
	if n != 1 {
		return fmt.Errorf("record dispatch attempt for effect %s: %d rows changed, want 1", effect, n)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dispatch attempt for effect %s: %w", effect, err)
	}
	return nil
}

// Latest implements dispatchlog.Log.
func (l *Log) Latest(ctx context.Context, effect dispatchlog.EffectID) (uint64, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if err := effect.Validate(); err != nil {
		return 0, false, err
	}

	var latest int64
	err := l.db.QueryRowContext(ctx, `SELECT generation FROM dispatch_attempts WHERE effect_id = ?`, string(effect)).Scan(&latest)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read latest dispatch attempt for effect %s: %w", effect, err)
	}
	return uint64(latest), true, nil
}

// ensureFile creates the database file with owner-only permissions before the
// driver opens it, because the file holds internal decision metadata. An
// existing file is left unchanged.
func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("prepare sqlite dispatch log file: %w", err)
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
// "file:" URI. SQLite needs a leading slash for Windows drive paths, and
// reserved characters must not start the query string.
func filepathToURI(path string) string {
	p := filepath.ToSlash(path)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p
	}
	r := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")
	return r.Replace(p)
}
