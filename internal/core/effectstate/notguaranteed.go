package effectstate

// Fixed NotGuaranteed statements. Each Decision lists the statements that apply
// to it, so a reader can see what the decision does not establish.
const (
	ngInputInvalid          = "no state change is decided from invalid input"
	ngExactlyOnceUnknown    = "exactly-once application is not established for a non-idempotent effect"
	ngIdempotencyHonored    = "the external system is assumed to honor the idempotency key; this package does not verify it"
	ngWorkerReportIsClaim   = "a Worker's success or failure report is a claim and does not confirm the effect"
	ngCancelDoesNotRollBack = "cancellation does not roll back an effect that may already have been applied"
	ngCompletionPolicy      = "application-level completion policy is not evaluated here"
	ngReceiptAuthorityOnly  = "the effect state relies on the external receipt's authority, which this package does not verify"
	ngPureResultUnverified  = "result correctness is not verified by this package"
	ngIntentDurability      = "the intent record is assumed to be durable; this package cannot check it"
	ngNoEffectEvidence      = "no dispatch is recorded for the current generation; this relies on the caller's dispatch record being durable"
	ngReconcileBudgetBounds = "the reconciliation budget bounds queries; it does not establish the external outcome"
	ngStaleMessage          = "the stale message does not establish anything about the current generation"
)

// newDecision builds a Decision with a fresh NotGuaranteed slice, so callers
// cannot alias the package-level constants.
func newDecision(action Action, reason ReasonCode, state EffectState, unknownInBatch int, notGuaranteed ...string) Decision {
	ng := make([]string, len(notGuaranteed))
	copy(ng, notGuaranteed)
	return Decision{
		Action:                action,
		Reason:                reason,
		EffectState:           state,
		NotGuaranteed:         ng,
		UnknownEffectsInBatch: unknownInBatch,
	}
}
