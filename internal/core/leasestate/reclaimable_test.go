package leasestate

import (
	"testing"
	"time"
)

func TestOnlyTheExpiredLeasesArePicked(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	active := LeaseRecord{IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	expired := LeaseRecord{IssuedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
	got := Reclaimable([]LeaseRecord{active, expired}, now)
	if len(got) != 1 {
		t.Fatalf("Reclaimable() = %d entries, want 1", len(got))
	}
	if got[0].Reason != "expired" || !got[0].Lease.ExpiresAt.Equal(expired.ExpiresAt) {
		t.Fatalf("reclaim = %+v", got[0])
	}
}

func TestAnEmptyScanPicksNothing(t *testing.T) {
	if got := Reclaimable(nil, time.Now()); len(got) != 0 {
		t.Fatalf("Reclaimable(nil) = %+v", got)
	}
}

func TestAllActiveLeasesAreLeftAlone(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	leases := []LeaseRecord{
		{IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)},
		{IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(2 * time.Hour)},
	}
	if got := Reclaimable(leases, now); len(got) != 0 {
		t.Fatalf("Reclaimable() = %+v, want none", got)
	}
}
