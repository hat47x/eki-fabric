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

// LeaseDecoder decodes hostile P2 eki.lease/v1 payload bytes. Successful
// decoding does not authenticate the signature or authorize execution.
type LeaseDecoder interface {
	DecodeLease(payload []byte) (decisionpayload.Lease, error)
}

// LeaseCodec adds canonical encoding for the LSE-1 producer. Workers only
// need LeaseDecoder. Neither operation proves current Lease authority.
type LeaseCodec interface {
	LeaseDecoder
	EncodeLease(lease decisionpayload.Lease) ([]byte, error)
}
