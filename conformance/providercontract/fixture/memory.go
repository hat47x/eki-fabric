// Package fixture holds in-memory provider adapters for the provider contract
// suite. Nothing here performs network I/O or talks to a cluster. Each fixture
// models the documented default behaviour of one kind of system. The simulation
// methods on Memory decide what that system does next, so the suite can check the
// adapter's reports against the declared defaults.
package fixture

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resourceprovider"
)

// ErrLeaseFinished is returned by a simulation method on a lease whose attempt has
// already ended.
var ErrLeaseFinished = errors.New("fixture: lease attempt already finished")

// Config describes one fixture.
type Config struct {
	// Declaration is what the adapter declares. The fixture behaves according to
	// it. A declaration that the fixture does not follow would be a bug in the fixture.
	Declaration resourceprovider.Declaration
	// RetryLimit is the number of automatic retries a lease gets while automatic
	// retry is on. Zero means the fixture does not model a count limit.
	RetryLimit int
}

// Memory is an in-memory ProviderAdapter. Its simulation methods drive the
// underlying system that the fixture stands for.
type Memory struct {
	mu         sync.Mutex
	decl       resourceprovider.Declaration
	retryLimit int
	leases     map[ids.LeaseID]*lease
	nextSlot   int
}

type lease struct {
	id       ids.LeaseID
	slots    []resourceprovider.SlotRef
	retryOff bool
	retries  int
	events   []resourceprovider.Event
	finished bool
}

// New returns a fixture for cfg. The declaration must be valid.
func New(cfg Config) (*Memory, error) {
	if err := resourceprovider.ValidateDeclaration(cfg.Declaration); err != nil {
		return nil, err
	}
	if cfg.RetryLimit < 0 {
		return nil, errors.New("fixture: negative retry limit")
	}
	return &Memory{
		decl:       cfg.Declaration,
		retryLimit: cfg.RetryLimit,
		leases:     make(map[ids.LeaseID]*lease),
	}, nil
}

// Declare returns a copy of the declaration.
func (m *Memory) Declare() resourceprovider.Declaration {
	d := m.decl
	d.Credential.Providers = slices.Clone(d.Credential.Providers)
	return d
}

// Allocate reserves the requested slots for one lease and reports that the work has started.
func (m *Memory) Allocate(ctx context.Context, req resourceprovider.AllocationRequest) (resourceprovider.Allocation, error) {
	if err := ctx.Err(); err != nil {
		return resourceprovider.Allocation{}, err
	}
	if err := resourceprovider.ValidateAllocationRequest(req); err != nil {
		return resourceprovider.Allocation{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// The fixture refuses a per-submission disable that its declaration does not
	// allow. It never silently keeps the retry on.
	if req.Retry == resourceprovider.RetryDisabled && !m.decl.Retry.DisableablePerSubmission {
		return resourceprovider.Allocation{}, fmt.Errorf("%w: provider %q", resourceprovider.ErrRetryNotDisableable, m.decl.Provider)
	}
	if _, ok := m.leases[req.LeaseID]; ok {
		return resourceprovider.Allocation{}, resourceprovider.ErrLeaseAlreadyAllocated
	}

	slots := make([]resourceprovider.SlotRef, 0, req.Slots)
	for range req.Slots {
		m.nextSlot++
		slots = append(slots, resourceprovider.SlotRef(fmt.Sprintf("%s-slot-%04d", m.decl.Provider, m.nextSlot)))
	}
	l := &lease{
		id:       req.LeaseID,
		slots:    slots,
		retryOff: req.Retry == resourceprovider.RetryDisabled,
	}
	m.leases[req.LeaseID] = l
	m.emit(l, resourceprovider.EventStarted)

	return resourceprovider.Allocation{LeaseID: req.LeaseID, Slots: slices.Clone(slots)}, nil
}

// Events returns the events for lease whose Sequence is greater than after.
func (m *Memory) Events(ctx context.Context, id ids.LeaseID, after uint64) ([]resourceprovider.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	l, ok := m.leases[id]
	if !ok {
		return nil, resourceprovider.ErrUnknownLease
	}
	var out []resourceprovider.Event
	for _, ev := range l.events {
		if ev.Sequence > after {
			out = append(out, ev)
		}
	}
	return out, nil
}

// CompleteAttempt makes the underlying work report success. The lease then ends.
func (m *Memory) CompleteAttempt(id ids.LeaseID) error {
	return m.finish(id, resourceprovider.EventCompleted)
}

// PreemptAttempt makes the provider take the slot back. The lease ends. No failure
// is reported.
func (m *Memory) PreemptAttempt(id ids.LeaseID) error {
	return m.finish(id, resourceprovider.EventPreempted)
}

// FailAttempt makes the underlying work fail.
//
// When automatic retry is on for this lease, the provider retries the work inside
// the same attempt. It reports Requeued and then Started again, until RetryLimit
// retries have been used. Once the limit is reached, or when retry is off, the
// failure is final and the lease ends.
func (m *Memory) FailAttempt(id ids.LeaseID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, err := m.live(id)
	if err != nil {
		return err
	}
	retryOn := m.decl.Retry.OnByDefault && !l.retryOff
	if retryOn && (m.retryLimit == 0 || l.retries < m.retryLimit) {
		l.retries++
		m.emit(l, resourceprovider.EventRequeued)
		m.emit(l, resourceprovider.EventStarted)
		return nil
	}
	m.emit(l, resourceprovider.EventFailed)
	l.finished = true
	return nil
}

// Retries returns how many automatic retries the lease has used. It exists for
// tests that check a documented retry count.
func (m *Memory) Retries(id ids.LeaseID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, ok := m.leases[id]
	if !ok {
		return 0, resourceprovider.ErrUnknownLease
	}
	return l.retries, nil
}

func (m *Memory) finish(id ids.LeaseID, kind resourceprovider.EventKind) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, err := m.live(id)
	if err != nil {
		return err
	}
	m.emit(l, kind)
	l.finished = true
	return nil
}

// live returns the lease if its attempt has not ended. The caller must hold m.mu.
func (m *Memory) live(id ids.LeaseID) (*lease, error) {
	l, ok := m.leases[id]
	if !ok {
		return nil, resourceprovider.ErrUnknownLease
	}
	if l.finished {
		return nil, ErrLeaseFinished
	}
	return l, nil
}

// emit appends one event to the lease. The caller must hold m.mu.
func (m *Memory) emit(l *lease, kind resourceprovider.EventKind) {
	l.events = append(l.events, resourceprovider.Event{
		LeaseID:  l.id,
		Sequence: uint64(len(l.events)) + 1,
		Kind:     kind,
	})
}
