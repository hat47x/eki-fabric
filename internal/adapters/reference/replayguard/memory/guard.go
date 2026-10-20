// Package memory is an in-process reference replay guard.
//
// It is useful for tests and for a single Worker process that has no durable
// store yet. It does not survive a restart, so it is not Worker execution
// evidence on its own (RQ-048).
package memory

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hat47x/eki-fabric/internal/spi/replayguard"
)

var (
	ErrInvalidGuard  = errors.New("invalid replay guard configuration")
	ErrInvalidNonce  = errors.New("invalid nonce claim")
	ErrGuardCapacity = errors.New("replay guard is full")
)

const (
	MinNonceBytes = 16
	MaxNonceBytes = 32
)

type Clock interface{ Now() time.Time }

type Guard struct {
	mu         sync.Mutex
	clock      Clock
	entries    map[string]time.Time
	maxEntries int
}

var _ replayguard.Guard = (*Guard)(nil)

// New creates a guard that keeps at most maxEntries live nonces. A full guard
// fails closed instead of evicting a live nonce.
func New(clock Clock, maxEntries int) (*Guard, error) {
	if clock == nil {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidGuard)
	}
	if maxEntries <= 0 {
		return nil, fmt.Errorf("%w: maxEntries must be positive", ErrInvalidGuard)
	}
	return &Guard{clock: clock, entries: make(map[string]time.Time, maxEntries), maxEntries: maxEntries}, nil
}

// Claim records the nonce until the given time. The claim expiry is the time
// the nonce stops being replayable; callers should pass the earliest time at
// which the WorkOrder and its Lease both expire.
func (g *Guard) Claim(ctx context.Context, nonce []byte, until time.Time) (bool, error) {
	if g == nil || g.clock == nil || g.entries == nil {
		return false, fmt.Errorf("%w: guard is not initialized", ErrInvalidGuard)
	}
	if ctx == nil {
		return false, fmt.Errorf("%w: context is required", ErrInvalidNonce)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(nonce) < MinNonceBytes || len(nonce) > MaxNonceBytes {
		return false, fmt.Errorf("%w: nonce must contain %d..%d bytes", ErrInvalidNonce, MinNonceBytes, MaxNonceBytes)
	}
	now := g.clock.Now()
	if now.IsZero() {
		return false, fmt.Errorf("%w: clock returned zero time", ErrInvalidNonce)
	}
	if until.IsZero() || !until.After(now) {
		return false, fmt.Errorf("%w: claim expiry must be after now", ErrInvalidNonce)
	}

	key := nonceKey(nonce)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.purge(now)
	if expiry, ok := g.entries[key]; ok && expiry.After(now) {
		return false, nil
	}
	if _, ok := g.entries[key]; !ok && len(g.entries) >= g.maxEntries {
		return false, fmt.Errorf("%w: %d live nonces", ErrGuardCapacity, len(g.entries))
	}
	g.entries[key] = until.UTC()
	return true, nil
}

// purge removes claims whose expiry has passed. An entry that expires exactly
// at now is no longer live and may be reused.
func (g *Guard) purge(now time.Time) {
	for key, expiry := range g.entries {
		if !expiry.After(now) {
			delete(g.entries, key)
		}
	}
}

func nonceKey(nonce []byte) string {
	sum := sha256.Sum256(nonce)
	return string(sum[:])
}
