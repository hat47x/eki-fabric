package decisiondecoder

import "github.com/hat47x/eki-fabric/internal/core/decisionpayload"

// PlacementApprovalDecoder converts the already extracted COSE payload bytes
// into the exact closed P2 Placement Approval semantic shape.
//
// Successful decoding does not prove that the COSE signature is valid and does
// not authorize a Lease. Callers must still run decisiontrust verification and
// current LSE-1 state/policy checks.
type PlacementApprovalDecoder interface {
	DecodePlacementApproval(payload []byte) (decisionpayload.PlacementApproval, error)
}
