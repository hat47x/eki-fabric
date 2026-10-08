package effectstate

import "fmt"

// Decider holds the per-effect policy. It is immutable after construction, and
// every method is a pure function of its arguments.
type Decider struct {
	reconciliationBudget int
}

// NewDecider builds a Decider. The reconciliation budget must be positive.
// Zero or negative values are rejected, because a zero budget would silently
// turn every unknown effect into an immediate HOLD.
func NewDecider(cfg Config) (Decider, error) {
	if cfg.ReconciliationBudget <= 0 {
		return Decider{}, fmt.Errorf("%w: reconciliation budget must be positive, got %d",
			ErrInvalidConfig, cfg.ReconciliationBudget)
	}
	return Decider{reconciliationBudget: cfg.ReconciliationBudget}, nil
}

// Decide returns the recovery decision for the current generation of a Task.
// A Decider that was not built by NewDecider holds every input, because it
// has no valid reconciliation budget.
func (d Decider) Decide(in Input) Decision {
	if d.reconciliationBudget <= 0 || !validInput(in) {
		return newDecision(ActionHold, ReasonInputInvalid, EffectStateInvalid, in.UnknownEffectsInBatch,
			ngInputInvalid)
	}
	switch in.Outcome {
	case OutcomeReceiptApplied:
		return d.confirmedApplied(in)
	case OutcomeReceiptNotApplied:
		return d.confirmedNotApplied(in)
	}
	if in.Kind == EffectKindPureCompute {
		return d.pureCompute(in)
	}
	if !in.possiblyDispatched() {
		return d.notDispatched(in)
	}
	return d.dispatchedUnknown(in)
}

// confirmedApplied handles an authoritative receipt that proves the effect was
// applied. The receipt is final, so no retry or reconciliation follows.
func (d Decider) confirmedApplied(in Input) Decision {
	if in.CancellationRequested {
		return newDecision(ActionNoop, ReasonCancelAfterConfirmedEffect, EffectStateConfirmedApplied,
			in.UnknownEffectsInBatch, ngCancelDoesNotRollBack, ngCompletionPolicy)
	}
	return newDecision(ActionComplete, ReasonEffectConfirmedApplied, EffectStateConfirmedApplied,
		in.UnknownEffectsInBatch, ngCompletionPolicy)
}

// confirmedNotApplied handles an authoritative receipt that proves the effect
// was not applied. Retry is then allowed under the retry budget.
func (d Decider) confirmedNotApplied(in Input) Decision {
	if in.CancellationRequested {
		return newDecision(ActionNoop, ReasonCancelledNoOpenEffect, EffectStateConfirmedNotApplied,
			in.UnknownEffectsInBatch, ngReceiptAuthorityOnly)
	}
	if in.attemptsExhausted() {
		return newDecision(ActionFailTerminal, ReasonRetryBudgetExhausted, EffectStateConfirmedNotApplied,
			in.UnknownEffectsInBatch, ngReceiptAuthorityOnly)
	}
	return newDecision(ActionRetry, ReasonNotAppliedConfirmed, EffectStateConfirmedNotApplied,
		in.UnknownEffectsInBatch, ngReceiptAuthorityOnly)
}

// pureCompute handles a Task with no external effect. Retry is safe until the
// attempt budget is spent, and cancellation ends it normally.
func (d Decider) pureCompute(in Input) Decision {
	if in.CancellationRequested {
		return newDecision(ActionNoop, ReasonCancelledNoOpenEffect, EffectStateNone,
			in.UnknownEffectsInBatch, ngPureResultUnverified)
	}
	if in.attemptsExhausted() {
		return newDecision(ActionFailTerminal, ReasonRetryBudgetExhausted, EffectStateNone,
			in.UnknownEffectsInBatch, ngPureResultUnverified)
	}
	return newDecision(ActionRetry, ReasonPureComputeRetry, EffectStateNone,
		in.UnknownEffectsInBatch, ngPureResultUnverified)
}

// notDispatched handles an external effect for which no dispatch was attempted
// in the current generation. A recorded intent is required before any retry.
//
// A non-idempotent effect may be retried only when no dispatch was ever attempted
// for it (DispatchedGeneration is zero). The caller records a dispatch attempt
// before the call is sent, so a missing record shows that no call was sent. Once
// any generation was dispatched, that dispatch's outcome is unresolved and a
// retry could repeat it, so the effect is held. This is the provisional rule in
// plan/adr/0007-provisional-side-effect-retry-policy.md.
func (d Decider) notDispatched(in Input) Decision {
	if !in.intentRecorded() {
		return newDecision(ActionHold, ReasonIntentMissing, EffectStateNotDispatched,
			in.UnknownEffectsInBatch, ngIntentDurability)
	}
	if in.CancellationRequested {
		return newDecision(ActionNoop, ReasonCancelledNoOpenEffect, EffectStateNotDispatched,
			in.UnknownEffectsInBatch, ngNoEffectEvidence)
	}
	if in.Kind == EffectKindNonIdempotent && in.DispatchedGeneration != 0 {
		return newDecision(ActionHold, ReasonNonIdempotentRetryNotAuthed, EffectStateNotDispatched,
			in.UnknownEffectsInBatch, ngExactlyOnceUnknown, ngNoEffectEvidence)
	}
	if in.attemptsExhausted() {
		return newDecision(ActionHold, ReasonRetryBudgetExhausted, EffectStateNotDispatched,
			in.UnknownEffectsInBatch, ngIdempotencyHonored)
	}
	if in.Kind == EffectKindNonIdempotent {
		return newDecision(ActionRetry, ReasonNotDispatchedRetry, EffectStateNotDispatched,
			in.UnknownEffectsInBatch, ngExactlyOnceUnknown, ngNoEffectEvidence)
	}
	// Idempotent: a retry is allowed under the same operation identity.
	return newDecision(ActionRetry, ReasonIdempotentRetry, EffectStateNotDispatched,
		in.UnknownEffectsInBatch, ngIdempotencyHonored)
}

// dispatchedUnknown handles an external effect that may have reached the
// external system and has no authoritative confirmation. Worker loss, Worker
// claims, and the absence of a report all land here. None of them confirm the
// outcome.
func (d Decider) dispatchedUnknown(in Input) Decision {
	reconciliationSpent := in.ReconciliationsUsed >= d.reconciliationBudget
	if in.Kind == EffectKindNonIdempotent {
		if reconciliationSpent {
			return newDecision(ActionHold, ReasonReconciliationBudgetOut, EffectStateDispatchedUnknown,
				in.UnknownEffectsInBatch, ngExactlyOnceUnknown, ngWorkerReportIsClaim, ngReconcileBudgetBounds)
		}
		reason := ReasonEffectOutcomeUnknown
		if in.CancellationRequested {
			reason = ReasonEffectOpenCancelRequested
		}
		return newDecision(ActionHold, reason, EffectStateDispatchedUnknown,
			in.UnknownEffectsInBatch, ngExactlyOnceUnknown, ngWorkerReportIsClaim, ngCancelDoesNotRollBack)
	}
	// Idempotent: a retry is allowed under the same operation identity, unless
	// cancellation or the budget stops it.
	if in.CancellationRequested {
		if reconciliationSpent {
			return newDecision(ActionHold, ReasonReconciliationBudgetOut, EffectStateDispatchedUnknown,
				in.UnknownEffectsInBatch, ngWorkerReportIsClaim, ngReconcileBudgetBounds)
		}
		return newDecision(ActionContinueReconcile, ReasonEffectOpenCancelRequested, EffectStateDispatchedUnknown,
			in.UnknownEffectsInBatch, ngWorkerReportIsClaim, ngCancelDoesNotRollBack)
	}
	if in.attemptsExhausted() {
		return newDecision(ActionHold, ReasonRetryBudgetExhausted, EffectStateDispatchedUnknown,
			in.UnknownEffectsInBatch, ngWorkerReportIsClaim, ngIdempotencyHonored)
	}
	return newDecision(ActionRetry, ReasonIdempotentRetry, EffectStateDispatchedUnknown,
		in.UnknownEffectsInBatch, ngWorkerReportIsClaim, ngIdempotencyHonored)
}
