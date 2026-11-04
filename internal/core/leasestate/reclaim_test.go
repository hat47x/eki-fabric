package leasestate

import (
	"testing"
	"time"
)

func TestAnActiveLeaseHasNoReclaimReason(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	lease := LeaseRecord{IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	if got := lease.ReclaimReason(now); got != "" {
		t.Fatalf("ReclaimReason() = %q, want empty", got)
	}
}

func TestAnExpiredLeaseIsReclaimedBecauseItExpired(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	lease := LeaseRecord{IssuedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
	if got := lease.ReclaimReason(now); got != "expired" {
		t.Fatalf("ReclaimReason() = %q, want expired", got)
	}
}
