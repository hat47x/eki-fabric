package federation

import "errors"

// Sentinel errors. Verification failures wrap one of these inside a Rejection,
// and registry mutations return them directly, so callers can test them with
// errors.Is.
var (
	ErrInvalidTrustDomain  = errors.New("federation: invalid trust domain name")
	ErrInvalidLink         = errors.New("federation: invalid federation link")
	ErrInvalidBundle       = errors.New("federation: invalid trust bundle")
	ErrMalformedIdentity   = errors.New("federation: malformed identity")
	ErrInvalidTime         = errors.New("federation: a verification or revocation time is required")
	ErrNilRegistry         = errors.New("federation: nil or uninitialized registry")
	ErrInvalidMaxBundleAge = errors.New("federation: maximum bundle age must be positive")
	ErrAlreadyFederated    = errors.New("federation: trust domain is already federated")
	ErrAuthorityKeyReused  = errors.New("federation: bundle authority key is already pinned for another trust domain")
	ErrDomainMismatch      = errors.New("federation: bundle domain does not match the link domain")
	ErrBundleSignature     = errors.New("federation: bundle is not signed by the pinned authority")
	ErrRollback            = errors.New("federation: bundle sequence is lower than the bound sequence")
	ErrEquivocation        = errors.New("federation: bundle sequence is bound to a different digest")
	ErrNotFederated        = errors.New("federation: trust domain is not federated")
	ErrDomainRevoked       = errors.New("federation: trust domain is revoked")
	ErrAlreadyRevoked      = errors.New("federation: trust domain is already revoked")
	ErrUnknownPurpose      = errors.New("federation: purpose is not in the closed vocabulary")
	ErrPurposeNotAllowed   = errors.New("federation: purpose is not allowed by the federation link")
	ErrUnknownKey          = errors.New("federation: signing key is not in the bound trust bundle")
	ErrSignatureInvalid    = errors.New("federation: identity signature does not verify")
	ErrTrustBundleInvalid  = errors.New("federation: trust bundle is not valid at the verification time")
	ErrTrustBundleStale    = errors.New("federation: trust bundle is stale")
)
