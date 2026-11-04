// Package leaserecall takes back leases whose window has passed (#555).
//
// It is deliberately split from storage: the question "which leases are done" is
// answered by leasestate.Reclaimable, and this package only asks a lister for the
// open leases, decides, and hands each one to a reclaimer. That keeps the loop
// testable without a database and keeps the state transition in one place.
package leaserecall

import (
	"context"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

// Lister returns the leases that are still recorded as open. A lease that has
// already been reclaimed must not be listed again: reclaiming twice would take a
// renewed lease away from a Worker that is working.
type Lister interface {
	OpenLeases(ctx context.Context) ([]leasestate.LeaseRecord, error)
}

// Reclaimer performs the transition for one lease. It is called once per lease and
// its error stops the sweep: continuing after a failed transition could reclaim a
// lease whose Task moved on in the meantime.
type Reclaimer interface {
	Reclaim(ctx context.Context, lease leasestate.LeaseRecord, reason string) error
}

// Service is one sweep of the Control Plane's reclaim loop.
type Service struct {
	lister    Lister
	reclaimer Reclaimer
	now       func() time.Time
	onReclaim func(reason string)
}

// Config is what a Service needs.
type Config struct {
	Lister    Lister
	Reclaimer Reclaimer
	// Now is the clock. Nil means the system clock.
	Now func() time.Time
	// OnReclaim receives the reason of each reclaimed lease. It is how ADR-0039's
	// counter connects without this package depending on measurement.
	OnReclaim func(reason string)
}

// New returns a Service.
func New(cfg Config) (*Service, error) {
	if cfg.Lister == nil || cfg.Reclaimer == nil {
		return nil, fmt.Errorf("leaserecall: a lister and a reclaimer are required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{lister: cfg.Lister, reclaimer: cfg.Reclaimer, now: now, onReclaim: cfg.OnReclaim}, nil
}

// Sweep reclaims every lease that is no longer active and reports how many it
// took back.
//
// A lister failure is returned without reclaiming anything: a partial list would
// reclaim some leases and silently skip the rest, which reads as "nothing else was
// due".
func (s *Service) Sweep(ctx context.Context) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("leaserecall: service is not initialized")
	}
	open, err := s.lister.OpenLeases(ctx)
	if err != nil {
		return 0, fmt.Errorf("leaserecall: list: %w", err)
	}
	taken := leasestate.Reclaimable(open, s.now())
	for _, candidate := range taken {
		if err := s.reclaimer.Reclaim(ctx, candidate.Lease, candidate.Reason); err != nil {
			return 0, fmt.Errorf("leaserecall: reclaim %s: %w", candidate.Lease.ID, err)
		}
		if s.onReclaim != nil {
			s.onReclaim(candidate.Reason)
		}
	}
	return len(taken), nil
}
