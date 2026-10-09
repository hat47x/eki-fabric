// Package sqlite is a durable federation.Registry backed by one local SQLite file.
//
// Store wraps the in-memory federation.Registry of the core. A write is applied to
// the core first, which checks it. The change is then saved in one immediate
// transaction, and Store returns nil only after the commit succeeds. The database
// uses WAL mode with synchronous=FULL, so a committed change survives a process
// crash or a power loss on a local disk that honors flush requests. A network
// filesystem, or a disk whose cache ignores flushes, can lose a commit that Store
// reported as successful. This adapter cannot detect that case.
//
// If a save fails, Store rebuilds the in-memory state from the stored rows, so
// memory never keeps a change that storage does not hold. If the save failed
// after the commit, the rebuilt state shows the change. If the rebuild fails too,
// Store refuses every later call until it is reopened.
//
// Each update checks the values it expects to find in storage (a compare-and-set).
// A write that finds other values fails and rebuilds the memory state, so a stale
// Store cannot overwrite a newer change.
//
// Open restores the stored state through federation.RestoreRegistry. Every row
// is checked again, including each bundle's authority signature, and a row that
// fails the check makes Open return an error. The database must be used only by
// this adapter. Anyone who can write to the file with SQL can change its rows,
// and the triggers only stop the most direct rewinds.
//
// Single writer: one Store should be open for a file at a time. Store does not
// reload from storage on reads, so a second Store that writes to the same file
// leaves this Store's reads stale until one of its own writes fails or it is
// reopened. The writes stay safe because of the compare-and-set checks above.
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

	"github.com/hat47x/eki-fabric/internal/core/federation"
)

// ErrUnsupportedSchema is returned when the database was written by a newer
// schema version than this adapter understands.
var ErrUnsupportedSchema = errors.New("unsupported federation registry schema version")

var errClosed = errors.New("sqlite federation registry: store is closed")

// Store is a durable federation.Registry. It is safe for concurrent use within
// one process.
type Store struct {
	db     *sql.DB
	maxAge time.Duration

	mu     sync.RWMutex
	reg    *federation.Registry
	closed bool
	broken error
}

// Open opens or creates the federation registry database at path, brings its
// schema up to date, and restores the stored state. maxBundleAge is the staleness
// policy of the core and must be positive. It is not stored in the database. The
// path must be on a local filesystem, and the file should be used only for the
// federation registry.
func Open(ctx context.Context, path string, maxBundleAge time.Duration) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite federation registry: path is required")
	}
	if maxBundleAge <= 0 {
		return nil, fmt.Errorf("sqlite federation registry: %w: got %s", federation.ErrInvalidMaxBundleAge, maxBundleAge)
	}
	if err := ensureFile(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite federation registry: %w", err)
	}
	// One connection serializes access within this process. Across processes,
	// the immediate transaction takes the database write lock.
	db.SetMaxOpenConns(1)

	if err := migrate(ctx, db, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, maxAge: maxBundleAge}
	reg, err := s.loadRegistry(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	s.reg = reg
	return s, nil
}

// Close releases the database. Committed changes are already durable.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// Federate establishes the link for a domain together with its first bundle. The
// rules are those of federation.Registry.Federate. The change is reported as
// successful only after it is committed.
func (s *Store) Federate(link federation.Link, bundle federation.Bundle, authoritySignature []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	if err := s.reg.Federate(link, bundle, authoritySignature); err != nil {
		return err
	}
	return s.save(fmt.Sprintf("federate %s", link.Domain()), func(tx *sql.Tx) error {
		return insertDomain(tx, link, bundle, authoritySignature)
	})
}

// InstallBundle offers a bundle for its own domain. The rules are those of
// federation.Registry.InstallBundle. The change is reported as successful only
// after it is committed.
func (s *Store) InstallBundle(bundle federation.Bundle, authoritySignature []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	// The previous binding is the expected stored state. If the domain is not
	// federated, prev is the zero Binding, but the core then refuses the offer
	// below, so prev is not used.
	prev, _ := s.reg.Binding(bundle.Domain())
	if err := s.reg.InstallBundle(bundle, authoritySignature); err != nil {
		return err
	}
	post, err := s.reg.Binding(bundle.Domain())
	if err != nil {
		return s.rebuild(err)
	}
	return s.save(fmt.Sprintf("install bundle %d for %s", post.Sequence(), post.Domain()), func(tx *sql.Tx) error {
		return updateBundle(tx, prev, post, authoritySignature)
	})
}

// Revoke removes a federated domain from federation from effectiveAt onward. The
// rules are those of federation.Registry.Revoke. The change is reported as
// successful only after it is committed.
func (s *Store) Revoke(domain federation.TrustDomain, effectiveAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	if err := s.reg.Revoke(domain, effectiveAt); err != nil {
		return err
	}
	post, err := s.reg.Binding(domain)
	if err != nil {
		return s.rebuild(err)
	}
	return s.save(fmt.Sprintf("revoke %s", domain), func(tx *sql.Tx) error {
		return markRevoked(tx, domain, post.RevokedAt())
	})
}

// Binding returns the current binding of one domain, as federation.Registry.Binding.
func (s *Store) Binding(domain federation.TrustDomain) (federation.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.usable(); err != nil {
		return federation.Binding{}, err
	}
	return s.reg.Binding(domain)
}

// Verify checks an identity, as federation.Registry.Verify.
func (s *Store) Verify(id federation.Identity, purpose federation.Purpose, now time.Time) (federation.Verified, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.usable(); err != nil {
		return federation.Verified{}, err
	}
	return s.reg.Verify(id, purpose, now)
}

// usable reports whether the Store can serve calls. The caller holds mu.
func (s *Store) usable() error {
	if s.closed {
		return errClosed
	}
	return s.broken
}

// save runs write in one immediate transaction. If the transaction fails, the
// in-memory state is rebuilt from storage. The caller holds the write lock.
func (s *Store) save(op string, write func(tx *sql.Tx) error) error {
	err := s.commit(write)
	if err == nil {
		return nil
	}
	return s.rebuild(fmt.Errorf("%s: %w", op, err))
}

// commit runs write in a transaction and returns nil only after the commit.
func (s *Store) commit(write func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := write(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// rebuild replaces the in-memory state with the stored state after a failed
// write. The caller holds the write lock. If storage cannot be read or restored,
// the Store refuses later calls, because its memory no longer matches storage.
func (s *Store) rebuild(cause error) error {
	reg, err := s.loadRegistry(context.Background())
	if err != nil {
		s.broken = fmt.Errorf("sqlite federation registry: state cannot be reloaded after a failed write, reopen the store: %w", errors.Join(cause, err))
		return s.broken
	}
	s.reg = reg
	return fmt.Errorf("sqlite federation registry: change not stored, state reloaded from storage: %w", cause)
}

// loadRegistry reads the stored state and restores it through the core.
func (s *Store) loadRegistry(ctx context.Context) (*federation.Registry, error) {
	snap, err := readSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	reg, err := federation.RestoreRegistry(s.maxAge, snap)
	if err != nil {
		return nil, fmt.Errorf("sqlite federation registry: stored state is refused: %w", err)
	}
	return reg, nil
}

// ensureFile creates the database file with owner-only permissions before the
// driver opens it, because the file holds internal decision metadata. An existing
// file is left unchanged.
func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("prepare sqlite federation registry file: %w", err)
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
	// Every transaction takes the write lock when it begins, so a read followed by
	// a write cannot interleave with another writer.
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
