package clientapi

import (
	"time"

	"github.com/hat47x/eki-fabric/internal/core/measurement"
)

// ArtifactAccess is one line of the retrieval audit (spec/external/README.md,
// #494). It is recorded whether the answer was the body or a refusal: a refused
// attempt is the part an investigation needs, and it is the part a success-only
// log would lose.
type ArtifactAccess struct {
	// At is when the request was answered.
	At time.Time `json:"at"`
	// Principal is the authenticated caller, empty when authentication failed
	// before the route was reached.
	Principal string `json:"principal"`
	// Artifact is the digest the caller asked for.
	Artifact string `json:"artifact"`
	// Outcome is "served", "not_found" or "expired". It is a closed set so a
	// log reader does not have to parse prose.
	Outcome string `json:"outcome"`
}

// The outcomes an audit line can carry.
const (
	ArtifactServed   = "served"
	ArtifactNotFound = "not_found"
	ArtifactExpired  = "expired"
)

// audit records one access. A configured sink is used when there is one;
// otherwise the line goes to the handler's logger, so a deployment that has not
// set up separate audit storage still keeps the record.
func (h *Handler) audit(principal, artifact, outcome string) {
	entry := ArtifactAccess{At: time.Now().UTC(), Principal: principal, Artifact: artifact, Outcome: outcome}
	if h.artifactAuditErr != nil {
		// The sink can refuse (ADR-0040 §5). Retrieval carries on: the owner-only
		// rule does not depend on the record, and turning an audit outage into an
		// outage of the whole route would punish the requester for something they
		// cannot act on. The gap is counted so it is not silent.
		if err := h.artifactAuditErr(entry); err != nil {
			h.noteUnrecorded("sink", err)
		}
		return
	}
	if h.artifactAudit != nil {
		h.artifactAudit(entry)
		return
	}
	h.log.Info("artifact access",
		"principal", entry.Principal,
		"artifact", entry.Artifact,
		"outcome", entry.Outcome,
	)
}

// noteUnrecorded counts one access the audit sink refused and logs why. The count
// is the durable signal (ADR-0039): a log line can be missed, a number that keeps
// rising is harder to ignore.
func (h *Handler) noteUnrecorded(reason string, err error) {
	if h.counters != nil {
		h.counters.Add(measurement.AuditUnrecorded, reason)
	}
	if h.log != nil {
		h.log.Error("artifact access was not recorded", "reason", reason, "error", err.Error())
	}
}
