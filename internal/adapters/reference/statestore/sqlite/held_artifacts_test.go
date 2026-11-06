package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// The claim is recorded with a new revision, and the guards that protect the
// Worker's identity and state still hold (ADR-0029 §5.1).
func TestSetHeldArtifactsRecordsTheClaim(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	t.Cleanup(func() { _ = store.Close() })
	identity := newWorker(t, store)
	held := contract.SHA256([]byte("artifact-a"))

	if err := store.SetHeldArtifacts(context.Background(), ids.WorkerIdentity(identity), workerregistry.HeldArtifacts{held}, registryTestNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	worker, err := store.LoadWorker(context.Background(), ids.WorkerIdentity(identity))
	if err != nil {
		t.Fatal(err)
	}
	if !worker.HeldArtifacts.Holds(held) {
		t.Fatalf("HeldArtifacts = %v, want the recorded digest", worker.HeldArtifacts)
	}
	if worker.Revision != 2 {
		t.Fatalf("Revision = %d, want 2 after one claim", worker.Revision)
	}
	if worker.State != workerregistry.StateRegistered {
		t.Fatalf("State = %q, want the Worker still registered", worker.State)
	}
}

// An unknown Worker is reported as missing, not as a failed write.
func TestSetHeldArtifactsRefusesAnUnknownWorker(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	t.Cleanup(func() { _ = store.Close() })
	err := store.SetHeldArtifacts(context.Background(), ids.WorkerIdentity("wrk_00000000000000000000000000000009"), workerregistry.HeldArtifacts{contract.SHA256([]byte("a"))}, registryTestNow)
	if !errors.Is(err, statestore.ErrNotFound) {
		t.Fatalf("SetHeldArtifacts() error = %v, want %v", err, statestore.ErrNotFound)
	}
}

// A claim that does not validate never reaches the table.
func TestSetHeldArtifactsRefusesABrokenClaim(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	t.Cleanup(func() { _ = store.Close() })
	identity := newWorker(t, store)
	err := store.SetHeldArtifacts(context.Background(), ids.WorkerIdentity(identity), workerregistry.HeldArtifacts{{}}, registryTestNow)
	if !errors.Is(err, workerregistry.ErrHeldArtifacts) {
		t.Fatalf("SetHeldArtifacts() error = %v, want %v", err, workerregistry.ErrHeldArtifacts)
	}
}
