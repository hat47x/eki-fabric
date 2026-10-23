package memory

import (
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
)

func TestRecoveryStoreContract(t *testing.T) {
	statestoretest.RunRecoveryStoreContract(t, func(t testing.TB) statestoretest.RecoveryTestStore {
		t.Helper()
		return New()
	})
}
