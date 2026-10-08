package grant

import "errors"

// Sentinel errors returned by this package. Callers match them with errors.Is.
// Wrapped messages name the offending field but never echo its value, so
// identities and policy text do not leak through error strings.
var (
	ErrNilGrant               = errors.New("grant: nil grant (value was not issued by Issue)")
	ErrNilLedger              = errors.New("grant: nil ledger (revocations cannot be recorded or checked)")
	ErrMissingField           = errors.New("grant: required field is missing")
	ErrTooLong                = errors.New("grant: field exceeds maximum length")
	ErrInvalidField           = errors.New("grant: field is malformed")
	ErrTooManyResourceClasses = errors.New("grant: too many resource classes")
	ErrDuplicateResourceClass = errors.New("grant: resource class appears more than once")
	ErrPercentOutOfRange      = errors.New("grant: percent is outside 1..100")
	ErrInvalidWindow          = errors.New("grant: validity window is empty or reversed")
	ErrWindowTooLong          = errors.New("grant: validity window exceeds maximum duration")
	ErrInvalidLeaseLimit      = errors.New("grant: concurrent lease limit is outside 0..100")
	ErrIssuerMismatch         = errors.New("grant: issuer is not the provider the grant names")
	ErrSelfGrant              = errors.New("grant: a party cannot grant itself")
	ErrUnknownPurpose         = errors.New("grant: unknown purpose")
	ErrUnknownReason          = errors.New("grant: unknown revocation reason category")
	ErrRevokerMismatch        = errors.New("grant: revoker is not the provider the grant names")
	ErrAlreadyRevoked         = errors.New("grant: grant is already revoked")
	ErrRevocationBeforeIssue  = errors.New("grant: revocation takes effect before issuance")
	ErrInvalidRequest         = errors.New("grant: admission request is malformed")
	ErrGrantNotActive         = errors.New("grant: grant is not active at the decision time")
	ErrResourceNotGranted     = errors.New("grant: resource class is not granted")
	ErrCeilingExceeded        = errors.New("grant: request exceeds the resource class ceiling")
	ErrLeaseLimitReached      = errors.New("grant: concurrent lease limit is reached")
	ErrGrantNotRevoked        = errors.New("grant: grant is not revoked")
	ErrRevocationTimeMismatch = errors.New("grant: grants were not revoked at the same time")
	ErrDuplicateGrant         = errors.New("grant: the same grant appears more than once")
	ErrTooManyGrants          = errors.New("grant: too many grants in one reclaim plan")
	ErrInvalidRunningLeases   = errors.New("grant: running lease count is outside its bound")
	ErrInvalidReclaimConfig   = errors.New("grant: reclaim configuration is outside its bounds")
	ErrInsufficientSlots      = errors.New("grant: reclaim window has too few slots for the running leases")
)
