// Package claimqueue keeps ResultClaims until the Control Plane accepts them.
//
// A Worker can be reclaimed, lose the network, or crash between finishing an
// Attempt and reporting it. The claim is evidence of what happened, so it must
// not be dropped just because the first delivery failed. The queue delivers at
// least once and relies on the Control Plane's exact-replay rule
// (resultclaim.Decide) to collapse duplicates.
package claimqueue

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var (
	ErrInvalidConfig = errors.New("invalid claim queue configuration")
	ErrConflict      = errors.New("a different claim is already queued for this attempt")
	ErrInvalidEntry  = errors.New("invalid claim queue entry")
)

// Entry is one queued claim and its delivery state.
type Entry struct {
	Claim     resultclaim.Claim
	QueuedAt  time.Time
	Attempts  uint32
	NextAt    time.Time
	LastError string
}

// Store owns the queued entries. Implementations must keep an entry until the
// queue deletes it, and must be safe for the queue's sequential use.
type Store interface {
	Load(ctx context.Context, attemptID ids.AttemptID) (Entry, bool, error)
	Save(ctx context.Context, entry Entry) error
	Due(ctx context.Context, now time.Time, limit int) ([]Entry, error)
	Delete(ctx context.Context, attemptID ids.AttemptID, digest string) error
}

// Sender delivers one claim. An error means the delivery outcome is unknown or
// failed; the queue keeps the entry and tries again later.
type Sender interface {
	Send(ctx context.Context, claim resultclaim.Claim) error
}

type Clock interface{ Now() time.Time }

type Config struct {
	Store     Store
	Sender    Sender
	Clock     Clock
	BaseDelay time.Duration
	MaxDelay  time.Duration
}

type Queue struct {
	store     Store
	sender    Sender
	clock     Clock
	baseDelay time.Duration
	maxDelay  time.Duration
}

func New(cfg Config) (*Queue, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: store is required", ErrInvalidConfig)
	case cfg.Sender == nil:
		return nil, fmt.Errorf("%w: sender is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	case cfg.BaseDelay <= 0:
		return nil, fmt.Errorf("%w: BaseDelay must be positive", ErrInvalidConfig)
	case cfg.MaxDelay < cfg.BaseDelay:
		return nil, fmt.Errorf("%w: MaxDelay must be at least BaseDelay", ErrInvalidConfig)
	}
	return &Queue{store: cfg.Store, sender: cfg.Sender, clock: cfg.Clock,
		baseDelay: cfg.BaseDelay, maxDelay: cfg.MaxDelay}, nil
}

// Enqueue records a claim for delivery. An exact replay is reported as
// alreadyQueued; a different claim for the same Attempt is rejected.
func (q *Queue) Enqueue(ctx context.Context, claim resultclaim.Claim) (alreadyQueued bool, err error) {
	if q == nil || q.store == nil || q.clock == nil {
		return false, fmt.Errorf("%w: queue is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return false, fmt.Errorf("%w: context is required", ErrInvalidConfig)
	}
	if !claim.Valid() {
		return false, resultclaim.ErrInvalidClaim
	}
	now := q.clock.Now().UTC()
	if now.IsZero() {
		return false, fmt.Errorf("%w: clock returned zero time", ErrInvalidConfig)
	}
	existing, found, err := q.store.Load(ctx, claim.AttemptID())
	if err != nil {
		return false, err
	}
	if found {
		if existing.Claim.Equal(claim) {
			return true, nil
		}
		return false, ErrConflict
	}
	return false, q.store.Save(ctx, Entry{Claim: claim, QueuedAt: now, NextAt: now})
}

// Report summarises one delivery pass.
type Report struct {
	Delivered int
	Retried   int
	Failed    int
}

// DeliverDue sends every entry whose NextAt has passed, up to limit entries. A
// failed delivery reschedules the entry with exponential backoff; the claim is
// not dropped.
func (q *Queue) DeliverDue(ctx context.Context, limit int) (Report, error) {
	if q == nil || q.store == nil || q.sender == nil || q.clock == nil {
		return Report{}, fmt.Errorf("%w: queue is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return Report{}, fmt.Errorf("%w: context is required", ErrInvalidConfig)
	}
	if limit <= 0 {
		return Report{}, fmt.Errorf("%w: limit must be positive", ErrInvalidConfig)
	}
	now := q.clock.Now().UTC()
	if now.IsZero() {
		return Report{}, fmt.Errorf("%w: clock returned zero time", ErrInvalidConfig)
	}
	due, err := q.store.Due(ctx, now, limit)
	if err != nil {
		return Report{}, err
	}
	report := Report{}
	for _, entry := range due {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if err := q.sender.Send(ctx, entry.Claim); err != nil {
			if entry.Attempts == ^uint32(0) {
				return report, fmt.Errorf("%w: retry counter exhausted", ErrInvalidEntry)
			}
			entry.Attempts++
			entry.NextAt = now.Add(q.backoff(entry.Claim, entry.Attempts))
			entry.LastError = err.Error()
			if err := q.store.Save(ctx, entry); err != nil {
				return report, err
			}
			report.Retried++
			continue
		}
		if err := q.store.Delete(ctx, entry.Claim.AttemptID(), entry.Claim.Digest().String()); err != nil {
			return report, err
		}
		report.Delivered++
	}
	return report, nil
}

// backoff grows exponentially and stays within MaxDelay. The delay is derived
// from the claim digest so that a Worker restart keeps the same schedule rather
// than resetting it.
func (q *Queue) backoff(claim resultclaim.Claim, attempts uint32) time.Duration {
	if attempts == 0 {
		return 0
	}
	delay := q.baseDelay
	for i := uint32(1); i < attempts; i++ {
		if delay >= q.maxDelay/2 {
			return q.maxDelay
		}
		delay *= 2
		if delay >= q.maxDelay {
			return q.maxDelay
		}
	}
	if delay <= 1 {
		return delay
	}
	// Equal jitter: keep at least half of the delay and spread the rest. A
	// claim must not be retried in a tight loop, but many Workers reporting at
	// once should not arrive in lockstep either.
	half := delay / 2
	spread := uint64(time.Duration(delay-half) + 1)
	return half + time.Duration(jitterValue(claim.Digest().String(), attempts, spread))
}

func jitterValue(seed string, attempts uint32, spread uint64) uint64 {
	if spread == 0 {
		return 0
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("eki.claim-queue/v1\x00%s\x00%d", seed, attempts)))
	return binary.BigEndian.Uint64(h[:8]) % spread
}

// Len reports how many entries are queued. It is used for tests and diagnostics.
func (q *Queue) Len(ctx context.Context) (int, error) {
	if q == nil || q.store == nil {
		return 0, fmt.Errorf("%w: queue is not initialized", ErrInvalidConfig)
	}
	due, err := q.store.Due(ctx, q.clock.Now().UTC().Add(365*24*time.Hour), 1<<20)
	if err != nil {
		return 0, err
	}
	return len(due), nil
}
