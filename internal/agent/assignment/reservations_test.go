package assignment

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var (
	attemptA = ids.AttemptID("atm_0000000000000000000000000000000a")
	attemptB = ids.AttemptID("atm_0000000000000000000000000000000b")
)

func usage(cpu, memory uint64) startgate.Usage {
	return startgate.Usage{Running: 1, CPUMillis: cpu, MemoryBytes: memory}
}

// The gate must see the sum, or a second Attempt is admitted on capacity the first
// one already claimed (#354).
func TestTheLedgerSumsTheHeldReservations(t *testing.T) {
	ledger := newReservations()
	later := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	ledger.Hold(attemptA, usage(2400, 4<<30), later)
	ledger.Hold(attemptB, usage(2400, 4<<30), later)

	got := ledger.Usage()
	if got.Running != 2 || got.CPUMillis != 4800 || got.MemoryBytes != 8<<30 {
		t.Fatalf("Usage() = %+v, want both reservations summed", got)
	}
}

func TestReleaseGivesTheCapacityBack(t *testing.T) {
	ledger := newReservations()
	later := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	ledger.Hold(attemptA, usage(2400, 4<<30), later)
	ledger.Release(attemptA)
	if got := ledger.Usage(); got.Running != 0 || got.CPUMillis != 0 {
		t.Fatalf("Usage() = %+v, want nothing after the release", got)
	}
}

// A completion that arrives twice must not give the capacity back twice: that would
// let two Attempts share one slot.
func TestReleasingTwiceIsHarmless(t *testing.T) {
	ledger := newReservations()
	later := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	ledger.Hold(attemptA, usage(2400, 4<<30), later)
	ledger.Hold(attemptB, usage(1000, 1<<30), later)

	ledger.Release(attemptA)
	ledger.Release(attemptA)

	if got := ledger.Usage(); got.Running != 1 || got.CPUMillis != 1000 {
		t.Fatalf("Usage() = %+v, want only the other Attempt", got)
	}
}

// A completion that never arrives must not hold the endpoint until a restart.
func TestSweepDropsReservationsPastTheirDeadline(t *testing.T) {
	ledger := newReservations()
	now := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	ledger.Hold(attemptA, usage(2400, 4<<30), now.Add(-time.Minute))
	ledger.Hold(attemptB, usage(1000, 1<<30), now.Add(time.Minute))

	if dropped := ledger.SweepExpired(now); dropped != 1 {
		t.Fatalf("SweepExpired() dropped %d, want 1", dropped)
	}
	if got := ledger.Usage(); got.Running != 1 || got.CPUMillis != 1000 {
		t.Fatalf("Usage() = %+v, want the live reservation kept", got)
	}
}

// An empty hold is not a reservation.
func TestHoldingNothingChangesNothing(t *testing.T) {
	ledger := newReservations()
	ledger.Hold(attemptA, startgate.Usage{}, time.Now())
	ledger.Hold("", usage(2400, 4<<30), time.Now())
	if got := ledger.Usage(); got.Running != 0 {
		t.Fatalf("Usage() = %+v, want nothing", got)
	}
}
