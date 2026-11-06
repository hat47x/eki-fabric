package assignment

import (
	"sync"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// heldReservation is one Attempt's claim on this endpoint.
type heldReservation struct {
	usage    startgate.Usage
	deadline time.Time
}

// reservations is the ledger of what the running Attempts have reserved on this
// endpoint (ADR-0031 §6.2). The start gate is judged against its total, so a second
// Attempt cannot be admitted on capacity the first one already claimed (#354).
//
// It is a separate type because the bookkeeping has to be right on its own before
// anything depends on it: a ledger that forgets to release a reservation makes the
// endpoint refuse work it could run, and one that releases twice lets two Attempts
// run in the same slot.
type reservations struct {
	mu   sync.Mutex
	held map[ids.AttemptID]heldReservation
}

func newReservations() *reservations {
	return &reservations{held: make(map[ids.AttemptID]heldReservation)}
}

// Hold records what an Attempt reserved until its deadline. Holding the same
// Attempt twice keeps the later value: both writes describe the same start.
func (r *reservations) Hold(attemptID ids.AttemptID, usage startgate.Usage, deadline time.Time) {
	if r == nil || attemptID == "" || usage.Running == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held[attemptID] = heldReservation{usage: usage, deadline: deadline}
}

// Release returns one Attempt's reservation. An Attempt that is not held is
// ignored, so a completion that arrives twice (a retry, or a sweep that raced the
// handler) is harmless.
func (r *reservations) Release(attemptID ids.AttemptID) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.held, attemptID)
}

// Usage is what the gate judges a new start against.
func (r *reservations) Usage() startgate.Usage {
	if r == nil {
		return startgate.Usage{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var total startgate.Usage
	for _, held := range r.held {
		total.Running += held.usage.Running
		total.CPUMillis += held.usage.CPUMillis
		total.MemoryBytes += held.usage.MemoryBytes
		total.GPUDevices += held.usage.GPUDevices
	}
	return total
}

// SweepExpired drops the reservations whose deadline has passed, so a completion
// that never arrived cannot keep the endpoint busy until a restart (ADR-0031 §6.2,
// point 3). It returns how many it dropped.
func (r *reservations) SweepExpired(now time.Time) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped := 0
	for attemptID, held := range r.held {
		if !now.Before(held.deadline) {
			delete(r.held, attemptID)
			dropped++
		}
	}
	return dropped
}
