package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) ReserveLease(ctx context.Context, reservation statestore.LeaseReservation) (statestore.LeaseReservationResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.LeaseReservationResult{}, err
	}
	if err := reservation.Validate(); err != nil {
		return statestore.LeaseReservationResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[reservation.TaskID]
	if !ok {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, reservation.TaskID)
	}
	if task.TaskIRDigest != reservation.TaskIRDigest ||
		task.CancellationRequested ||
		(task.Phase != taskstate.TaskPhaseReady && task.Phase != taskstate.TaskPhaseActive) {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: task %s changed before lease reservation", statestore.ErrConflict, reservation.TaskID)
	}

	scope := placementScope{
		trustDomain: reservation.PlacementTrustDomain,
		principal:   reservation.PlacementAuthorityPrincipal,
		decisionID:  reservation.PlacementDecisionID,
	}
	if existingID, ok := s.placementLeases[scope]; ok {
		existing := s.leases[existingID]
		if !reservation.MatchesReservedPlacement(existing) {
			return statestore.LeaseReservationResult{}, fmt.Errorf(
				"%w: decision %q",
				statestore.ErrPlacementDecisionConflict,
				reservation.PlacementDecisionID,
			)
		}
		attempt, ok := s.attempts[existing.AttemptID]
		if !ok || !attemptCanReplayLease(attempt, existing) {
			return statestore.LeaseReservationResult{}, fmt.Errorf(
				"%w: attempt %s no longer owns replayed lease %s",
				statestore.ErrConflict,
				existing.AttemptID,
				existing.ID,
			)
		}
		return statestore.LeaseReservationResult{Lease: existing, Replayed: true}, nil
	}

	attempt, ok := s.attempts[reservation.AttemptID]
	if !ok {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, reservation.AttemptID)
	}
	if attempt.TaskID != reservation.TaskID ||
		attempt.TaskIRDigest != reservation.TaskIRDigest ||
		attempt.WorkerIdentity != reservation.WorkerIdentity ||
		attempt.Phase != attemptstate.AttemptPhasePlacing ||
		attempt.LeaseID != "" {
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: attempt %s changed before new lease reservation",
			statestore.ErrConflict,
			reservation.AttemptID,
		)
	}
	if attempt.Revision != reservation.ExpectedAttemptRevision {
		return statestore.LeaseReservationResult{}, fmt.Errorf(
			"%w: attempt %s is at revision %d, expected %d",
			statestore.ErrConflict,
			reservation.AttemptID,
			attempt.Revision,
			reservation.ExpectedAttemptRevision,
		)
	}
	if task.Revision != reservation.ExpectedTaskRevision {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: task %s changed before new lease reservation", statestore.ErrConflict, reservation.TaskID)
	}
	if _, exists := s.leases[reservation.LeaseID]; exists {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: lease %s", statestore.ErrAlreadyExists, reservation.LeaseID)
	}

	for _, lease := range s.leases {
		if lease.TaskID == reservation.TaskID && lease.ExpiresAt.After(reservation.IssuedAt.UTC()) {
			return statestore.LeaseReservationResult{}, fmt.Errorf(
				"%w: task %s fence=%d",
				statestore.ErrLeaseActive,
				reservation.TaskID,
				lease.FenceEpoch,
			)
		}
	}

	fence := s.maxFence[reservation.TaskID] + 1
	if fence == 0 {
		return statestore.LeaseReservationResult{}, fmt.Errorf("%w: fence epoch exhausted for task %s", statestore.ErrConflict, reservation.TaskID)
	}
	lease, err := leasestate.New(leasestate.Config{
		ID:                          reservation.LeaseID,
		TaskID:                      reservation.TaskID,
		TaskIRDigest:                reservation.TaskIRDigest,
		AttemptID:                   reservation.AttemptID,
		WorkerIdentity:              reservation.WorkerIdentity,
		PlacementTrustDomain:        reservation.PlacementTrustDomain,
		PlacementAuthorityPrincipal: reservation.PlacementAuthorityPrincipal,
		PlacementDecisionID:         reservation.PlacementDecisionID,
		PlacementPayloadDigest:      reservation.PlacementPayloadDigest,
		PlacementExpiresAt:          reservation.PlacementExpiresAt,
		PolicyRevision:              reservation.PolicyRevision,
		FenceEpoch:                  fence,
		RenewalSequence:             0,
		IssuedAt:                    reservation.IssuedAt,
		ExpiresAt:                   reservation.ExpiresAt,
	})
	if err != nil {
		return statestore.LeaseReservationResult{}, err
	}
	assigned := attempt
	assigned.Phase = attemptstate.AttemptPhaseAssigned
	assigned.LeaseID = lease.ID
	assigned.Revision++
	assigned.UpdatedAt = laterTime(attempt.UpdatedAt, lease.IssuedAt)
	if err := attemptstate.ValidateTransition(attempt, assigned); err != nil {
		return statestore.LeaseReservationResult{}, err
	}

	s.leases[lease.ID] = lease
	s.placementLeases[placementScope{
		trustDomain: lease.PlacementTrustDomain,
		principal:   lease.PlacementAuthorityPrincipal,
		decisionID:  lease.PlacementDecisionID,
	}] = lease.ID
	s.maxFence[lease.TaskID] = lease.FenceEpoch
	s.attempts[assigned.ID] = assigned
	return statestore.LeaseReservationResult{Lease: lease}, nil
}

func attemptCanReplayLease(attempt attemptstate.AttemptRecord, lease leasestate.LeaseRecord) bool {
	if attempt.TaskID != lease.TaskID ||
		attempt.TaskIRDigest != lease.TaskIRDigest ||
		attempt.WorkerIdentity != lease.WorkerIdentity ||
		attempt.LeaseID != lease.ID {
		return false
	}
	switch attempt.Phase {
	case attemptstate.AttemptPhaseAssigned,
		attemptstate.AttemptPhaseDispatched,
		attemptstate.AttemptPhaseClaimed,
		attemptstate.AttemptPhaseVerifying:
		return true
	default:
		return false
	}
}

func laterTime(a, b time.Time) time.Time {
	a, b = a.UTC(), b.UTC()
	if b.After(a) {
		return b
	}
	return a
}

func (s *Store) LoadLease(ctx context.Context, id ids.LeaseID) (leasestate.LeaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return leasestate.LeaseRecord{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	lease, ok := s.leases[id]
	if !ok {
		return leasestate.LeaseRecord{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, id)
	}
	return lease, nil
}

var _ statestore.LeaseStore = (*Store)(nil)
