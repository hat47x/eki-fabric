package memory

import (
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
)

func TestResultRejectionContract(t *testing.T) {
	statestoretest.RunResultRejectionContract(t, func(t testing.TB) statestoretest.ResultRejectionTestStore {
		t.Helper()
		return New()
	})
}
