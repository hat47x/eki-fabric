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


func TestLeaseStoreContract(t *testing.T) {
	statestoretest.RunLeaseStoreContract(t, func(t testing.TB) statestoretest.TaskLeaseStore {
		t.Helper()
		return New()
	})
}
