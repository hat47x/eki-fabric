package memory

import (
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestTaskStoreContract(t *testing.T) {
	statestoretest.RunTaskStoreContract(t, func(t testing.TB) statestore.TaskStore {
		t.Helper()
		return New()
	})
}

func TestAttemptStoreContract(t *testing.T) {
	statestoretest.RunAttemptStoreContract(t, func(t testing.TB) statestoretest.TaskAttemptStore {
		t.Helper()
		return New()
	})
}

func TestLeaseStoreContract(t *testing.T) {
	statestoretest.RunLeaseStoreContract(t, func(t testing.TB) statestoretest.TaskLeaseStore {
		t.Helper()
		return New()
	})
}

func TestLeaseBypassContract(t *testing.T) {
	statestoretest.RunLeaseBypassContract(t, statestoretest.LeaseBypassFactory{
		New: func(t testing.TB) statestoretest.TaskLeaseStore {
			t.Helper()
			return New()
		},
		RebindTaskIRDigest: func(t testing.TB, store statestoretest.TaskLeaseStore, id ids.TaskID, digest contract.Digest) {
			t.Helper()
			s := store.(*Store)
			s.mu.Lock()
			defer s.mu.Unlock()
			task, ok := s.tasks[id]
			if !ok {
				t.Fatalf("rebind: task %s not found", id)
			}
			task.TaskIRDigest = digest
			s.tasks[id] = task
		},
		OverwriteAttempt: func(t testing.TB, store statestoretest.TaskLeaseStore, record attemptstate.AttemptRecord) {
			t.Helper()
			s := store.(*Store)
			s.mu.Lock()
			defer s.mu.Unlock()
			current, ok := s.attempts[record.ID]
			if !ok {
				t.Fatalf("overwrite: attempt %s not found", record.ID)
			}
			current.TaskID = record.TaskID
			current.TaskIRDigest = record.TaskIRDigest
			current.WorkerIdentity = record.WorkerIdentity
			current.LeaseID = record.LeaseID
			s.attempts[record.ID] = current
		},
	})
}

func TestPolicyStoreContract(t *testing.T) {
	statestoretest.RunPolicyStoreContract(t, func(t testing.TB) statestore.PolicyStore {
		t.Helper()
		return New()
	})
}

func TestLeaseEmissionStoreContract(t *testing.T) {
	statestoretest.RunLeaseEmissionContract(t, func(t testing.TB) statestoretest.LeaseEmissionTestStore {
		t.Helper()
		return New()
	})
}

func TestLeaseSigningContract(t *testing.T) {
	statestoretest.RunLeaseSigningContract(t, func(t testing.TB) statestoretest.LeaseEmissionTestStore {
		t.Helper()
		return New()
	})
}

func TestWorkOrderSigningContract(t *testing.T) {
	statestoretest.RunWorkOrderSigningContract(t, func(t testing.TB) statestoretest.WorkOrderSigningTestStore {
		t.Helper()
		return New()
	})
}
