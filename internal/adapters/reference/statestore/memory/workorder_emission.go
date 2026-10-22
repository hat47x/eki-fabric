package memory

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workorderemission"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// CommitWorkOrderEmission linearizes one WOR-1 WorkOrder under the same
// mutex that serializes Policy, Task, Attempt, and Lease updates.
func (s *Store) CommitWorkOrderEmission(ctx context.Context, commit statestore.WorkOrderEmissionCommit) (statestore.WorkOrderEmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	if err := commit.Validate(); err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.workOrders[commit.LeaseID]; ok {
		if existing.Status() == workorderemission.StatusAborted {
			return statestore.WorkOrderEmissionResult{}, statestore.ErrWorkOrderEmissionAborted
		}
		if !commit.Matches(existing) {
			return statestore.WorkOrderEmissionResult{}, statestore.ErrWorkOrderEmissionConflict
		}
		return statestore.WorkOrderEmissionResult{Record: existing, Replayed: true}, nil
	}

	scope := authorityDecisionScope{
		trustDomain: commit.TrustDomain,
		principal:   commit.AuthorityPrincipal,
		decisionID:  commit.DecisionID,
	}
	if other, exists := s.workOrderDecision[scope]; exists && other != commit.LeaseID {
		return statestore.WorkOrderEmissionResult{}, statestore.ErrWorkOrderEmissionConflict
	}

	lease, ok := s.leases[commit.LeaseID]
	if !ok {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, commit.LeaseID)
	}
	emitted, ok := s.leaseEmissions[lease.ID]
	if !ok || !commit.LeaseEmissionMatches(emitted) {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: Lease %s is not the EMITTED Lease Decision the WorkOrder references", statestore.ErrConflict, lease.ID)
	}
	if !lease.PolicyFenced() || lease.PolicyEpoch != commit.ExpectedPolicyEpoch || lease.PolicyRevision != commit.ExpectedPolicyRevision {
		return statestore.WorkOrderEmissionResult{}, statestore.ErrPolicyChanged
	}
	if lease.WorkerIdentity != commit.WorkerIdentity {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: WorkOrder Worker differs from the Lease Worker", statestore.ErrConflict)
	}
	if lease.FenceEpoch != commit.ExpectedFenceEpoch || s.maxFence[lease.TaskID] != lease.FenceEpoch {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: Lease fence is no longer the latest for Task", statestore.ErrConflict)
	}
	if commit.CommittedAt.Before(lease.IssuedAt) || !commit.CommittedAt.Before(lease.ExpiresAt) ||
		commit.ExpiresAt.After(lease.ExpiresAt) {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: WorkOrder window is outside the live Lease", statestore.ErrConflict)
	}

	task, ok := s.tasks[lease.TaskID]
	if !ok {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, lease.TaskID)
	}
	if task.Revision != commit.ExpectedTaskRevision ||
		task.TaskIRDigest != lease.TaskIRDigest ||
		task.CancellationRequested ||
		(task.Phase != taskstate.TaskPhaseReady && task.Phase != taskstate.TaskPhaseActive) {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: Task changed before WorkOrder emission", statestore.ErrConflict)
	}

	attempt, ok := s.attempts[lease.AttemptID]
	if !ok {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: attempt %s", statestore.ErrNotFound, lease.AttemptID)
	}
	if attempt.Revision != commit.ExpectedAttemptRevision ||
		attempt.TaskID != lease.TaskID ||
		attempt.TaskIRDigest != lease.TaskIRDigest ||
		attempt.WorkerIdentity != lease.WorkerIdentity ||
		attempt.LeaseID != lease.ID ||
		attempt.Phase != attemptstate.AttemptPhaseAssigned {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: Attempt changed before WorkOrder emission", statestore.ErrConflict)
	}

	policy, ok := s.policyHistory[s.policyCurrent]
	if !ok || s.policyCurrent == 0 {
		return statestore.WorkOrderEmissionResult{}, statestore.ErrPolicyUnavailable
	}
	if s.policyCurrent != commit.ExpectedPolicyEpoch ||
		policy.Status != policystate.StatusActive ||
		policy.Revision != lease.PolicyRevision {
		return statestore.WorkOrderEmissionResult{}, statestore.ErrPolicyChanged
	}

	record, err := commit.Record()
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	s.workOrders[lease.ID] = record
	s.workOrderDecision[scope] = lease.ID
	return statestore.WorkOrderEmissionResult{Record: record}, nil
}

// AbortWorkOrderEmission records that no WorkOrder will be signed for a Lease.
// It changes no other state: FenceEpoch, the Lease, and the Attempt stay as
// they are.
func (s *Store) AbortWorkOrderEmission(ctx context.Context, abort statestore.WorkOrderEmissionAbort) (statestore.WorkOrderEmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	if err := abort.Validate(); err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.workOrders[abort.LeaseID]; ok {
		if existing.Status() == workorderemission.StatusEmitted {
			return statestore.WorkOrderEmissionResult{}, statestore.ErrWorkOrderEmissionConflict
		}
		return statestore.WorkOrderEmissionResult{Record: existing, Replayed: true}, nil
	}
	if _, ok := s.leases[abort.LeaseID]; !ok {
		return statestore.WorkOrderEmissionResult{}, fmt.Errorf("%w: lease %s", statestore.ErrNotFound, abort.LeaseID)
	}
	record, err := workorderemission.NewAborted(workorderemission.AbortedConfig{
		LeaseID: abort.LeaseID, AbortReason: abort.AbortReason, Reason: abort.Reason, CommittedAt: abort.CommittedAt,
	})
	if err != nil {
		return statestore.WorkOrderEmissionResult{}, err
	}
	s.workOrders[abort.LeaseID] = record
	return statestore.WorkOrderEmissionResult{Record: record}, nil
}

func (s *Store) LoadWorkOrderEmission(ctx context.Context, id ids.LeaseID) (workorderemission.Record, error) {
	if err := ctx.Err(); err != nil {
		return workorderemission.Record{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.workOrders[id]
	if !ok {
		return workorderemission.Record{}, fmt.Errorf("%w: WorkOrder emission %s", statestore.ErrNotFound, id)
	}
	return record, nil
}

var _ statestore.WorkOrderEmissionStore = (*Store)(nil)
