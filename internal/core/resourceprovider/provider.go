// Package resourceprovider states the contract that a managed resource provider
// must meet before EKI treats its worker slots as part of the fabric. A provider
// is a Kubernetes cluster, a Slurm partition, or a similar system that keeps its
// own placement and node management (ADR-0009).
//
// The package has two parts. ProviderAdapter is the interface that a concrete
// adapter implements. It asks the provider for slots under a Lease, reads the
// events the provider reports, and exposes the provider's declared retry
// behaviour. The other parts are pure functions that encode five rules:
//
//   - (a) A task whose retry is unsafe is placed only on a provider whose
//     automatic retry can be disabled per submission (PlaceWithRetryRule).
//   - (b) A provider preemption is a reclaim, not a failure (Classify).
//   - (c) A provider's report of success is a claim, not an accepted result
//     (ClaimFromEvent).
//   - (d) A provider's declared managed status does not raise assurance. Only an
//     administrator policy can (ManagedForPlacement).
//   - (e) An adapter's credential is scoped to one provider (ValidateCredentialScope).
//
// The package performs no I/O, reads no clock, and starts no goroutines.
//
// Limitation: this package and its conformance suite are a contract-level
// simulation. Nothing here reaches a network or a real cluster. A passing suite
// shows that the fixtures follow the contract. It does not show that a real
// Kubernetes or Slurm installation behaves the same way. Issue #67 stays open
// until the adapter runs against real clusters.
package resourceprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// MaxSlotsPerAllocation bounds how many worker slots one allocation request may ask for.
const MaxSlotsPerAllocation = 256

var (
	// ErrInvalidDeclaration is returned for a declaration that cannot be evaluated.
	ErrInvalidDeclaration = errors.New("resourceprovider: invalid declaration")
	// ErrInvalidRequest is returned for an allocation request that cannot be evaluated.
	ErrInvalidRequest = errors.New("resourceprovider: invalid allocation request")
	// ErrRetryNotDisableable is returned when a task that must not be retried
	// would run on a provider that cannot disable automatic retry per submission.
	ErrRetryNotDisableable = errors.New("resourceprovider: automatic retry cannot be disabled per submission")
	// ErrLeaseAlreadyAllocated is returned when the same lease is allocated twice.
	ErrLeaseAlreadyAllocated = errors.New("resourceprovider: lease already allocated")
	// ErrUnknownLease is returned for a lease that the adapter has no allocation for.
	ErrUnknownLease = errors.New("resourceprovider: unknown lease")
	// ErrUnknownEvent is returned for an event kind outside the closed set.
	ErrUnknownEvent = errors.New("resourceprovider: unknown event kind")
	// ErrCredentialScope is returned when a credential reaches more than one
	// provider, is cluster-wide, or has no partition bound.
	ErrCredentialScope = errors.New("resourceprovider: credential scope is wider than one provider partition")
)

// ProviderID names one provider: a cluster namespace, a Slurm partition, or a
// similar unit that an administrator manages. It is a stable identifier, not a
// display name.
type ProviderID string

// RetryDeclaration is the provider's statement about automatic retry. This is a
// retry the provider performs on its own, without a new request from EKI. Such a
// retry can repeat a side effect, because EKI does not see it as a new Attempt
// (research/integration/orchestrator-integration-and-layering.md section 5.1, RQ-046).
//
// The declaration is a claim. Conformance checks it against the observed
// behaviour of the adapter.
type RetryDeclaration struct {
	// OnByDefault is true when the provider retries without any setting from EKI.
	OnByDefault bool
	// DisableablePerSubmission is true when EKI can turn automatic retry off for
	// one submission.
	DisableablePerSubmission bool
}

// Declaration is what an adapter states about its provider. Placement reads it,
// and conformance checks it.
type Declaration struct {
	Provider ProviderID
	Retry    RetryDeclaration
	// DeclaredManaged is the provider's own claim that an administrator manages
	// it. It is kept for audit. Placement reads managed status only through
	// ManagedForPlacement, so this field never raises assurance by itself.
	DeclaredManaged bool
	// Credential is the scope of the credential that the adapter uses to reach Provider.
	Credential CredentialScope
}

// RetrySetting is the automatic retry setting that one submission carries.
type RetrySetting string

const (
	// RetryProviderDefault leaves automatic retry to the provider's default.
	RetryProviderDefault RetrySetting = "provider-default"
	// RetryDisabled turns automatic retry off for the submission.
	RetryDisabled RetrySetting = "disabled"
)

// EventKind is one of the five events that a provider reports about a lease.
type EventKind string

const (
	EventStarted   EventKind = "started"
	EventCompleted EventKind = "completed"
	EventFailed    EventKind = "failed"
	EventPreempted EventKind = "preempted"
	EventRequeued  EventKind = "requeued"
)

// Event is one report from the provider about a lease. Sequence starts at 1 for
// each lease and grows by one for each event, so a reader can resume from the
// last Sequence it has seen.
type Event struct {
	LeaseID  ids.LeaseID
	Sequence uint64
	Kind     EventKind
}

// SlotRef is an opaque identifier for one worker slot inside the provider.
type SlotRef string

// AllocationRequest asks a provider for worker slots under one lease.
type AllocationRequest struct {
	LeaseID ids.LeaseID
	Slots   int
	// Retry is the output of PlaceWithRetryRule. The adapter does not choose it.
	Retry RetrySetting
}

// Allocation is the provider's answer to an AllocationRequest.
type Allocation struct {
	LeaseID ids.LeaseID
	// Slots are opaque. They must not reveal node names, addresses, or
	// namespaces to Client-facing callers (RQ-018).
	Slots []SlotRef
}

// ProviderAdapter is the boundary between EKI and one provider. It belongs to
// the Extension SPI layer (EP-2) and never enters the Core state machine. Callers
// must pass a non-nil context to every method.
type ProviderAdapter interface {
	// Declare returns the adapter's declaration. It must not change over the life
	// of the adapter.
	Declare() Declaration
	// Allocate reserves req.Slots worker slots for req.LeaseID. An adapter must
	// refuse a request with RetryDisabled unless its declaration allows disabling
	// retry per submission.
	Allocate(ctx context.Context, req AllocationRequest) (Allocation, error)
	// Events returns the events reported for lease whose Sequence is greater than
	// after, in Sequence order. Events for other leases are never returned. When
	// the provider is unreachable, events can fail to arrive. Callers must not read
	// that silence as the work having stopped.
	Events(ctx context.Context, lease ids.LeaseID, after uint64) ([]Event, error)
}

// ValidateDeclaration checks the parts of a declaration that do not depend on the
// provider's behaviour: the identifier and the credential scope.
func ValidateDeclaration(d Declaration) error {
	if d.Provider == "" {
		return fmt.Errorf("%w: empty provider id", ErrInvalidDeclaration)
	}
	return ValidateCredentialScope(d.Credential, d.Provider)
}

// ValidateAllocationRequest checks the shape of a request before any adapter
// receives it.
func ValidateAllocationRequest(req AllocationRequest) error {
	if _, err := ids.ParseLeaseID(req.LeaseID.String()); err != nil {
		return fmt.Errorf("%w: lease id: %w", ErrInvalidRequest, err)
	}
	if req.Slots < 1 || req.Slots > MaxSlotsPerAllocation {
		return fmt.Errorf("%w: slots must be between 1 and %d", ErrInvalidRequest, MaxSlotsPerAllocation)
	}
	switch req.Retry {
	case RetryProviderDefault, RetryDisabled:
		return nil
	}
	return fmt.Errorf("%w: unknown retry setting %q", ErrInvalidRequest, req.Retry)
}
