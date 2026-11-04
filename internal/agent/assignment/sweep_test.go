package assignment

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/startgate"
)

// The poll is the service's periodic beat, so the sweep rides on it: a reservation
// whose completion never arrived is returned without a timer of its own (#354).
func TestThePollSweepsReservationsPastTheirDeadline(t *testing.T) {
	now := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	service := &Service{reservations: newReservations()}
	service.reservations.Hold(attemptA, startgate.Usage{Running: 1, CPUMillis: 2400}, now.Add(-time.Minute))
	service.reservations.Hold(attemptB, startgate.Usage{Running: 1, CPUMillis: 1000}, now.Add(time.Minute))

	if dropped := service.sweepReservations(now); dropped != 1 {
		t.Fatalf("sweepReservations() dropped %d, want 1", dropped)
	}
	if got := service.reservations.Usage(); got.Running != 1 || got.CPUMillis != 1000 {
		t.Fatalf("Usage() = %+v, want the live reservation kept", got)
	}
}

// Nothing to drop is the normal case and must stay free of side effects.
func TestTheSweepKeepsLiveReservations(t *testing.T) {
	now := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	service := &Service{reservations: newReservations()}
	service.reservations.Hold(attemptA, startgate.Usage{Running: 1, CPUMillis: 2400}, now.Add(time.Hour))

	if dropped := service.sweepReservations(now); dropped != 0 {
		t.Fatalf("sweepReservations() dropped %d, want 0", dropped)
	}
	if got := service.reservations.Usage(); got.Running != 1 {
		t.Fatalf("Usage() = %+v, want the reservation kept", got)
	}
}
