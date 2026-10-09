package resourceprovider

import (
	"errors"
	"fmt"
	"maps"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
)

var (
	// ErrNotSuccess is returned when a provider claim does not state success. A
	// failed or unfinished lease yields no result to accept.
	ErrNotSuccess = errors.New("resourceprovider: claim does not state success")
	// ErrClaimLeaseMismatch is returned when a claim names a lease other than the one
	// the binding was prepared for.
	ErrClaimLeaseMismatch = errors.New("resourceprovider: claim belongs to another lease")
)

// ResultBinding holds the values that connect one lease to a Task attempt. The
// trusted boundary supplies every field. A ProviderClaim carries none of them, and
// the provider's own words never fill them in.
//
// The provider reports only that a lease succeeded. Which Task and Attempt the lease
// served, which Worker identity produced the output, the digest of that output, and
// the identity dimensions of the producer all come from EKI's own records.
type ResultBinding struct {
	// Lease is the lease that the binding was prepared for. A claim for any other
	// lease is refused.
	Lease ids.LeaseID
	// TaskID and AttemptID bind the claim to one Task attempt.
	TaskID    string
	AttemptID string
	// WorkerID is the producing Worker identity. It is never a verifier.
	WorkerID string
	// OutputDigest is the lowercase hexadecimal SHA-256 of the output the lease produced.
	OutputDigest string
	// Dimensions are the producer's identity values from the trusted identity records.
	Dimensions map[resultaccept.Dimension]string
}

// ResultClaimFromProvider converts a provider success claim into the input that
// resultaccept.Decide takes.
//
// Rule (c): the result is a ResultClaim, not an accepted result. The conversion adds
// no verification and produces no decision. A failed claim is refused, and so is a
// claim for another lease. The Sequence of the claim is not used.
//
// The function reads no clock and performs no I/O.
func ResultClaimFromProvider(claim ProviderClaim, b ResultBinding) (resultaccept.ResultClaim, error) {
	if claim.Outcome != ClaimSucceeded {
		return resultaccept.ResultClaim{}, fmt.Errorf("%w: outcome %q", ErrNotSuccess, claim.Outcome)
	}
	if claim.LeaseID != b.Lease {
		return resultaccept.ResultClaim{}, fmt.Errorf("%w: claim names %q, binding is %q", ErrClaimLeaseMismatch, claim.LeaseID, b.Lease)
	}
	return resultaccept.ResultClaim{
		TaskID:       b.TaskID,
		AttemptID:    b.AttemptID,
		WorkerID:     b.WorkerID,
		OutputDigest: b.OutputDigest,
		Dimensions:   maps.Clone(b.Dimensions),
	}, nil
}

// DecideProviderResult is the path in this package from a provider success to a
// decision. It converts the claim with ResultClaimFromProvider, then passes the
// caller's verifications to resultaccept.Decide. It adds no verification of its own.
// So an Accepted outcome needs independent verifications that the trusted boundary
// supplied, and a provider's report alone yields a held decision.
//
// Errors from the conversion are returned unchanged. Errors from Decide wrap
// resultaccept.ErrRejectedInput.
func DecideProviderResult(claim ProviderClaim, b ResultBinding, verifications []resultaccept.Verification, output resultaccept.OutputClass, policy *resultaccept.Policy) (resultaccept.Decision, error) {
	rc, err := ResultClaimFromProvider(claim, b)
	if err != nil {
		return resultaccept.Decision{}, err
	}
	return resultaccept.Decide(rc, verifications, output, policy)
}
