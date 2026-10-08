package effectstate

// validInput reports whether in is internally consistent. Every rule below
// fails closed: an input that breaks a rule produces HOLD with
// ReasonInputInvalid instead of an accept or a retry.
//
// The rules encode these facts:
//   - Generations start at 1, and no generation can be dispatched before it
//     is issued.
//   - A dispatch requires a recorded intent, so the intent is never older than
//     the last dispatched generation. Pure compute has no intent requirement.
//   - An external receipt, or a Worker claim, can describe only a generation
//     that was dispatched. A pure compute effect has no external system, so it
//     cannot carry a receipt.
//   - Counters are non-negative, and the attempt limit and the current attempt
//     are at least 1.
func validInput(in Input) bool {
	if !in.Kind.Valid() || !in.Outcome.Valid() {
		return false
	}
	if in.CurrentGeneration == 0 {
		return false
	}
	if in.DispatchedGeneration > in.CurrentGeneration {
		return false
	}
	if in.Intent.Generation > in.CurrentGeneration {
		return false
	}
	if in.AttemptLimit <= 0 || in.AttemptsUsed <= 0 {
		return false
	}
	if in.ReconciliationsUsed < 0 || in.UnknownEffectsInBatch < 0 {
		return false
	}
	if in.Kind != EffectKindPureCompute && in.DispatchedGeneration > 0 &&
		in.Intent.Generation < in.DispatchedGeneration {
		return false
	}
	if in.Kind == EffectKindPureCompute &&
		(in.Outcome == OutcomeReceiptApplied || in.Outcome == OutcomeReceiptNotApplied) {
		return false
	}
	if outcomeNeedsDispatch(in.Outcome) && !in.possiblyDispatched() {
		return false
	}
	return true
}

// outcomeNeedsDispatch reports whether an outcome can describe only a
// generation that was actually dispatched. Worker loss is excluded because a
// Worker can be lost before any dispatch was made.
func outcomeNeedsDispatch(o ObservedOutcome) bool {
	switch o {
	case OutcomeWorkerClaimedSuccess, OutcomeWorkerClaimedFailure,
		OutcomeReceiptApplied, OutcomeReceiptNotApplied:
		return true
	default:
		return false
	}
}

// possiblyDispatched reports whether a dispatch may have reached the external
// side for the current generation. The caller records the dispatch attempt
// before the call is sent, so a missing record proves no dispatch happened.
func (in Input) possiblyDispatched() bool {
	return in.DispatchedGeneration == in.CurrentGeneration
}

// intentRecorded reports whether the durable intent covers the current
// generation.
func (in Input) intentRecorded() bool {
	return in.Intent.Generation == in.CurrentGeneration
}

// attemptsExhausted reports whether no further Attempt may be created.
func (in Input) attemptsExhausted() bool {
	return in.AttemptsUsed >= in.AttemptLimit
}
