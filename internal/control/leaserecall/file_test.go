package leaserecall

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAMissingFileIsAnEmptyRegistry(t *testing.T) {
	f, err := OpenFile(filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	open, err := f.OpenLeases(t.Context())
	if err != nil || len(open) != 0 {
		t.Fatalf("OpenLeases() = %+v, %v", open, err)
	}
}

func TestAnOpenLeaseSurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	f, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lease := memoryLease(t, "les_00000000000000000000000000000021", time.Now().Add(time.Hour))
	if err := f.Open(lease); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	again, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	open, err := again.OpenLeases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ID != lease.ID {
		t.Fatalf("open = %+v", open)
	}
}

func TestAReclaimIsPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	f, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lease := memoryLease(t, "les_00000000000000000000000000000022", time.Now().Add(time.Hour))
	if err := f.Open(lease); err != nil {
		t.Fatal(err)
	}
	if err := f.Reclaim(t.Context(), lease, "expired"); err != nil {
		t.Fatalf("Reclaim() error = %v", err)
	}
	again, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	open, err := again.OpenLeases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("open = %+v, want none", open)
	}
}

func TestACorruptFileIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(path); err == nil {
		t.Fatal("OpenFile() accepted a corrupt file")
	}
}
