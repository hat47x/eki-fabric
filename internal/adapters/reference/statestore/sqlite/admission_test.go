package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/statestoretest"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestAdmissionContract(t *testing.T) {
	statestoretest.RunAdmissionContract(t, func(t testing.TB) statestore.Store {
		t.Helper()
		store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		})
		return store
	})
}

func TestAdmissionDurabilityContract(t *testing.T) {
	statestoretest.RunAdmissionDurabilityContract(t, statestoretest.DurableStoreFactory{
		Open: func(t testing.TB, location string) (statestore.Store, func() error) {
			store := openStore(t, location)
			return store, store.Close
		},
	})
}

func TestAdmissionSchemaRejectsInvalidRowsEvenIfAdapterIsBypassed(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	record := newTaskRecord(t, "schema")
	if err := store.CreateTask(context.Background(), record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	fp := contract.SHA256([]byte("fp")).Hex()
	id := string(record.ID)

	cases := map[string]string{
		"empty principal": `INSERT INTO admissions VALUES ('` + id + `', '', 'key-00000001', '` + fp + `', 3)`,
		"short key":       `INSERT INTO admissions VALUES ('` + id + `', 'alice', 'short', '` + fp + `', 3)`,
		"bad fingerprint": `INSERT INTO admissions VALUES ('` + id + `', 'alice', 'key-00000001', 'abcd', 3)`,
		"zero attempts":   `INSERT INTO admissions VALUES ('` + id + `', 'alice', 'key-00000001', '` + fp + `', 0)`,
		"unknown task":    `INSERT INTO admissions VALUES ('tsk_unknown', 'alice', 'key-00000001', '` + fp + `', 3)`,
	}
	for name, stmt := range cases {
		if _, err := store.db.Exec(stmt); err == nil {
			t.Errorf("%s: insert succeeded, want a constraint failure", name)
		}
	}
}

func TestMigrationsUpgradeAnExistingVersionOneDatabase(t *testing.T) {
	// A database created before admissions existed must be upgraded in place
	// and keep its tasks.
	path := filepath.Join(t.TempDir(), "state.db")
	v1 := openStoreWithMigrations(t, path, migrations[:1])
	record := newTaskRecord(t, "upgrade")
	if err := v1.CreateTask(context.Background(), record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if err := v1.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store := openStore(t, path)
	defer store.Close()
	if _, err := store.LoadTask(context.Background(), record.ID); err != nil {
		t.Fatalf("LoadTask() after upgrade error = %v", err)
	}
	if _, err := store.LoadAdmission(context.Background(), record.ID); !errors.Is(err, statestore.ErrNotFound) {
		t.Fatalf("LoadAdmission() for a pre-admission Task error = %v, want ErrNotFound", err)
	}
}

func openStoreWithMigrations(t testing.TB, path string, steps []string) *Store {
	t.Helper()
	store, err := openWithMigrations(context.Background(), path, steps)
	if err != nil {
		t.Fatalf("openWithMigrations(%q) error = %v", path, err)
	}
	return store
}
