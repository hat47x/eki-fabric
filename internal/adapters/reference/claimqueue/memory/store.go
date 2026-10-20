// Package memory is an in-process claim queue store.
//
// It keeps queued claims across Agent restarts only in the sense that the
// process keeps running; a real restart loses them. It is the reference
// implementation for tests and for a single long-lived Agent process.
package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var ErrInvalidEntry = errors.New("invalid claim queue entry")

type Store struct {
	mu      sync.Mutex
	entries map[ids.AttemptID]claimqueue.Entry
}

var _ claimqueue.Store = (*Store)(nil)

func New() *Store { return &Store{entries: map[ids.AttemptID]claimqueue.Entry{}} }

func (s *Store) Load(ctx context.Context, attemptID ids.AttemptID) (claimqueue.Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return claimqueue.Entry{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[attemptID]
	return entry, ok, nil
}

func (s *Store) Save(ctx context.Context, entry claimqueue.Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validate(entry); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[entry.Claim.AttemptID()] = entry
	return nil
}

func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]claimqueue.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be positive", ErrInvalidEntry)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]claimqueue.Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		if !entry.NextAt.After(now) {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].NextAt.Equal(out[j].NextAt) {
			return out[i].NextAt.Before(out[j].NextAt)
		}
		return out[i].Claim.AttemptID() < out[j].Claim.AttemptID()
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, attemptID ids.AttemptID, digest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[attemptID]
	if !ok {
		return nil
	}
	if entry.Claim.Digest().String() != digest {
		return nil
	}
	delete(s.entries, attemptID)
	return nil
}

func validate(entry claimqueue.Entry) error {
	switch {
	case !entry.Claim.Valid():
		return fmt.Errorf("%w: claim is not constructed", ErrInvalidEntry)
	case entry.QueuedAt.IsZero():
		return fmt.Errorf("%w: queued time is required", ErrInvalidEntry)
	case entry.NextAt.IsZero():
		return fmt.Errorf("%w: next attempt time is required", ErrInvalidEntry)
	}
	return nil
}
