package memory

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) CommitLeaseEmission(ctx context.Context, commit statestore.LeaseEmissionCommit) (statestore.LeaseEmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	if err := commit.Validate(); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.leaseEmissions[commit.LeaseID]; ok {
		if existing.Status() == leaseemission.StatusAborted {
			return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionAborted
		}
		if !commit.Matches(existing) {
			return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionConflict
		}
		return statestore.LeaseEmissionResult{Record: existing, Replayed: true}, nil
	}

	lease, ok := s.leases[commit.LeaseID]
	if !ok {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, commit.LeaseID)
	}
	if !lease.PolicyFenced() || lease.PolicyEpoch != commit.ExpectedPolicyEpoch {
		return statestore.LeaseEmissionResult{}, statestore.ErrPolicyChanged
	}
	if commit.CommittedAt.Before(lease.IssuedAt) || !commit.CommittedAt.Before(lease.ExpiresAt) {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: Lease is not live at emission commit", statestore.ErrConflict)
	}

	scope := authorityDecisionScope{
		trustDomain: commit.TrustDomain,
		principal:   commit.AuthorityPrincipal,
		decisionID:  commit.DecisionID,
	}
	if other, exists := s.emissionDecisions[scope]; exists && other != lease.ID {
		return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionConflict
	}

	task, ok := s.tasks[lease.TaskID]
	if !ok {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, lease.TaskID)
	}
	if task.Revision != commit.ExpectedTaskRevision ||
		task.TaskIRDigest != lease.TaskIRDigest ||
		task.CancellationRequested ||
		(task.Phase != taskstate.TaskPhaseReady && task.Phase != taskstate.TaskPhaseActive) {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: Task changed before Lease emission", statestore.ErrConflict)
	}

	attempt, ok := s.attempts[lease.AttemptID]
	if !ok {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, lease.AttemptID)
	}
	if attempt.Revision != commit.ExpectedAttemptRevision ||
		attempt.TaskID != lease.TaskID ||
		attempt.TaskIRDigest != lease.TaskIRDigest ||
		attempt.WorkerIdentity != lease.WorkerIdentity ||
		attempt.LeaseID != lease.ID ||
		attempt.Phase != attemptstate.AttemptPhaseAssigned {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: Attempt changed before Lease emission", statestore.ErrConflict)
	}

	policy, ok := s.policyHistory[s.policyCurrent]
	if !ok || s.policyCurrent == 0 {
		return statestore.LeaseEmissionResult{}, statestore.ErrPolicyUnavailable
	}
	if s.policyCurrent != commit.ExpectedPolicyEpoch ||
		policy.Status != policystate.StatusActive ||
		policy.Revision != lease.PolicyRevision {
		return statestore.LeaseEmissionResult{}, statestore.ErrPolicyChanged
	}

	record, err := leaseemission.NewEmitted(leaseemission.EmittedConfig{
		LeaseID:            lease.ID,
		DecisionID:         commit.DecisionID,
		TrustDomain:        commit.TrustDomain,
		AuthorityPrincipal: commit.AuthorityPrincipal,
		KeyID:              commit.KeyID,
		Algorithm:          commit.Algorithm,
		PayloadDigest:      commit.PayloadDigest,
		EnvelopeDigest:     commit.EnvelopeDigest,
		SignedEnvelope:     commit.SignedEnvelope,
		PolicyRevision:     lease.PolicyRevision,
		PolicyEpoch:        lease.PolicyEpoch,
		CommittedAt:        commit.CommittedAt,
	})
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}

	s.leaseEmissions[lease.ID] = record
	s.emissionDecisions[scope] = lease.ID
	return statestore.LeaseEmissionResult{Record: record}, nil
}

// RevokeAssignment implements statestore.AssignmentRevokeStore. It revokes a
// delivery the Control Plane already EMITTED: the Lease emission record flips
// to ABORTED with the revocation's close reason, and the backing Attempt
// closes with the same reason, in one write. A Lease without an EMITTED
// emission is refused — nothing was delivered, so there is nothing for the
// Worker to un-learn. Replaying the same revocation is a no-op; replaying it
// with a different reason is a conflict.
func (s *Store) RevokeAssignment(ctx context.Context, revoke statestore.AssignmentRevocation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := revoke.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.leaseEmissions[revoke.LeaseID]
	if !ok {
		return fmt.Errorf("%w: Lease emission %s", statestore.ErrNotFound, revoke.LeaseID)
	}
	switch existing.Status() {
	case leaseemission.StatusAborted:
		if existing.Reason() != revoke.Reason || existing.CloseReason() != string(revoke.CloseReason) {
			return fmt.Errorf("%w: Lease emission %s is already revoked for another reason", statestore.ErrLeaseEmissionConflict, revoke.LeaseID)
		}
		return nil
	case leaseemission.StatusEmitted:
	default:
		return fmt.Errorf("%w: Lease emission %s has status %q", statestore.ErrLeaseEmissionConflict, revoke.LeaseID, existing.Status())
	}

	lease, ok := s.leases[revoke.LeaseID]
	if !ok {
		return fmt.Errorf("%w: lease %s", statestore.ErrNotFound, revoke.LeaseID)
	}
	attempt, ok := s.attempts[lease.AttemptID]
	if !ok {
		return fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, lease.AttemptID)
	}
	if attempt.LeaseID != lease.ID || attempt.WorkerIdentity != lease.WorkerIdentity {
		return fmt.Errorf("%w: Attempt does not hold Lease %s", statestore.ErrConflict, lease.ID)
	}
	switch attempt.Phase {
	case attemptstate.AttemptPhaseAssigned, attemptstate.AttemptPhaseDispatched:
	default:
		return fmt.Errorf("%w: Attempt phase %q cannot revoke a delivery", statestore.ErrConflict, attempt.Phase)
	}

	closed := attempt
	closed.Phase = attemptstate.AttemptPhaseClosed
	closed.CloseReason = revoke.CloseReason
	closed.Revision++
	closed.UpdatedAt = laterTime(attempt.UpdatedAt, revoke.CommittedAt)
	if err := attemptstate.ValidateTransition(attempt, closed); err != nil {
		return err
	}
	record, err := leaseemission.NewAborted(leaseemission.AbortedConfig{
		LeaseID:     lease.ID,
		CloseReason: string(revoke.CloseReason),
		Reason:      revoke.Reason,
		CommittedAt: revoke.CommittedAt,
	})
	if err != nil {
		return err
	}

	s.leaseEmissions[lease.ID] = record
	s.attempts[closed.ID] = closed
	return nil
}

func (s *Store) AbortLeaseEmission(ctx context.Context, abort statestore.LeaseEmissionAbort) (statestore.LeaseEmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	if err := abort.Validate(); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.leaseEmissions[abort.LeaseID]; ok {
		if existing.Status() == leaseemission.StatusEmitted {
			return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionConflict
		}
		if existing.Reason() != abort.Reason || existing.CloseReason() != string(abort.CloseReason) {
			return statestore.LeaseEmissionResult{}, statestore.ErrLeaseEmissionConflict
		}
		return statestore.LeaseEmissionResult{Record: existing, Replayed: true}, nil
	}

	lease, ok := s.leases[abort.LeaseID]
	if !ok {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, abort.LeaseID)
	}
	attempt, ok := s.attempts[lease.AttemptID]
	if !ok {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, lease.AttemptID)
	}
	if attempt.Revision != abort.ExpectedAttemptRevision ||
		attempt.LeaseID != lease.ID ||
		attempt.WorkerIdentity != lease.WorkerIdentity ||
		attempt.Phase != attemptstate.AttemptPhaseAssigned {
		return statestore.LeaseEmissionResult{}, fmt.Errorf("%w: Attempt changed before Lease abort", statestore.ErrConflict)
	}

	closed := attempt
	closed.Phase = attemptstate.AttemptPhaseClosed
	closed.CloseReason = abort.CloseReason
	closed.Revision++
	closed.UpdatedAt = laterTime(attempt.UpdatedAt, abort.CommittedAt)
	if err := attemptstate.ValidateTransition(attempt, closed); err != nil {
		return statestore.LeaseEmissionResult{}, err
	}
	record, err := leaseemission.NewAborted(leaseemission.AbortedConfig{
		LeaseID:     lease.ID,
		CloseReason: string(abort.CloseReason),
		Reason:      abort.Reason,
		CommittedAt: abort.CommittedAt,
	})
	if err != nil {
		return statestore.LeaseEmissionResult{}, err
	}

	s.leaseEmissions[lease.ID] = record
	s.attempts[closed.ID] = closed
	return statestore.LeaseEmissionResult{Record: record}, nil
}

func (s *Store) LoadLeaseEmission(ctx context.Context, id ids.LeaseID) (leaseemission.Record, error) {
	if err := ctx.Err(); err != nil {
		return leaseemission.Record{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.leaseEmissions[id]
	if !ok {
		return leaseemission.Record{}, fmt.Errorf("%w: Lease emission %s", statestore.ErrNotFound, id)
	}
	return record, nil
}

var _ statestore.LeaseEmissionStore = (*Store)(nil)
var _ statestore.AssignmentRevokeStore = (*Store)(nil)
