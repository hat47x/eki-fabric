// Package fs stores artifact bodies in a directory, named by their digest
// (ADR-0029 §5.11).
//
// A body is immutable and named by its content, so the same bytes are stored once
// and no record of "which Task produced it" is needed. Writing is atomic: a reader
// sees either the previous body or the complete new one, never a partial file.
package fs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ErrDigestMismatch means the bytes written do not hash to the digest they were
// offered under. Nothing is stored: the name would then describe other content.
var ErrDigestMismatch = errors.New("fs: artifact body does not match its digest")

// ErrNotFound means this store holds no body for the digest.
var ErrNotFound = errors.New("fs: artifact body not found")

// Store keeps bodies under one directory.
type Store struct {
	// Dir is the directory bodies are kept in. It is created on first write.
	Dir string
}

// Put stores the body under its digest and returns its size.
//
// The digest is checked while the bytes are written: a body that does not hash to
// the name is refused and removed, so a caller cannot file content under a name it
// did not earn.
func (s Store) Put(ctx context.Context, digest contract.Digest, body io.Reader) (int64, error) {
	if s.Dir == "" {
		return 0, errors.New("fs: the artifact directory is not configured")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return 0, fmt.Errorf("fs: create artifact directory: %w", err)
	}
	tmp, err := os.CreateTemp(s.Dir, ".artifact-*")
	if err != nil {
		return 0, fmt.Errorf("fs: write artifact: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hasher), body)
	if err != nil {
		_ = tmp.Close()
		return 0, fmt.Errorf("fs: write artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("fs: write artifact: %w", err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], hasher.Sum(nil))
	if got := contract.FromSHA256(sum); got.Hex() != digest.Hex() {
		return 0, ErrDigestMismatch
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return 0, fmt.Errorf("fs: write artifact: %w", err)
	}
	if err := os.Rename(name, filepath.Join(s.Dir, digest.Hex())); err != nil {
		return 0, fmt.Errorf("fs: write artifact: %w", err)
	}
	return size, nil
}

// Open returns the body and its size. It implements the Worker Plane's
// ArtifactSource, so a deployment that keeps bodies here can serve them as they are.
func (s Store) Open(ctx context.Context, digest contract.Digest) (io.ReadCloser, int64, error) {
	if s.Dir == "" {
		return nil, 0, errors.New("fs: the artifact directory is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	file, err := os.Open(filepath.Join(s.Dir, digest.Hex()))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("fs: open artifact: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("fs: open artifact: %w", err)
	}
	return file, info.Size(), nil
}

// Remove deletes the body. A body that is already gone is not an error: removal is
// what a sweep asks for, not a check that the body exists.
func (s Store) Remove(ctx context.Context, digest contract.Digest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(s.Dir, digest.Hex()))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("fs: remove artifact: %w", err)
	}
	return nil
}

var _ = 0 // kept for the package comment above; no exported sentinel is needed.

// Resolve implements admission.ArtifactResolver: an input reference is accepted when
// it is the digest of a body this deployment holds (ADR-0029 §5.12).
//
// The identifier is not parsed as anything but digest hex. A deployment that issues
// other identifiers needs a registry; until then, refusing is the honest answer.
func (s Store) Resolve(ctx context.Context, artifactID string) (contract.Digest, error) {
	digest, err := contract.ParseSHA256(artifactID)
	if err != nil {
		return contract.Digest{}, fmt.Errorf("%w: %q is not a digest", admission.ErrUnresolvableArtifact, artifactID)
	}
	body, _, err := s.Open(ctx, digest)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return contract.Digest{}, fmt.Errorf("%w: no body for %s", admission.ErrUnresolvableArtifact, digest.Hex())
		}
		return contract.Digest{}, err
	}
	_ = body.Close()
	return digest, nil
}
