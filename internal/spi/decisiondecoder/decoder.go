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

// LeaseCodec converts between the closed P2 `eki.lease/v1` semantic shape and
// its RFC 8949 Core Deterministic Encoding.
//
// EncodeLease is used only by LSE-1 to build the bytes it signs. DecodeLease
// treats its input as hostile. Neither operation authorizes a Lease: the
// signed envelope, key, Trust Bundle, and current Lease state must still be
// verified.
type LeaseCodec interface {
	EncodeLease(lease decisionpayload.Lease) ([]byte, error)
	DecodeLease(payload []byte) (decisionpayload.Lease, error)
}
