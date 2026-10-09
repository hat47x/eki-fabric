package resourceprovider

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// TestClassifyPreemptionIsReclaim covers rule (b).
func TestClassifyPreemptionIsReclaim(t *testing.T) {
	cases := []struct {
		kind EventKind
		want Disposition
	}{
		{EventStarted, DispositionRunning},
		{EventCompleted, DispositionClaimed},
		{EventFailed, DispositionAttemptFailed},
		{EventPreempted, DispositionReclaim},
		{EventRequeued, DispositionInternalRetry},
	}
	for _, tc := range cases {
		got, err := Classify(tc.kind)
		if err != nil {
			t.Fatalf("Classify(%q) error = %v", tc.kind, err)
		}
		if got != tc.want {
			t.Fatalf("Classify(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}

	if d, _ := Classify(EventPreempted); d.CountsAsAttemptFailure() {
		t.Fatal("a preemption counted as an attempt failure, want reclaim only")
	}
	if d, _ := Classify(EventFailed); !d.CountsAsAttemptFailure() {
		t.Fatal("a reported failure did not count as an attempt failure")
	}
	for _, d := range []Disposition{DispositionRunning, DispositionClaimed, DispositionReclaim, DispositionInternalRetry} {
		if d.CountsAsAttemptFailure() {
			t.Fatalf("disposition %q counted as an attempt failure", d)
		}
	}

	if _, err := Classify("vanished"); !errors.Is(err, ErrUnknownEvent) {
		t.Fatalf("Classify(unknown) error = %v, want ErrUnknownEvent", err)
	}
}

// TestClaimFromEventIsNotAResult covers rule (c).
func TestClaimFromEventIsNotAResult(t *testing.T) {
	lease, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	completed := Event{LeaseID: lease, Sequence: 2, Kind: EventCompleted}

	claim, ok := ClaimFromEvent(completed)
	if !ok {
		t.Fatal("ClaimFromEvent(completed) ok = false, want a claim")
	}
	if claim.Outcome != ClaimSucceeded {
		t.Fatalf("outcome = %q, want %q", claim.Outcome, ClaimSucceeded)
	}
	if claim.LeaseID != lease || claim.Sequence != 2 {
		t.Fatalf("claim = %+v, want lease and sequence of the source event", claim)
	}

	failed, ok := ClaimFromEvent(Event{LeaseID: lease, Sequence: 3, Kind: EventFailed})
	if !ok || failed.Outcome != ClaimFailed {
		t.Fatalf("ClaimFromEvent(failed) = %+v, %v, want failed claim", failed, ok)
	}

	for _, kind := range []EventKind{EventStarted, EventPreempted, EventRequeued} {
		if _, ok := ClaimFromEvent(Event{LeaseID: lease, Sequence: 1, Kind: kind}); ok {
			t.Fatalf("ClaimFromEvent(%q) ok = true, want no outcome", kind)
		}
	}
}
