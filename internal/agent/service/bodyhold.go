package service

import (
	"sync"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// bodyHold keeps the output bytes of completed Attempts on this Agent until
// the claim has been acknowledged and the body sent (ADR-0034 2.1). Bodies
// are keyed by Attempt, so a body is only ever sent for the Attempt that
// produced it: a delayed claim delivery, a later Attempt, or a second
// Runner instance cannot hand one Attempt another Attempt's bytes (#465).
//
// The hold lives in memory. An Agent restart drops bodies that were not sent
// yet, which ADR-0034 accepts; the Control Plane then shows an accepted
// result without a body.
type bodyHold struct {
	mu     sync.Mutex
	bodies map[ids.AttemptID][]byte
}

func newBodyHold() *bodyHold {
	return &bodyHold{bodies: map[ids.AttemptID][]byte{}}
}

// Put keeps a copy of body for the Attempt. Empty bodies are not kept: an
// empty output has nothing to send.
func (h *bodyHold) Put(attempt ids.AttemptID, body []byte) {
	if h == nil || len(body) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bodies[attempt] = append([]byte(nil), body...)
}

// Take removes and returns the body kept for the Attempt.
func (h *bodyHold) Take(attempt ids.AttemptID) ([]byte, bool) {
	if h == nil {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	body, ok := h.bodies[attempt]
	delete(h.bodies, attempt)
	return body, ok
}
