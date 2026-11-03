package leaserecall

import (
	"context"
	"fmt"
	"sync"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

// Memory is a lease registry that keeps the open leases in this process. It makes
// the recall loop usable now: leases are not persisted yet, so without it the
// sweep had no list to read (#555).
//
// It is not durable. A restart loses the open leases, which is acceptable only
// while the Control Plane also loses the Tasks they belong to; when leases are
// written to the state store, that store implements the same two methods.
type Memory struct {
	mu     sync.Mutex
	leases map[ids.LeaseID]leasestate.LeaseRecord
}

// NewMemory returns an empty registry.
func NewMemory() *Memory {
	return &Memory{leases: map[ids.LeaseID]leasestate.LeaseRecord{}}
}

// Open records a lease as open. Recording the same lease twice keeps the later
// value: a renewal is the same lease with a later expiry.
func (m *Memory) Open(lease leasestate.LeaseRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leases[lease.ID] = lease
}

// OpenLeases returns the leases this registry still holds.
func (m *Memory) OpenLeases(context.Context) ([]leasestate.LeaseRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]leasestate.LeaseRecord, 0, len(m.leases))
	for _, lease := range m.leases {
		out = append(out, lease)
	}
	return out, nil
}

// Reclaim takes a lease back. Reclaiming one that is not open is an error rather
// than a silent success: the sweep stops on it, and "already reclaimed" is
// information the operator should see.
func (m *Memory) Reclaim(_ context.Context, lease leasestate.LeaseRecord, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.leases[lease.ID]; !ok {
		return fmt.Errorf("leaserecall: lease %s is not open", lease.ID)
	}
	delete(m.leases, lease.ID)
	return nil
}
