package effectstate

import (
	"reflect"
	"slices"
	"testing"
)

var (
	allKinds = []EffectKind{
		EffectKindUnknown, EffectKindPureCompute, EffectKindIdempotent, EffectKindNonIdempotent,
	}
	allOutcomes = []ObservedOutcome{
		OutcomeNone, OutcomeWorkerClaimedSuccess, OutcomeWorkerClaimedFailure,
		OutcomeReceiptApplied, OutcomeReceiptNotApplied, OutcomeWorkerLost, ObservedOutcome(99),
	}
)

// unknownOutcome reports whether o gives no authoritative confirmation.
func unknownOutcome(o ObservedOutcome) bool {
	switch o {
	case OutcomeNone, OutcomeWorkerClaimedSuccess, OutcomeWorkerClaimedFailure, OutcomeWorkerLost:
		return true
	default:
		return false
	}
}

// TestExhaustiveInvariants enumerates a small, complete state space and checks
// the safety properties on every combination.
func TestExhaustiveInvariants(t *testing.T) {
	d := mustDecider(t, 2)
	combinations := 0
	for _, kind := range allKinds {
		for _, outcome := range allOutcomes {
			for current := uint64(0); current <= 2; current++ {
				for dispatched := uint64(0); dispatched <= 2; dispatched++ {
					for intent := uint64(0); intent <= 2; intent++ {
						for used := 0; used <= 3; used++ {
							for limit := 0; limit <= 3; limit++ {
								for recon := 0; recon <= 3; recon++ {
									for _, cancel := range []bool{false, true} {
										in := Input{
											Kind:                  kind,
											Outcome:               outcome,
											CurrentGeneration:     current,
											DispatchedGeneration:  dispatched,
											Intent:                IntentRecord{Generation: intent},
											AttemptsUsed:          used,
											AttemptLimit:          limit,
											ReconciliationsUsed:   recon,
											CancellationRequested: cancel,
										}
										combinations++
										checkInvariants(t, d, in)
									}
								}
							}
						}
					}
				}
			}
		}
	}
	t.Logf("enumerated %d input combinations", combinations)
}

func checkInvariants(t *testing.T, d Decider, in Input) {
	t.Helper()
	got := d.Decide(in)
	if again := d.Decide(in); !reflect.DeepEqual(got, again) {
		t.Fatalf("non-deterministic decision for %+v: %+v vs %+v", in, got, again)
	}
	if len(got.NotGuaranteed) == 0 {
		t.Fatalf("empty NotGuaranteed for %+v: %+v", in, got)
	}
	if got.Action == ActionRetry {
		retryable := in.Kind == EffectKindPureCompute || in.Kind == EffectKindIdempotent || got.Reason == ReasonNotDispatchedRetry ||
			got.EffectState == EffectStateConfirmedNotApplied
		if !retryable {
			t.Fatalf("RETRY without a retry condition for %+v: %+v", in, got)
		}
	}
	// A non-idempotent retry is allowed only when no dispatch was ever attempted
	// for the effect; an earlier dispatch with an unresolved outcome holds it.
	if got.Reason == ReasonNotDispatchedRetry && in.DispatchedGeneration != 0 {
		t.Fatalf("non-idempotent retry after an earlier dispatch for %+v: %+v", in, got)
	}
	if got.Action == ActionFailTerminal && got.EffectState == EffectStateDispatchedUnknown {
		t.Fatalf("FAIL_TERMINAL with unknown effect for %+v: %+v", in, got)
	}
	if got.Reason == ReasonInputInvalid && (got.Action != ActionHold || got.EffectState != EffectStateInvalid) {
		t.Fatalf("invalid input not held for %+v: %+v", in, got)
	}
	// Central invariant: a non-idempotent effect that is possibly dispatched
	// with an unknown outcome is never retried, failed, completed, or cancelled.
	possiblyDispatched := in.CurrentGeneration >= 1 && in.DispatchedGeneration == in.CurrentGeneration
	if in.Kind == EffectKindNonIdempotent && possiblyDispatched && unknownOutcome(in.Outcome) {
		if got.Action != ActionHold {
			t.Fatalf("non-idempotent unknown effect yielded %s for %+v: %+v", got.Action, in, got)
		}
		if got.Reason != ReasonInputInvalid && !slices.Contains(got.NotGuaranteed, ngExactlyOnceUnknown) {
			t.Fatalf("non-idempotent unknown effect lacks exactly-once caveat for %+v", in)
		}
	}
}

// TestBatchCountIsDataOnly checks that the caller's batch count never changes
// the action, reason, or effect state. It is copied into the output as data.
func TestBatchCountIsDataOnly(t *testing.T) {
	d := mustDecider(t, 2)
	for _, kind := range []EffectKind{EffectKindPureCompute, EffectKindIdempotent, EffectKindNonIdempotent} {
		for _, outcome := range []ObservedOutcome{OutcomeNone, OutcomeWorkerLost} {
			for _, dispatched := range []uint64{0, 1} {
				in := dispatchedInput(kind)
				in.Outcome = outcome
				in.DispatchedGeneration = dispatched
				ref := d.Decide(in)
				for batch := 0; batch < 100; batch++ {
					in.UnknownEffectsInBatch = batch
					got := d.Decide(in)
					if got.Action != ref.Action || got.Reason != ref.Reason || got.EffectState != ref.EffectState {
						t.Fatalf("batch %d changed decision for kind %s: %+v vs %+v", batch, kind, got, ref)
					}
					if got.UnknownEffectsInBatch != batch {
						t.Fatalf("batch echo = %d, want %d", got.UnknownEffectsInBatch, batch)
					}
				}
			}
		}
	}
}

// TestNotGuaranteedIsFreshPerCall checks that a caller cannot alias the
// package-level statements through a returned Decision.
func TestNotGuaranteedIsFreshPerCall(t *testing.T) {
	d := mustDecider(t, 2)
	in := dispatchedInput(EffectKindNonIdempotent)
	in.Outcome = OutcomeWorkerLost
	first := d.Decide(in)
	first.NotGuaranteed[0] = "mutated by caller"
	second := d.Decide(in)
	if second.NotGuaranteed[0] == "mutated by caller" {
		t.Fatalf("NotGuaranteed aliases shared state across calls")
	}
}
