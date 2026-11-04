package resourcemap

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
)

var host = Host{CPUMillis: 4000, MemoryBytes: 8 << 30, DiskBytes: 100 << 30}

func TestTheStrictestResourceDecides(t *testing.T) {
	// 25% of the CPU, 50% of the memory: the memory is the binding one.
	need, err := Demand(Request{CPUMillis: 1000, MemoryBytes: 4 << 30}, host)
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
	need, err := Demand(Request{CPUMillis: 1, MemoryBytes: 1}, host)
	if err != nil {
		t.Fatal(err)
	}
	if got := Strictest(need); got != 1 {
		t.Fatalf("Strictest() = %d, want 1: rounding to 0 would make a request look like none", got)
	}
}

func TestAnOversizedRequestIsCappedAtOneHundred(t *testing.T) {
	need, err := Demand(Request{CPUMillis: 40000, MemoryBytes: 80 << 30}, host)
	if err != nil {
		t.Fatal(err)
	}
	if got := Strictest(need); got != 100 {
		t.Fatalf("Strictest() = %d, want 100", got)
	}
}

func TestWithoutHostTotalsNothingIsGuessed(t *testing.T) {
	if _, err := Demand(Request{CPUMillis: 1000}, Host{}); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("Demand() error = %v, want %v", err, ErrUnknownHost)
	}
}

func TestANegativeRequestIsRefused(t *testing.T) {
	if _, err := Demand(Request{CPUMillis: -1}, host); err == nil {
		t.Fatal("Demand() accepted a negative request")
	}
}

func TestARequestThatAsksForNothingMapsToNoResource(t *testing.T) {
	need, err := Demand(Request{}, host)
	if err != nil {
		t.Fatal(err)
	}
	if len(need) != 0 || Strictest(need) != 0 {
		t.Fatalf("Demand() = %v", need)
	}
}

func TestDiskIsJudgedAgainstTotalCapacity(t *testing.T) {
	// 25 GiB of a 100 GiB disk. The total, not the free space: a moving denominator
	// would make the same request pass or fail depending on what else runs.
	need, err := Demand(Request{DiskBytes: 25 << 30}, host)
	if err != nil {
		t.Fatal(err)
	}
	if got := need[governor.ResourceDiskIO]; got != 25 {
		t.Fatalf("disk = %d, want 25", got)
	}
}

func TestDiskIsNotJudgedWithoutACapacity(t *testing.T) {
	bare := Host{CPUMillis: 4000, MemoryBytes: 8 << 30}
	need, err := Demand(Request{DiskBytes: 25 << 30}, bare)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := need[governor.ResourceDiskIO]; ok {
		t.Fatalf("disk was judged without a capacity: %v", need)
	}
}
