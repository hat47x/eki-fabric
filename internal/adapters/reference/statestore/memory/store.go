package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/hat47x/eki-fabric/internal/core/acceptedresult"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/rejectedresult"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workorderemission"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type idempotencyScope struct {
	principal string
	key       string
}

type placementScope struct {
	trustDomain string
	principal   string
	decisionID  string
}

type authorityDecisionScope struct {
	trustDomain string
	principal   string
	decisionID  string
}

type Store struct {
	mu                sync.RWMutex
	admissions        map[ids.TaskID]statestore.Admission
	idempotent        map[idempotencyScope]ids.TaskID
	tasks             map[ids.TaskID]taskstate.TaskRecord
	attempts          map[ids.AttemptID]attemptstate.AttemptRecord
	leases            map[ids.LeaseID]leasestate.LeaseRecord
	leaseEmissions    map[ids.LeaseID]leaseemission.Record
	emissionDecisions map[authorityDecisionScope]ids.LeaseID
	workOrders        map[ids.LeaseID]workorderemission.Record
	workOrderDecision map[authorityDecisionScope]ids.LeaseID
	placementLeases   map[placementScope]ids.LeaseID
	maxFence          map[ids.TaskID]uint64
	policyCurrent     uint64
	policyHistory     map[uint64]policystate.State
	claims            map[ids.AttemptID]resultclaim.Claim
	accepted          map[ids.TaskID]acceptedresult.Record
	rejected          map[ids.AttemptID]rejectedresult.Record
	recovery          recoveryState
}

func New() *Store {
	return &Store{
		tasks:             make(map[ids.TaskID]taskstate.TaskRecord),
		attempts:          make(map[ids.AttemptID]attemptstate.AttemptRecord),
		admissions:        make(map[ids.TaskID]statestore.Admission),
		idempotent:        make(map[idempotencyScope]ids.TaskID),
		leases:            make(map[ids.LeaseID]leasestate.LeaseRecord),
		leaseEmissions:    make(map[ids.LeaseID]leaseemission.Record),
		emissionDecisions: make(map[authorityDecisionScope]ids.LeaseID),
		workOrders:        make(map[ids.LeaseID]workorderemission.Record),
		workOrderDecision: make(map[authorityDecisionScope]ids.LeaseID),
		placementLeases:   make(map[placementScope]ids.LeaseID),
		maxFence:          make(map[ids.TaskID]uint64),
		policyHistory:     make(map[uint64]policystate.State),
		claims:            make(map[ids.AttemptID]resultclaim.Claim),
		accepted:          make(map[ids.TaskID]acceptedresult.Record),
		rejected:          make(map[ids.AttemptID]rejectedresult.Record),
		recovery:          newRecoveryState(),
	}
}

func (s *Store) CreateTask(ctx context.Context, record taskstate.TaskRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[record.ID]; exists {
		return fmt.Errorf("%w: task %s", statestore.ErrAlreadyExists, record.ID)
	}
	s.tasks[record.ID] = record
	return nil
}

func (s *Store) LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error) {
	if err := ctx.Err(); err != nil {
		return taskstate.TaskRecord{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.tasks[id]
	if !ok {
		return taskstate.TaskRecord{}, fmt.Errorf("%w: task %s", statestore.ErrNotFound, id)
	}
	return record, nil
}

func (s *Store) CompareAndSwapTask(ctx context.Context, id ids.TaskID, expectedRevision uint64, next taskstate.TaskRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if next.ID != id {
		return fmt.Errorf("%w: next task ID does not match target", statestore.ErrConflict)
	}
	if next.Revision != expectedRevision+1 {
		return fmt.Errorf("%w: next revision %d does not follow expected revision %d", statestore.ErrConflict, next.Revision, expectedRevision)
	}
	if err := next.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("%w: task %s", statestore.ErrNotFound, id)
	}
	if current.Revision != expectedRevision {
		return fmt.Errorf("%w: task %s is at revision %d, expected %d", statestore.ErrConflict, id, current.Revision, expectedRevision)
	}
	if current.TaskIRDigest != next.TaskIRDigest {
		return fmt.Errorf("%w: task %s TaskIR digest is immutable", statestore.ErrConflict, id)
	}

	s.tasks[id] = next
	return nil
}
