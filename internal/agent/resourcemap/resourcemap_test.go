package resourcemap

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
)

var host = Host{CPUMillis: 4000, MemoryBytes: 8 << 30}

func TestTheStrictestResourceDecides(t *testing.T) {
	// 25% of the CPU, 50% of the memory: the memory is the binding one.
	need, err := Demand(1000, 4<<30, host)
	if err != nil {
		t.Fatal(err)
	}
	if got := need[governor.ResourceCPU]; got != 25 {
		t.Fatalf("cpu = %d, want 25", got)
	}
	if got := need[governor.ResourceMemory]; got != 50 {
		t.Fatalf("memory = %d, want 50", got)
	}
	if got := Strictest(need); got != 50 {
		t.Fatalf("Strictest() = %d, want 50: the sum would count one short resource twice", got)
	}
}

func TestATinyRequestIsStillOnePercent(t *testing.T) {
	need, err := Demand(1, 1, host)
	if err != nil {
		t.Fatal(err)
	}
	if got := Strictest(need); got != 1 {
		t.Fatalf("Strictest() = %d, want 1: rounding to 0 would make a request look like none", got)
	}
}

func TestAnOversizedRequestIsCappedAtOneHundred(t *testing.T) {
	need, err := Demand(40000, 80<<30, host)
	if err != nil {
		t.Fatal(err)
	}
	if got := Strictest(need); got != 100 {
		t.Fatalf("Strictest() = %d, want 100", got)
	}
}

func TestWithoutHostTotalsNothingIsGuessed(t *testing.T) {
	if _, err := Demand(1000, 1<<30, Host{}); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("Demand() error = %v, want %v", err, ErrUnknownHost)
	}
}

func TestANegativeRequestIsRefused(t *testing.T) {
	if _, err := Demand(-1, 1<<30, host); err == nil {
		t.Fatal("Demand() accepted a negative request")
	}
}

func TestARequestThatAsksForNothingMapsToNoResource(t *testing.T) {
	need, err := Demand(0, 0, host)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 0 || Strictest(need) != 0 {
		t.Fatalf("Demand() = %v", need)
	}
}
