package memory

import (
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
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
