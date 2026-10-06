package memory

import (
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestAdmissionContract(t *testing.T) {
	statestoretest.RunAdmissionContract(t, func(t testing.TB) statestore.Store {
		t.Helper()
		return New()
	})
}
