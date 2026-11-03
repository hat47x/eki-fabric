package leaserecall

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

func memoryLease(t *testing.T, name string, expires time.Time) leasestate.LeaseRecord {
	t.Helper()
	id, err := ids.ParseLeaseID(name)
	if err != nil {
		t.Fatal(err)
	}
	return leasestate.LeaseRecord{ID: id, ExpiresAt: expires}
}

func TestTheSweepReclaimsFromTheMemoryRegistry(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	registry := NewMemory()
	active := memoryLease(t, "les_00000000000000000000000000000011", now.Add(time.Hour))
	expired := memoryLease(t, "les_00000000000000000000000000000012", now.Add(-time.Hour))
	registry.Open(active)
	registry.Open(expired)

	svc, err := New(Config{Lister: registry, Reclaimer: registry, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	taken, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if taken != 1 {
		t.Fatalf("taken = %d, want 1", taken)
	}
	open, err := registry.OpenLeases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ID != active.ID {
		t.Fatalf("open = %+v, want only the active lease", open)
	}
}

func TestReclaimingALeaseThatIsNotOpenIsAnError(t *testing.T) {
	registry := NewMemory()
	lease := memoryLease(t, "les_00000000000000000000000000000013", time.Now())
	if err := registry.Reclaim(t.Context(), lease, "expired"); err == nil {
		t.Fatal("Reclaim() accepted a lease that is not open")
	}
}

func TestRenewingKeepsTheLaterExpiry(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	registry := NewMemory()
	lease := memoryLease(t, "les_00000000000000000000000000000014", now.Add(-time.Hour))
	registry.Open(lease)
	lease.ExpiresAt = now.Add(time.Hour)
	registry.Open(lease)

	svc, err := New(Config{Lister: registry, Reclaimer: registry, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if taken, err := svc.Sweep(t.Context()); err != nil || taken != 0 {
		t.Fatalf("Sweep() = %d, %v; a renewed lease must not be reclaimed", taken, err)
	}
}
