package statestore

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

var (
	// ErrLeaseActive means another unexpired lease already owns the Task fence.
	ErrLeaseActive = errors.New("an active lease already exists")
	// ErrPlacementDecisionConflict means a scoped Placement Decision ID was
	// reused with a different durable authority or subject binding. A decision
	// cannot be rebound inside the same trust-domain/principal scope.
	ErrPlacementDecisionConflict = errors.New("placement decision ID reused with a different binding")
)

type LeaseReservation struct {
	LeaseID                    ids.LeaseID
	TaskID                     ids.TaskID
	TaskIRDigest               contract.Digest
	ExpectedTaskRevision       uint64
	AttemptID                  ids.AttemptID
	WorkerIdentity             string
	PlacementTrustDomain        string
	PlacementAuthorityPrincipal string
	PlacementDecisionID         string
	PlacementPayloadDigest      contract.Digest
	PlacementExpiresAt          time.Time
	PolicyRevision              string
	IssuedAt                    time.Time
	ExpiresAt                   time.Time
}

func (r LeaseReservation) Validate() error {
	if _, err := ids.ParseLeaseID(r.LeaseID.String()); err != nil {
		return fmt.Errorf("lease reservation: lease ID: %w", err)
	}
	if _, err := ids.ParseTaskID(r.TaskID.String()); err != nil {
		return fmt.Errorf("lease reservation: task ID: %w", err)
	}
	if r.TaskIRDigest.IsZero() {
		return errors.New("lease reservation: TaskIR digest is required")
	}
	if r.ExpectedTaskRevision == 0 {
		return errors.New("lease reservation: expected Task revision must be positive")
	}
	if _, err := ids.ParseAttemptID(r.AttemptID.String()); err != nil {
		return fmt.Errorf("lease reservation: attempt ID: %w", err)
	}
	if r.WorkerIdentity == "" || len(r.WorkerIdentity) > 256 || !utf8.ValidString(r.WorkerIdentity) {
		return errors.New("lease reservation: invalid Worker identity")
	}
	if r.PlacementTrustDomain == "" || len(r.PlacementTrustDomain) > 256 || !utf8.ValidString(r.PlacementTrustDomain) {
		return errors.New("lease reservation: invalid Placement trust domain")
	}
	if r.PlacementAuthorityPrincipal == "" || len(r.PlacementAuthorityPrincipal) > 256 || !utf8.ValidString(r.PlacementAuthorityPrincipal) {
		return errors.New("lease reservation: invalid Placement authority principal")
	}
	if r.PlacementDecisionID == "" || len(r.PlacementDecisionID) > 128 || !utf8.ValidString(r.PlacementDecisionID) {
		return errors.New("lease reservation: invalid Placement Decision ID")
	}
	if r.PlacementPayloadDigest.IsZero() {
		return errors.New("lease reservation: Placement payload digest is required")
	}
	if r.PlacementExpiresAt.IsZero() || r.ExpiresAt.After(r.PlacementExpiresAt) {
		return errors.New("lease reservation: Lease expiry exceeds Placement Approval expiry")
	}
	if r.PolicyRevision == "" || len(r.PolicyRevision) > 128 || !utf8.ValidString(r.PolicyRevision) {
		return errors.New("lease reservation: invalid policy revision")
	}
	if r.IssuedAt.IsZero() || r.ExpiresAt.IsZero() || !r.IssuedAt.Before(r.ExpiresAt) {
		return errors.New("lease reservation: invalid issued/expires window")
	}
	return nil
}

type LeaseReservationResult struct {
	Lease    leasestate.LeaseRecord
	Replayed bool
}

// MatchesReservedPlacement checks the payload-bound Placement subject carried
// into durable Lease state. Lease ID and Lease time window are intentionally
// excluded: a transport retry may regenerate those caller-side suggestions,
// but it must not change the Placement authority or subject.
func (r LeaseReservation) MatchesReservedPlacement(existing leasestate.LeaseRecord) bool {
	return existing.TaskID == r.TaskID &&
		existing.TaskIRDigest == r.TaskIRDigest &&
		existing.AttemptID == r.AttemptID &&
		existing.WorkerIdentity == r.WorkerIdentity &&
		existing.PlacementTrustDomain == r.PlacementTrustDomain &&
		existing.PlacementAuthorityPrincipal == r.PlacementAuthorityPrincipal &&
		existing.PlacementDecisionID == r.PlacementDecisionID &&
		existing.PlacementPayloadDigest == r.PlacementPayloadDigest &&
		existing.PlacementExpiresAt.Equal(r.PlacementExpiresAt.UTC()) &&
		existing.PolicyRevision == r.PolicyRevision
}

// LeaseStore reserves durable fencing state before any signed Lease is emitted.
//
// Implementations must make Task revision/cancellation checks, active-Lease
// exclusion, placement replay handling, and FenceEpoch allocation atomic.
type LeaseStore interface {
	ReserveLease(ctx context.Context, reservation LeaseReservation) (LeaseReservationResult, error)
	LoadLease(ctx context.Context, id ids.LeaseID) (leasestate.LeaseRecord, error)
}
