package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestTrustBundleStoreContract(t *testing.T) {
	statestoretest.RunTrustBundleContract(t, func(t *testing.T) statestore.TrustBundleStore {
		t.Helper()
		store, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	})
}
