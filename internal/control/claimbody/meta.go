package claimbody

import (
	"context"
	"fmt"
	"time"
)

// MetaSink records what a retrieval needs besides the bytes: who may read the
// body and until when (#494, #514). It is optional so a deployment that keeps
// no bodies keeps working.
type MetaSink interface {
	// PutMeta records the metadata for the body kept under digestHex.
	PutMeta(digestHex string, owner string, size int64, mediaType string, expiresAt time.Time) error
}

// OwnerStore answers which Principal submitted the Task an Attempt ran for. It
// is the authority for "who may read this body", so it is read rather than
// guessed from the Worker identity.
type OwnerStore interface {
	LoadAttemptOwner(ctx context.Context, attemptID string) (string, error)
}

// WithMeta returns a receiver that also records metadata for every body it
// keeps. Both arguments are required together: recording who may read a body
// without knowing the owner would record a body nobody is allowed to read.
func (r *Receiver) WithMeta(owners OwnerStore, meta MetaSink, ttl time.Duration) (*Receiver, error) {
	switch {
	case owners == nil, meta == nil:
		return nil, fmt.Errorf("%w: both an owner store and a metadata sink are required", ErrInvalidConfig)
	case ttl <= 0:
		return nil, fmt.Errorf("%w: retention must be positive", ErrInvalidConfig)
	}
	r.owners = owners
	r.meta = meta
	r.retention = ttl
	return r, nil
}

// recordMeta stores the metadata for a body that was just kept. A failure here
// is reported rather than swallowed: without the record the body could never be
// served, so keeping it silently would fill the disk with bytes nobody can read.
func (r *Receiver) recordMeta(ctx context.Context, attemptID string, digestHex string, size int64, mediaType string) error {
	if r.meta == nil || r.owners == nil {
		return nil
	}
	owner, err := r.owners.LoadAttemptOwner(ctx, attemptID)
	if err != nil {
		return fmt.Errorf("claimbody: owner of attempt: %w", err)
	}
	if owner == "" {
		return fmt.Errorf("claimbody: the attempt has no owner")
	}
	return r.meta.PutMeta(digestHex, owner, size, mediaType, time.Now().UTC().Add(r.retention))
}
