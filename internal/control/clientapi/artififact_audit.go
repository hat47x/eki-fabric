package clientapi

import "time"

// ArtifactAccess is one line of the retrieval audit (spec/external/README.md,
// #494). It is recorded whether the answer was the body or a refusal: a refused
// attempt is the part an investigation needs, and it is the part a success-only
// log would lose.
type ArtifactAccess struct {
	// At is when the request was answered.
	At time.Time
	// Principal is the authenticated caller, empty when authentication failed
	// before the route was reached.
	Principal string
	// Artifact is the digest the caller asked for.
	Artifact string
	// Outcome is "served", "not_found" or "expired". It is a closed set so a
	// log reader does not have to parse prose.
	Outcome string
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
