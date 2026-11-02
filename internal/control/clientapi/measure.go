package clientapi

import (
	"net/http"

	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/measurement"
)

// countRejection records that a request was not accepted (ADR-0039 §2.1). The
// reason is the status, because that is the vocabulary the Client already sees:
// an operator reading the numbers is asking "how often was it auth, a duplicate,
// a limit or something else", and the response code answers exactly that.
//
// Only rejections caused by the request are counted. A 500 is EKI's own fault,
// and counting it beside "the Client was told no" would make the numbers describe
// two different things.
func (h *Handler) countRejection(ae *admission.Error, status int) {
	if h.counters == nil {
		return
	}
	reason := ""
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		reason = "auth"
	case http.StatusConflict:
		reason = "duplicate"
	case http.StatusTooManyRequests, http.StatusRequestEntityTooLarge:
		reason = "limit"
	case http.StatusBadRequest:
		reason = "invalid"
	default:
		return
	}
	h.counters.Add(measurement.AdmissionRejected, reason)
}

// SetCounters connects the measurement counters (ADR-0039). Nil leaves the
// handler uncounted, which a deployment that keeps no numbers may prefer.
func (h *Handler) SetCounters(c *measurement.Counters) { h.counters = c }

// WithCounters is SetCounters for a handler that is being built.
func (h *Handler) WithCounters(c *measurement.Counters) *Handler {
	h.counters = c
	return h
}
