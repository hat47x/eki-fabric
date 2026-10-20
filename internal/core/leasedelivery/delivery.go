// Package leasedelivery passes only durably EMITTED Lease bytes to a transport.
// It neither issues new authority nor decides whether a Worker may execute it.
package leasedelivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

var (
	ErrInvalidConfig  = errors.New("invalid Lease delivery configuration")
	ErrNotDeliverable = errors.New("Lease is not deliverable")
	// ErrDeliveryUnknown means the transport failed after being called. The
	// recipient may have received the Lease; no automatic retry is attempted.
	ErrDeliveryUnknown = errors.New("Lease delivery outcome is unknown")
)

type Store interface {
	LoadLease(context.Context, ids.LeaseID) (leasestate.LeaseRecord, error)
	LoadLeaseEmission(context.Context, ids.LeaseID) (leaseemission.Record, error)
}

type Clock interface{ Now() time.Time }

// Envelope contains only the persisted signed bytes and their stored route.
// The caller cannot supply a recipient or bytes to Service.Send.
type Envelope struct {
	LeaseID        ids.LeaseID
	WorkerIdentity string
	SignedLease    []byte
}

type Transport interface {
	SendLease(context.Context, Envelope) error
}

type Service struct {
	store     Store
	transport Transport
	clock     Clock
}

func New(store Store, transport Transport, clock Clock) (*Service, error) {
	if store == nil || transport == nil || clock == nil {
		return nil, ErrInvalidConfig
	}
	return &Service{store: store, transport: transport, clock: clock}, nil
}

// Send loads the durable outcome, refuses RESERVED/ABORTED/expired Leases, and
// calls the transport at most once. Repeated invocations reload the same exact
// EMITTED bytes without re-signing. A successful transport call is not proof of
// Worker receipt, signature verification, durable fencing, or execution.
func (s *Service) Send(ctx context.Context, id ids.LeaseID) error {
	if s == nil || s.store == nil || s.transport == nil || s.clock == nil {
		return ErrInvalidConfig
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrNotDeliverable)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := ids.ParseLeaseID(id.String()); err != nil {
		return fmt.Errorf("%w: invalid Lease ID", ErrNotDeliverable)
	}
	emission, err := s.store.LoadLeaseEmission(ctx, id)
	if err != nil {
		return fmt.Errorf("load durable Lease emission: %w", err)
	}
	if err := emission.Validate(); err != nil {
		return fmt.Errorf("%w: invalid emission: %w", ErrNotDeliverable, err)
	}
	if emission.Status() != leaseemission.StatusEmitted || emission.LeaseID() != id {
		return fmt.Errorf("%w: no matching EMITTED record", ErrNotDeliverable)
	}
	lease, err := s.store.LoadLease(ctx, id)
	if err != nil {
		return fmt.Errorf("load Lease reservation: %w", err)
	}
	if err := lease.Validate(); err != nil {
		return fmt.Errorf("%w: invalid reservation: %w", ErrNotDeliverable, err)
	}
	if lease.ID != id || !lease.PolicyFenced() || lease.PolicyEpoch != emission.PolicyEpoch() || lease.PolicyRevision != emission.PolicyRevision() {
		return fmt.Errorf("%w: emission and reservation differ", ErrNotDeliverable)
	}
	now := s.clock.Now().UTC()
	if now.IsZero() || now.Before(lease.IssuedAt) || !now.Before(lease.ExpiresAt) {
		return fmt.Errorf("%w: Lease window is not open", ErrNotDeliverable)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.transport.SendLease(ctx, Envelope{LeaseID: id, WorkerIdentity: lease.WorkerIdentity, SignedLease: emission.SignedEnvelope()}); err != nil {
		return fmt.Errorf("%w: %w", ErrDeliveryUnknown, err)
	}
	return nil
}
