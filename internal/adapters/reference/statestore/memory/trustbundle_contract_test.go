package memory

import (
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestTrustBundleStoreContract(t *testing.T) {
	statestoretest.RunTrustBundleContract(t, func(t *testing.T) statestore.TrustBundleStore {
		return NewTrustBundles()
	})
}
