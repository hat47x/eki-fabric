package service

import (
	"bytes"
	"sync"

	"github.com/hat47x/eki-fabric/internal/agent/spool"
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
	// spool and maxBytes mirror every body to disk (ADR-0037, #508). Both zero
	// means the hold is memory-only, which is what a deployment without a state
	// directory gets.
	spool    *spool.Store
	maxBytes int64
	// spoolErr is the last mirror failure. A failure here does not lose the
	// in-memory copy, but it means the body will not survive a restart, so the
	// caller can report it rather than discover it after a crash.
	spoolErr error
}

func newBodyHold() *bodyHold {
	return &bodyHold{bodies: map[ids.AttemptID][]byte{}}
}

// newDurableBodyHold keeps every body on disk as well as in memory. The body is
// written before the claim is sent, so a crash between the claim and the send no
// longer loses it (#508).
func newDurableBodyHold(store *spool.Store, maxBytes int64) *bodyHold {
	hold := newBodyHold()
	hold.spool = store
	hold.maxBytes = maxBytes
	return hold
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
	if h.spool == nil {
		return
	}
	if _, err := h.spool.Put(bytes.NewReader(body), h.maxBytes); err != nil {
		h.spoolErr = err
	}
}

// LastSpoolError reports the last failure to mirror a body to disk, if any.
func (h *bodyHold) LastSpoolError() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.spoolErr
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
