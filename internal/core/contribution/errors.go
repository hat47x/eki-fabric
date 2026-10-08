package contribution

import "errors"

// Sentinel errors returned by this package. Callers match them with errors.Is.
// Wrapped messages name the offending field but never echo its value, so
// identities and provenance text do not leak through error strings.
var (
	ErrMissingField         = errors.New("contribution: required field is missing")
	ErrFieldTooLong         = errors.New("contribution: field exceeds maximum length")
	ErrInvalidField         = errors.New("contribution: field is malformed")
	ErrUnknownMetric        = errors.New("contribution: unknown metric")
	ErrUnknownUnit          = errors.New("contribution: unknown unit")
	ErrUnitMismatch         = errors.New("contribution: unit is not declared by metric")
	ErrNegativeQuantity     = errors.New("contribution: negative quantity")
	ErrZeroQuantity         = errors.New("contribution: zero quantity")
	ErrQuantityTooLarge     = errors.New("contribution: quantity exceeds maximum")
	ErrInvalidWindow        = errors.New("contribution: measurement window is empty or reversed")
	ErrWindowTooLong        = errors.New("contribution: measurement window exceeds maximum duration")
	ErrUnknownResultStatus  = errors.New("contribution: unknown result status")
	ErrResultNotAccepted    = errors.New("contribution: result is not accepted")
	ErrUnknownAssurance     = errors.New("contribution: unknown assurance level")
	ErrUnknownObserver      = errors.New("contribution: unknown observer kind")
	ErrObserverConflict     = errors.New("contribution: observer identity is bound to conflicting kinds")
	ErrObservationMismatch  = errors.New("contribution: observations disagree")
	ErrWorkerNotIndependent = errors.New("contribution: worker observation cannot be combined with other observers")
	ErrUnsupportedSchema    = errors.New("contribution: unsupported schema version")
	ErrInvalidCorrection    = errors.New("contribution: invalid correction")
)
