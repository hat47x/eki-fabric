// Package spool keeps the body of a Worker's output on the Worker's own disk
// until the Control Plane has taken it (ADR-0037).
//
// The native runner computes only a digest and a size, so the body is lost when
// the process exits. This package is the smallest step that keeps it: one file
// per output, named after its digest, deleted by Sweep once its retention has
// passed.
package spool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

var (
	// ErrOverBudget means starting another Task would put the host over the
	// budget its owner set (ADR-0037 §5, #498).
	ErrOverBudget = errors.New("the host is holding more than its budget allows")
	// ErrTooLarge means the output did not fit the limit the WorkOrder set. The
	// body is not kept and nothing is truncated: a truncated body would have a
	// different digest, so "the output did not fit" is the only honest answer.
	ErrTooLarge = errors.New("the output exceeds the WorkOrder limit")
	// ErrExpired means the body was deleted after its retention passed.
	ErrExpired = errors.New("the output was deleted after its retention")
	// ErrDigestMismatch means the file on disk is not the body that was put
	// there. It is refused rather than returned: a caller that asked for a
	// digest must not receive bytes that do not match it.
	ErrDigestMismatch = errors.New("the stored body does not match its digest")
)

// Record is what the Worker keeps about a body it stored.
type Record struct {
	// Digest is the SHA-256 of the body, the same value the ResultClaim carries.
	Digest contract.Digest
	// Size is the number of bytes stored.
	Size int64
	// ExpiresAt is when Sweep may delete the body.
	ExpiresAt time.Time
}

// Store is a directory of output bodies. It is not safe for concurrent writes to
// the same digest, which cannot happen: a digest names one body.
type Store struct {
	dir string
	ttl time.Duration
	now func() time.Time
}

// New returns a store rooted at dir with the given retention. The directory is
// created with mode 0700: the bodies are the Worker's own data.
func New(dir string, ttl time.Duration, now func() time.Time) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("spool: no directory")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("spool: retention must be positive")
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("spool: create %s: %w", dir, err)
	}
	return &Store{dir: dir, ttl: ttl, now: now}, nil
}

// Put stores the body read from r and returns its digest and size. A body larger
// than maxBytes is refused and nothing is left behind (ErrTooLarge).
func (s *Store) Put(r io.Reader, maxBytes int64) (Record, error) {
	if maxBytes <= 0 {
		return Record{}, fmt.Errorf("spool: no limit")
	}
	temp, err := os.CreateTemp(s.dir, ".incoming-*")
	if err != nil {
		return Record{}, fmt.Errorf("spool: create: %w", err)
	}
	name := temp.Name()
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(r, maxBytes+1))
	closeErr := temp.Close()
	if copyErr != nil {
		os.Remove(name)
		return Record{}, fmt.Errorf("spool: write: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(name)
		return Record{}, fmt.Errorf("spool: close: %w", closeErr)
	}
	if written > maxBytes {
		os.Remove(name)
		return Record{}, ErrTooLarge
	}
	record := Record{
		Digest:    digestOf(hasher.Sum(nil)),
		Size:      written,
		ExpiresAt: s.now().UTC().Add(s.ttl),
	}
	final := s.path(record)
	if err := os.Rename(name, final); err != nil {
		os.Remove(name)
		return Record{}, fmt.Errorf("spool: keep: %w", err)
	}
	return record, nil
}

// Open returns the stored body after checking that it is still retained and that
// its bytes still hash to the recorded digest.
func (s *Store) Open(record Record) (io.ReadCloser, error) {
	if !s.now().UTC().Before(record.ExpiresAt) {
		return nil, ErrExpired
	}
	raw, err := os.ReadFile(s.path(record))
	if err != nil {
		return nil, fmt.Errorf("spool: read: %w", err)
	}
	sum := sha256.Sum256(raw)
	if digestOf(sum[:]) != record.Digest {
		return nil, ErrDigestMismatch
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}

// Sweep deletes the bodies whose retention has passed and returns how many it
// removed.
func (s *Store) Sweep() (int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, fmt.Errorf("spool: list: %w", err)
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return removed, fmt.Errorf("spool: stat: %w", err)
		}
		expires, ok := expiryOf(entry.Name())
		if !ok || s.now().UTC().Before(expires) {
			continue
		}
		_ = info
		if err := os.Remove(filepath.Join(s.dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("spool: remove: %w", err)
		}
		removed++
	}
	return removed, nil
}

// path puts the expiry in the name so Sweep does not depend on the file's
// modification time. The clock that decides expiry is the one Put used, which may
// be the process clock rather than the disk's. The name is the bare hex digest:
// the colon of Digest.String() is not a legal character in a Windows filename,
// and a name the disk refuses would make every Put fail on that OS.
func (s *Store) path(record Record) string {
	return filepath.Join(s.dir, fmt.Sprintf("%s.%d.body", record.Digest.Hex(), record.ExpiresAt.UnixNano()))
}

// digestOf builds the Digest of bytes that were already hashed.
func digestOf(sum []byte) contract.Digest {
	digest, err := contract.ParseSHA256(hex.EncodeToString(sum))
	if err != nil {
		return contract.Digest{}
	}
	return digest
}

// Usage is how much the store is holding. It is what a Local Governor needs to
// decide whether to start another Task (ADR-0037 §5, #498).
type Usage struct {
	// Bodies is the number of stored bodies, expired ones included.
	Bodies int
	// Bytes is the total size of those bodies.
	Bytes int64
	// Expired is how many of them are past their retention and waiting for
	// Sweep. They are still on disk, so they still count against the host.
	Expired int
}

// Usage walks the directory and adds up what is there. It reads the size from
// the filesystem rather than from a record, so a body that a crash left behind
// is counted too: the point is the disk, not the bookkeeping.
func (s *Store) Usage() (Usage, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return Usage{}, fmt.Errorf("spool: list: %w", err)
	}
	var usage Usage
	now := s.now().UTC()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		expires, ok := expiryOf(entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return Usage{}, fmt.Errorf("spool: stat: %w", err)
		}
		usage.Bodies++
		usage.Bytes += info.Size()
		if !now.Before(expires) {
			usage.Expired++
		}
	}
	return usage, nil
}

// Within reports whether one more Task of nextBytes would still fit the host's
// budget. The Governor asks this before it starts a Task, so a host never fills
// its disk and then discovers it cannot finish (ADR-0037 §5, #498).
//
// Expired bodies count: they still occupy the disk until Sweep removes them, so
// a caller that wants the budget back should sweep first.
func (u Usage) Within(budgetBytes, nextBytes int64) error {
	if budgetBytes <= 0 {
		return fmt.Errorf("spool: no budget")
	}
	if nextBytes < 0 {
		return fmt.Errorf("spool: negative size")
	}
	if u.Bytes+nextBytes > budgetBytes {
		return fmt.Errorf("%w: holding %d of %d bytes, next task asks for %d", ErrOverBudget, u.Bytes, budgetBytes, nextBytes)
	}
	return nil
}

// expiryOf reads the expiry out of a stored body's name. A file that does not
// have the shape this package writes is left alone: it may be a temporary file
// from a Put in another process.
func expiryOf(name string) (time.Time, bool) {
	trimmed, ok := strings.CutSuffix(name, ".body")
	if !ok {
		return time.Time{}, false
	}
	index := strings.LastIndex(trimmed, ".")
	if index < 0 {
		return time.Time{}, false
	}
	nanos, err := strconv.ParseInt(trimmed[index+1:], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, nanos).UTC(), true
}
