package resourceobservationreadonly

import (
	"errors"
	"fmt"
	"time"
)

const Schema = "eki.resource-observation/v0"

// Observation is a read-only claim about a provider's current state.
// It is not a placement decision.
type Observation struct {
	Schema         string
	ProviderID     string
	Capability     Capability
	State          ProviderState
	ObservedAt     time.Time
	Staleness      time.Duration
	CostCandidate  CostCandidate
	TrustLevel     string
	DataClasses    []string
}

// Capability describes what the provider can run.
type Capability struct {
	CPUMillis       int64
	MemoryBytes     int64
	WallTimeSeconds int64
	GPUCount        int64
	CacheLocality   string
	RuntimeClass    string
}

// ProviderState is the observed state.
type ProviderState struct {
	AvailableSlots int64
	UtilizedSlots  int64
	Status         string
}

// CostCandidate is a cost estimate for placement.
type CostCandidate struct {
	Currency       string
	UnitCost       float64
	EstimatedTotal float64
}

// SnapshotStore is a read-only snapshot of provider observations.
type SnapshotStore struct {
	now      func() time.Time
	readonly bool
	entries  map[string]Observation
}

// NewSnapshotStore creates a new snapshot store.
func NewSnapshotStore(now func() time.Time) *SnapshotStore {
	return &SnapshotStore{
		now:     now,
		entries: make(map[string]Observation),
	}
}

// Set replaces the observation for a provider.
func (s *SnapshotStore) Set(obs Observation) error {
	if !s.readonly {
		s.entries[obs.ProviderID] = obs
		return nil
	}
	return errors.New("snapshot store is read-only")
}

// Get returns the observation for a provider.
func (s *SnapshotStore) Get(providerID string) (Observation, bool) {
	obs, ok := s.entries[providerID]
	return obs, ok
}

// List returns all observations.
func (s *SnapshotStore) List() []Observation {
	out := make([]Observation, 0, len(s.entries))
	for _, obs := range s.entries {
		out = append(out, obs)
	}
	return out
}

// SetReadOnly makes the snapshot store read-only.
func (s *SnapshotStore) SetReadOnly() {
	s.readonly = true
}

// IsReadOnly reports whether the snapshot store is read-only.
func (s *SnapshotStore) IsReadOnly() bool {
	return s.readonly
}

// IsFresh reports whether the observation is fresh.
func (s *SnapshotStore) IsFresh(obs Observation) bool {
	return s.now().Sub(obs.ObservedAt) <= obs.Staleness
}

// Validate checks the observation fields.
func (o Observation) Validate() error {
	if o.ProviderID == "" {
		return fmt.Errorf("%w: provider id is empty", ErrInvalidObservation)
	}
	if o.Schema != Schema {
		return fmt.Errorf("%w: schema is %q", ErrInvalidObservation, o.Schema)
	}
	if o.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed at is zero", ErrInvalidObservation)
	}
	return nil
}

// ErrInvalidObservation means the observation is incomplete.
var ErrInvalidObservation = errors.New("invalid observation")
