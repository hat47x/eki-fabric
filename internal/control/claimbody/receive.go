// Package claimbody receives the body of an accepted output, after the claim
// that reported its digest, and keeps it behind the binding ADR-0034 fixes.
//
// The order is the decision: the claim is a report of a digest, the body is
// what makes the report checkable. The receiver measures what actually
// arrived and refuses the body that disagrees with the claim. Only then is
// the body kept, under its digest, for the acceptance to point at.
//
// The body is never interpreted. It is opaque bytes with a length and a
// media type a Worker declared; the receiver records the declared type and
// never acts on it.
package claimbody

import (
	"context"
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

// ErrInvalidConfig is returned for an incomplete receiver.
var ErrInvalidConfig = errors.New("invalid claim body configuration")

// ErrUnknownAttempt means no Attempt is known under this ID. The body is not
// kept: a body without an Attempt has nothing to bind to.
var ErrUnknownAttempt = errors.New("the Attempt of the body is not known")

// ErrWorkerMismatch means the body arrived from a Worker other than the one
// the Attempt's Lease named. The body is not kept.
var ErrWorkerMismatch = errors.New("the body arrived from a Worker the Attempt does not name")

// ErrNoCompletedClaim means the Attempt has no completed claim to bind this
// body to. The claim arrives first; a body that runs ahead of it has nothing
// to agree with.
var ErrNoCompletedClaim = errors.New("the Attempt has no completed claim to bind the body to")

// ErrDigestMismatch means the body received does not measure to the digest
// the completed claim reported. The body is not kept.
var ErrDigestMismatch = errors.New("the body does not measure to the digest the claim reported")

// ErrTooLarge means the body grew past the maximum the WorkOrder declared.
var ErrTooLarge = errors.New("the body exceeds the output maximum the WorkOrder declared")

// Kept is what a received body became.
type Kept struct {
	// Digest is the hex SHA-256 the receiver measured, which is the key the
	// body is kept under.
	Digest string
	// Size is the byte count the receiver measured.
	Size int64
	// MediaType is what the Worker declared. The receiver never acts on it.
	MediaType string
}

// AttemptStore reads the Attempt a body names.
type AttemptStore interface {
	LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error)
}

// ClaimStore reads the completed claim recorded for an Attempt.
type ClaimStore interface {
	LoadResultClaim(ctx context.Context, attempt ids.AttemptID) (resultclaim.Claim, bool, error)
}

// LeaseStore reads the Worker that holds the Lease an Attempt ran under.
type LeaseStore interface {
	// LoadLeaseWorker returns the Worker identity the Lease names.
	LoadLeaseWorker(ctx context.Context, id ids.LeaseID) (string, error)
}

// BodySink keeps the measured body. The reference implementation stages the
// bytes and keeps them only when the measured digest matches.
type BodySink interface {
	Put(body []byte, claimedDigest string, maxBytes int64) (digestHex string, size int64, err error)
}

// Receiver binds a received body to the claim that reported it.
type Receiver struct {
	attempts AttemptStore
	claims   ClaimStore
	leases   LeaseStore
	bodies   BodySink
}

// New returns a receiver. Every dependency is required: a receiver without
// any of them would have to trust the sender's word.
func New(attempts AttemptStore, claims ClaimStore, leases LeaseStore, bodies BodySink) (*Receiver, error) {
	switch {
	case attempts == nil, claims == nil, leases == nil, bodies == nil:
		return nil, fmt.Errorf("%w: attempt, claim, lease stores and a body sink are required", ErrInvalidConfig)
	}
	return &Receiver{attempts: attempts, claims: claims, leases: leases, bodies: bodies}, nil
}

// Receive measures the body against the completed claim of the Attempt it
// names, and keeps it only when they agree. mediaType is what the Worker
// declared; it is recorded, never acted on.
//
// The claimed digest and the maximum come from the stored claim and the
// stored Lease's WorkOrder, never from the request: a Worker that reports a
// different digest than it claimed gets a refusal, not a different binding.
func (r *Receiver) Receive(ctx context.Context, attemptID ids.AttemptID, workerIdentity string, body []byte, mediaType string) (Kept, error) {
	if err := ctx.Err(); err != nil {
		return Kept{}, err
	}
	attempt, err := r.attempts.LoadAttempt(ctx, attemptID)
	if err != nil {
		if errors.Is(err, ErrUnknownAttempt) {
			return Kept{}, ErrUnknownAttempt
		}
		return Kept{}, err
	}
	worker, err := r.leases.LoadLeaseWorker(ctx, attempt.LeaseID)
	if err != nil {
		return Kept{}, err
	}
	if worker != workerIdentity {
		return Kept{}, ErrWorkerMismatch
	}
	claim, found, err := r.claims.LoadResultClaim(ctx, attemptID)
	if err != nil {
		return Kept{}, err
	}
	if !found || claim.Status() != resultclaim.StatusCompleted {
		return Kept{}, ErrNoCompletedClaim
	}
	// The claimed digest and the byte count the claim reported are the
	// binding. The sink's maximum is the claim's own count: a body longer
	// than what was claimed cannot be what was claimed.
	digest, size, err := r.bodies.Put(body, claim.OutputDigest().Hex(), int64(claim.OutputBytes()))
	switch {
	case errors.Is(err, ErrDigestMismatch), errors.Is(err, ErrTooLarge):
		return Kept{}, err
	case err != nil:
		return Kept{}, err
	}
	return Kept{Digest: digest, Size: size, MediaType: mediaType}, nil
}
