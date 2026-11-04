package leaserecall

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

type fakeLister struct {
	leases []leasestate.LeaseRecord
	err    error
}

func (f fakeLister) OpenLeases(context.Context) ([]leasestate.LeaseRecord, error) {
	return f.leases, f.err
}

type fakeReclaimer struct {
	reclaimed []leasestate.LeaseRecord
	reasons   []string
	err       error
}

func (f *fakeReclaimer) Reclaim(_ context.Context, lease leasestate.LeaseRecord, reason string) error {
	if f.err != nil {
		return f.err
	}
	f.reclaimed = append(f.reclaimed, lease)
	f.reasons = append(f.reasons, reason)
	return nil
}

func leaseRecord(t *testing.T, name string, expires time.Time) leasestate.LeaseRecord {
	t.Helper()
	id, err := ids.ParseLeaseID(name)
	if err != nil {
		t.Fatal(err)
	}
	return leasestate.LeaseRecord{ID: id, ExpiresAt: expires}
}

func TestOnlyTheExpiredLeasesAreReclaimed(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	active := leaseRecord(t, "les_00000000000000000000000000000001", now.Add(time.Hour))
	expired := leaseRecord(t, "les_00000000000000000000000000000002", now.Add(-time.Hour))
	reclaimer := &fakeReclaimer{}
	reasons := []string{}
	svc, err := New(Config{
		Lister:    fakeLister{leases: []leasestate.LeaseRecord{active, expired}},
		Reclaimer: reclaimer,
		Now:       func() time.Time { return now },
		OnReclaim: func(reason string) { reasons = append(reasons, reason) },
	})
	if err != nil {
		t.Fatal(err)
	}
	taken, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if taken != 1 || len(reclaimer.reclaimed) != 1 {
		t.Fatalf("taken = %d, reclaimed = %d", taken, len(reclaimer.reclaimed))
	}
	if reclaimer.reclaimed[0].ID != expired.ID {
		t.Fatalf("reclaimed %s, want the expired lease", reclaimer.reclaimed[0].ID)
	}
	if len(reasons) != 1 || reasons[0] != "expired" {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestAListerFailureReclaimsNothing(t *testing.T) {
	reclaimer := &fakeReclaimer{}
	svc, err := New(Config{Lister: fakeLister{err: errors.New("boom")}, Reclaimer: reclaimer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sweep(t.Context()); err == nil {
		t.Fatal("Sweep() hid the lister failure")
	}
	if len(reclaimer.reclaimed) != 0 {
		t.Fatalf("reclaimed %d leases on a failed list", len(reclaimer.reclaimed))
	}
}

func TestAReclaimFailureStopsTheSweep(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	first := leaseRecord(t, "les_00000000000000000000000000000003", now.Add(-time.Hour))
	second := leaseRecord(t, "les_00000000000000000000000000000004", now.Add(-time.Hour))
	reclaimer := &fakeReclaimer{err: errors.New("state changed")}
	svc, err := New(Config{
		Lister:    fakeLister{leases: []leasestate.LeaseRecord{first, second}},
		Reclaimer: reclaimer,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sweep(t.Context()); err == nil {
		t.Fatal("Sweep() hid the reclaim failure")
	}
}

func TestAServiceNeedsBothSides(t *testing.T) {
	if _, err := New(Config{Lister: fakeLister{}}); err == nil {
		t.Fatal("New() accepted no reclaimer")
	}
	if _, err := New(Config{Reclaimer: &fakeReclaimer{}}); err == nil {
		t.Fatal("New() accepted no lister")
	}
}
