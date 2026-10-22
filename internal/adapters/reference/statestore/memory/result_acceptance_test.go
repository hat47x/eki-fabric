package memory

import (
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
)

func TestResultAcceptanceContract(t *testing.T) {
	statestoretest.RunResultAcceptanceContract(t, func(t testing.TB) statestoretest.ResultAcceptanceTestStore {
		t.Helper()
		return New()
	})
}
