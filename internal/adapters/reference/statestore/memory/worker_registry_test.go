package memory

import (
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestWorkerRegistryContract(t *testing.T) {
	statestoretest.RunWorkerRegistryContract(t, func(t testing.TB) statestore.WorkerRegistryStore {
		t.Helper()
		return New()
	})
}
