package service

import (
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/measurement"
)

func TestAReclaimedLeaseIsCounted(t *testing.T) {
	counters := measurement.New()
	hook := recallCounter(counters)
	if hook == nil {
		t.Fatal("recallCounter() returned nil for a real counter")
	}
	hook("expired")
	if got := counters.Reasons(measurement.LeaseReclaimed)["expired"]; got != 1 {
		t.Fatalf("expired = %d, want 1", got)
	}
}

func TestWithoutCountersThereIsNoHook(t *testing.T) {
	if recallCounter(nil) != nil {
		t.Fatal("recallCounter(nil) returned a hook")
	}
}
