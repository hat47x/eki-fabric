// Package workerplane defines the transport-neutral messages of the Worker
// Plane that the reference implementation already needs.
//
// The target wire protocol is specified in spec/worker/v1/worker.proto.
// These are in-process ports, not an implemented lossless mapping to its
// ResultClaim and EventReceipt messages. Field, outcome, and acknowledgement
// alignment is tracked in Issue #198 before a Worker Protocol transport can
// connect them.
// The Control Plane service and Agent client tests do not establish network
// interoperability or authentication.
package workerplane

import (
	"context"

	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

// Principal is the authenticated Worker. The transport produces it; no field
// of a Claim may raise it.
type Principal struct {
	WorkerIdentity string
	TrustDomain    string
}

// ClaimAck is what the Control Plane reports after accepting a ResultClaim.
// It is not a verification result.
type ClaimAck struct {
	// Decision is the resultclaim.Kind ("new", "duplicate", "stale", "late").
	Decision string
	// AttemptPhase is the phase after applying the claim.
	AttemptPhase string
	// VerificationRequired is true when the Attempt moved to A-VERIFYING.
	VerificationRequired bool
}

// ClaimClient submits one ResultClaim for the authenticated Worker.
type ClaimClient interface {
	SubmitClaim(ctx context.Context, principal Principal, claim resultclaim.Claim) (ClaimAck, error)
}
