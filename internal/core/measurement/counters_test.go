package measurement

import (
	"sync"
	"testing"
)

func TestCountsAreKeptPerReason(t *testing.T) {
	c := New()
	c.Add(AdmissionRejected, "auth")
	c.Add(AdmissionRejected, "auth")
	c.Add(AdmissionRejected, "limit")
	if got := c.Total(AdmissionRejected); got != 3 {
		t.Fatalf("Total() = %d, want 3", got)
	}
	reasons := c.Reasons(AdmissionRejected)
	if reasons["auth"] != 2 || reasons["limit"] != 1 {
		t.Fatalf("reasons = %+v", reasons)
	}
}

func TestKindsDoNotBleedIntoEachOther(t *testing.T) {
	c := New()
	c.Add(LeaseReclaimed, "expired")
	if got := c.Total(AdmissionRejected); got != 0 {
		t.Fatalf("Total(admission) = %d, want 0", got)
	}
	if got := c.Total(LeaseReclaimed); got != 1 {
		t.Fatalf("Total(lease) = %d, want 1", got)
	}
}

func TestAnUnknownKindIsIgnored(t *testing.T) {
	c := New()
	c.Add(Kind("made.up"), "x")
	if got := len(c.Snapshot()); got != 0 {
		t.Fatalf("snapshot has %d lines, want 0", got)
	}
}

func TestTheReasonMapIsACopy(t *testing.T) {
	c := New()
	c.Add(ClaimRefused, "stale")
	c.Reasons(ClaimRefused)["stale"] = 99
	if got := c.Reasons(ClaimRefused)["stale"]; got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}

func TestASnapshotIsSortedAndStable(t *testing.T) {
	c := New()
	c.Add(LimitRefused, "size")
	c.Add(AdmissionRejected, "limit")
	c.Add(AdmissionRejected, "auth")
	got := c.Snapshot()
	if len(got) != 3 {
		t.Fatalf("snapshot = %+v", got)
	}
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.Kind > cur.Kind || (prev.Kind == cur.Kind && prev.Reason > cur.Reason) {
			t.Fatalf("snapshot is not sorted: %+v", got)
		}
	}
}

func TestConcurrentAddsAreCounted(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Add(AdmissionRejected, "auth")
		}()
	}
	wg.Wait()
	if got := c.Total(AdmissionRejected); got != 50 {
		t.Fatalf("Total() = %d, want 50", got)
	}
}

func TestANilCounterIsUsable(t *testing.T) {
	var c *Counters
	c.Add(AdmissionRejected, "auth")
	if got := c.Total(AdmissionRejected); got != 0 {
		t.Fatalf("Total() = %d, want 0", got)
	}
	if c.Reasons(AdmissionRejected) != nil {
		t.Fatal("Reasons() on a nil counter returned a map")
	}
	if c.Snapshot() != nil {
		t.Fatal("Snapshot() on a nil counter returned data")
	}
}
