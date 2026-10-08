// Package effectstate decides what the Control Plane may do with a Task whose
// external side effect was dispatched to a Worker but whose outcome is unknown.
//
// The package is pure: it reads no clock, performs no I/O, and starts no
// goroutines. Same input yields the same Decision. Two rules are central. An
// effect that may have happened is never silently repeated, and an unknown
// outcome is never reported as failed or cancelled. When the input is
// ambiguous, the package chooses the conservative action: HOLD over RETRY, and
// UNKNOWN over FAILED. Any inconsistent input fails closed to HOLD with an
// input-invalid reason.
package effectstate

import "errors"

// EffectKind classifies the external side effect of a Task.
type EffectKind int

const (
	// EffectKindUnknown is the zero value. It is always rejected.
	EffectKindUnknown EffectKind = iota
	// EffectKindPureCompute means the Task has no external effect.
	EffectKindPureCompute
	// EffectKindIdempotent means the external system honors an idempotency key
	// that identifies the operation and its intent.
	EffectKindIdempotent
	// EffectKindNonIdempotent means the external system gives no such guarantee.
	EffectKindNonIdempotent
)

// String returns a stable name for the kind.
func (k EffectKind) String() string {
	switch k {
	case EffectKindPureCompute:
		return "PURE_COMPUTE"
	case EffectKindIdempotent:
		return "IDEMPOTENT"
	case EffectKindNonIdempotent:
		return "NON_IDEMPOTENT"
	default:
		return "UNKNOWN_KIND"
	}
}

// Valid reports whether k is one of the three defined kinds.
func (k EffectKind) Valid() bool {
	return k == EffectKindPureCompute || k == EffectKindIdempotent || k == EffectKindNonIdempotent
}

// IntentRecord is the durable record, written before dispatch, that an effect
// with a given Generation is intended.
type IntentRecord struct {
	// Generation is the dispatch generation the intent belongs to. Generations
	// are monotonic integers supplied by the caller and start at 1.
	Generation uint64
}

// ObservedOutcome is what the Control Plane currently knows about the effect
// of one Generation.
type ObservedOutcome int

const (
	// OutcomeNone means nothing has been observed for the Generation.
	OutcomeNone ObservedOutcome = iota
	// OutcomeWorkerClaimedSuccess is a Worker's own report of success. It is a
	// claim and does not confirm the effect.
	OutcomeWorkerClaimedSuccess
	// OutcomeWorkerClaimedFailure is a Worker's own report of failure. It is a
	// claim and does not prove the effect was not applied.
	OutcomeWorkerClaimedFailure
	// OutcomeReceiptApplied is an authoritative receipt from the external
	// system that the effect was applied.
	OutcomeReceiptApplied
	// OutcomeReceiptNotApplied is an authoritative receipt from the external
	// system that proves the effect was not applied.
	OutcomeReceiptNotApplied
	// OutcomeWorkerLost means the Worker holding the Generation disappeared
	// without reporting an outcome.
	OutcomeWorkerLost
)

// String returns a stable name for the observed outcome.
func (o ObservedOutcome) String() string {
	switch o {
	case OutcomeNone:
		return "NONE"
	case OutcomeWorkerClaimedSuccess:
		return "WORKER_CLAIMED_SUCCESS"
	case OutcomeWorkerClaimedFailure:
		return "WORKER_CLAIMED_FAILURE"
	case OutcomeReceiptApplied:
		return "RECEIPT_APPLIED"
	case OutcomeReceiptNotApplied:
		return "RECEIPT_NOT_APPLIED"
	case OutcomeWorkerLost:
		return "WORKER_LOST"
	default:
		return "INVALID_OUTCOME"
	}
}

// Valid reports whether o is one of the defined outcomes.
func (o ObservedOutcome) Valid() bool {
	return o >= OutcomeNone && o <= OutcomeWorkerLost
}

// Input is the complete, self-contained state the decision is based on.
type Input struct {
	// Kind is the effect kind of the Task.
	Kind EffectKind
	// Intent is the durable intent record for the current generation. A zero
	// Generation means no intent was recorded.
	Intent IntentRecord
	// DispatchedGeneration is the highest generation for which a dispatch was
	// attempted. Zero means no dispatch was ever attempted.
	DispatchedGeneration uint64
	// CurrentGeneration is the generation the Control Plane is deciding about.
	CurrentGeneration uint64
	// Outcome is the observed outcome for CurrentGeneration.
	Outcome ObservedOutcome
	// AttemptsUsed is the number of attempts already made, counting the
	// current one. It must be at least 1 once any generation is dispatched.
	AttemptsUsed int
	// AttemptLimit is the Task's maximum number of attempts. It must be positive.
	AttemptLimit int
	// ReconciliationsUsed is the number of reconciliation queries already spent
	// on this effect.
	ReconciliationsUsed int
	// CancellationRequested reports whether a cancellation intent is recorded.
	CancellationRequested bool
	// UnknownEffectsInBatch is the number of effects in unknown state within the
	// same reclaim batch, as counted by the caller. It must be non-negative. It
	// is echoed as data only and never changes the decision.
	UnknownEffectsInBatch int
}

// Config holds per-effect policy supplied at construction.
type Config struct {
	// ReconciliationBudget is the maximum number of reconciliation attempts per
	// effect. It must be positive; zero is rejected at construction.
	ReconciliationBudget int
}

// ErrInvalidConfig is returned when a Config violates its constraints.
var ErrInvalidConfig = errors.New("effectstate: invalid config")
