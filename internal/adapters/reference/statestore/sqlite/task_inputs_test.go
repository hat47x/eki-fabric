package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// The digests are written with the Task and read back in order (ADR-0029 §5.8).
func TestTaskInputsRoundTripThroughTheChildTable(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	t.Cleanup(func() { _ = store.Close() })

	first := contract.SHA256([]byte("input-a"))
	second := contract.SHA256([]byte("input-b"))
	record := newTaskRecord(t, "inputs")
	admission := newInputAdmission(t, record)
	admission.InputDigests = []contract.Digest{second, first}
	if _, err := store.AdmitTask(context.Background(), admission, statestore.AdmitLimit{MaxOutstandingTasks: 5}); err != nil {
		t.Fatal(err)
	}

	digests, err := store.LoadTaskInputs(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 2 || digests[0].Hex() != second.Hex() || digests[1].Hex() != first.Hex() {
		t.Fatalf("LoadTaskInputs() = %v, want the declared order", digests)
	}
}

// A Task with no inputs has no rows, and reads back empty rather than missing.
func TestATaskWithoutInputsReadsEmpty(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	t.Cleanup(func() { _ = store.Close() })

	record := newTaskRecord(t, "no-inputs")
	if _, err := store.AdmitTask(context.Background(), newInputAdmission(t, record), statestore.AdmitLimit{MaxOutstandingTasks: 5}); err != nil {
		t.Fatal(err)
	}
	digests, err := store.LoadTaskInputs(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 0 {
		t.Fatalf("LoadTaskInputs() = %v, want none", digests)
	}
}

// newInputAdmission builds an admission the store accepts, so the tests here are
// about the input rows rather than about admission validation.
func newInputAdmission(t testing.TB, record taskstate.TaskRecord) statestore.Admission {
	t.Helper()
	return statestore.Admission{
		Task:               record,
		Principal:          "alice",
		IdempotencyKey:     "key-" + record.ID.String(),
		RequestFingerprint: contract.SHA256([]byte(record.ID.String())),
		MaxAttempts:        3,
	}
}
