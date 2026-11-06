package startgate

import (
	"testing"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
)

// Two Attempts that each fit alone do not both fit: the gate compares one request
// with the sum of the running reservations, not with the machine's whole capacity
// (#354).
func TestTwoRequestsThatEachFitDoNotBothFit(t *testing.T) {
	gate := newGateWith(t, testNow, 4)
	decision := governorDecision(healthyCeilings())
	machine := MachineProfile{CPUMillis: 6000, MemoryBytes: 10 << 30}

	first, err := gate.Check(Request{Admission: admission(t, testNow, nil), Governor: decision, Machine: machine})
	if err != nil {
		t.Fatalf("the first request was refused: %v", err)
	}

	second := Request{Admission: admission(t, testNow, nil), Governor: decision, Machine: machine, InUse: UsageOf(first)}
	if _, err := gate.Check(second); err == nil {
		t.Fatal("the second request fit on top of the first: the gate judged it against the whole machine")
	}
}

// A zero Plan is not a reservation, so a caller can pass its empty slots through.
func TestAZeroPlanReservesNothing(t *testing.T) {
	if got := UsageOf(Plan{}, Plan{}); got.Running != 0 || got.CPUMillis != 0 || got.MemoryBytes != 0 {
		t.Fatalf("UsageOf(zero plans) = %+v, want nothing", got)
	}
}

var _ = governor.ResourceCPU
