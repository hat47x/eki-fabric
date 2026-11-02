// Package artifactstore keeps the body of an accepted output behind the
// content-addressed binding ADR-0034 fixes: the SHA-256 of the bytes is the
// key, the record that points at the body is the claim, and the re-computation
// happens on the way in and on the way out.
//
// The store never interprets a body. It is written as opaque bytes under a
// name made from its digest, and served back as a download surface only.
// Nothing here trusts the sender's word about the digest: Put measures the
// bytes it receives and refuses the write when the measured digest disagrees
// with the claimed one.
package artifactstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrDigestMismatch means the body received does not match the digest claimed
// for it. The write is refused and nothing is kept.
var ErrDigestMismatch = errors.New("artifact body does not match the claimed digest")

// ErrTooLarge means the body grew past the declared maximum while it was being
// read. The write is refused and nothing is kept.
var ErrTooLarge = errors.New("artifact body exceeds the declared maximum")

// ErrNotFound means no body is kept under this digest.
var ErrNotFound = errors.New("artifact body not found")

// MaxDigestHex is the length of a hex-encoded SHA-256 digest.
const MaxDigestHex = 64

// Store keeps artifact bodies as files named by their SHA-256 digest, under
// one root directory. The same body written twice is written once.
type Store struct {
	root string
}

// New returns a store rooted at dir. The directory is created if missing.
func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("artifactstore: root directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("artifactstore: create root: %w", err)
	}
	return &Store{root: dir}, nil
}

// Root is the directory the store keeps bodies under.
func (s *Store) Root() string { return s.root }

// Put reads the whole body from r, measures its SHA-256 and its size, and
// keeps the bytes only when the measured digest equals claimed. maxBytes caps
// the read; zero means no cap. The body is staged under a temporary name and
// renamed into place only after the digest matched, so a refused write leaves
// nothing behind.
func (s *Store) Put(r io.Reader, claimed string, maxBytes int64) (digestHex string, size int64, err error) {
	digest, err := parseDigestHex(claimed)
	if err != nil {
		return "", 0, err
	}
	if maxBytes < 0 {
		return "", 0, fmt.Errorf("artifactstore: negative maximum %d", maxBytes)
	}

	staging, err := os.CreateTemp(s.root, ".staging-*")
	if err != nil {
		return "", 0, fmt.Errorf("artifactstore: stage: %w", err)
	}
	stagingName := staging.Name()
	defer func() {
		// A refused write leaves no staging file behind.
		_ = os.Remove(stagingName)
	}()

	hash := sha256.New()
	measured, err := io.Copy(io.MultiWriter(staging, hash), &limited{r: r, max: maxBytes})
	if err != nil {
		staging.Close()
		return "", 0, err
	}
	if err := staging.Close(); err != nil {
		return "", 0, fmt.Errorf("artifactstore: stage: %w", err)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), digest) {
		return "", 0, ErrDigestMismatch
	}

	final := s.path(digest)
	if err := os.Rename(stagingName, final); err != nil {
		return "", 0, fmt.Errorf("artifactstore: keep: %w", err)
	}
	return digest, measured, nil
}

// Open returns a reader over the body kept under digestHex. The caller
// re-computes the digest over what it reads: this store binds nothing on the
// way out, it only hands back the bytes it kept.
func (s *Store) Open(digestHex string) (io.ReadSeekCloser, int64, error) {
	digest, err := parseDigestHex(digestHex)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(s.path(digest))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("artifactstore: open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("artifactstore: stat: %w", err)
	}
	return f, info.Size(), nil
}

// path maps a digest to the file name it is kept under. The name is made from
// the digest alone: no untrusted word becomes part of a path.
func (s *Store) path(digestHex string) string {
	return filepath.Join(s.root, strings.ToLower(digestHex)+".body")
}

// parseDigestHex accepts a hex-encoded SHA-256 digest and nothing else.
func parseDigestHex(claimed string) (string, error) {
	trimmed := strings.TrimSpace(claimed)
	if len(trimmed) != MaxDigestHex {
		return "", fmt.Errorf("artifactstore: digest %q is not a hex SHA-256", claimed)
	}
	if _, err := hex.DecodeString(trimmed); err != nil {
		return "", fmt.Errorf("artifactstore: digest %q is not hex", claimed)
	}
	return strings.ToLower(trimmed), nil
}

// limited stops reading once max bytes have passed. Reading past the cap is
// an error, not a silent truncation: a truncated body can never match the
// claimed digest, and the difference between "too large" and "wrong bytes"
// is worth keeping.
type limited struct {
	r      io.Reader
	max    int64
	seen   int64
	capped bool
}

func (l *limited) Read(p []byte) (int, error) {
	if l.capped {
		return 0, ErrTooLarge
	}
	if l.max > 0 && l.seen >= l.max {
		l.capped = true
		// One byte is read past the cap to see whether the body truly
		// continues; a body of exactly max bytes must be accepted.
		if _, err := l.r.Read(p[:1]); err == io.EOF {
			return 0, io.EOF
		}
		return 0, ErrTooLarge
	}
	n, err := l.r.Read(p)
	l.seen += int64(n)
	if l.max > 0 && l.seen > l.max {
		l.capped = true
		return int(int64(n) - (l.seen - l.max)), nil
	}
	return n, err
}
