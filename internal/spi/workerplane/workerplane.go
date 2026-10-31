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

// Assignment is one Attempt's signed Lease and WorkOrder, as the Control Plane
// stored them when it emitted them. The bytes are hostile to the receiver until
// it verifies them; the identifiers are hints for logging and carry no
// authority.
type Assignment struct {
	AttemptID string
	LeaseID   string
	// LeaseState is the delivery-level currency of the Lease at listing time
	// (ADR-0030 §2.2): current while the validity window is open, revoked once
	// the emission was aborted. The Worker must stop a revoked Attempt and may
	// not start one.
	LeaseState        workerplaneLeaseState
	LeaseEnvelope     []byte
	WorkOrderEnvelope []byte
}

// workerplaneLeaseState mirrors statestore.LeaseDeliveryState without the SPI
// dependency.
type workerplaneLeaseState = string

const (
	LeaseStateCurrent = "current"
	LeaseStateRevoked = "revoked"
)

// AssignmentSource lists the assignments of the authenticated Worker. The
// transport produces the Principal; no request field may name another Worker.
type AssignmentSource interface {
	ListAssignments(ctx context.Context, principal Principal) ([]Assignment, error)
}
