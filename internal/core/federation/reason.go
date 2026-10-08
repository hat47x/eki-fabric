package federation

import (
	"errors"
	"fmt"
)

// Reason is a stable code that explains why a verification was rejected. The
// code strings are part of the package contract and are safe to log and match on.
type Reason string

const (
	// ReasonMalformedInput means the registry, the time, or the identity fields were missing or malformed.
	ReasonMalformedInput Reason = "MALFORMED_INPUT"
	// ReasonUnknownPurpose means the purpose is not in the closed vocabulary.
	ReasonUnknownPurpose Reason = "UNKNOWN_PURPOSE"
	// ReasonNotFederated means the claimed trust domain has no federation link and binding.
	ReasonNotFederated Reason = "DOMAIN_NOT_FEDERATED"
	// ReasonPurposeNotAllowed means the link does not allow the requested purpose.
	ReasonPurposeNotAllowed Reason = "PURPOSE_NOT_ALLOWED"
	// ReasonDomainRevoked means the domain was revoked at or before the verification time.
	ReasonDomainRevoked Reason = "DOMAIN_REVOKED"
	// ReasonTrustBundleInvalid means the bound bundle was validated after the verification time.
	ReasonTrustBundleInvalid Reason = "TRUST_BUNDLE_INVALID"
	// ReasonTrustBundleStale means the bound bundle was validated longer ago than the maximum age.
	ReasonTrustBundleStale Reason = "TRUST_BUNDLE_STALE"
	// ReasonUnknownKey means the key ID is not in the bundle that is currently bound to the claimed domain.
	ReasonUnknownKey Reason = "UNKNOWN_KEY"
	// ReasonSignatureInvalid means the signature does not verify with the bound key for the claimed domain.
	ReasonSignatureInvalid Reason = "SIGNATURE_INVALID"
)

// Rejection is the error returned by every verification that does not succeed.
// Reason is the stable code, and Unwrap returns the underlying sentinel error.
type Rejection struct {
	Reason Reason
	cause  error
}

// Error returns the reason code followed by the cause, if there is one.
func (e *Rejection) Error() string {
	if e == nil {
		return "<nil rejection>"
	}
	if e.cause == nil {
		return string(e.Reason)
	}
	return fmt.Sprintf("%s: %v", e.Reason, e.cause)
}

// Unwrap returns the sentinel error that caused the rejection.
func (e *Rejection) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// ReasonOf returns the reason code of the first Rejection in err's chain. It
// returns false for nil and for any error that is not a Rejection.
func ReasonOf(err error) (Reason, bool) {
	var rejection *Rejection
	if !errors.As(err, &rejection) || rejection == nil {
		return "", false
	}
	return rejection.Reason, true
}

func reject(reason Reason, cause error) error {
	return &Rejection{Reason: reason, cause: cause}
}
