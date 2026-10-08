package effectstate

import (
	"errors"
	"testing"
)

func mustDecider(t *testing.T, budget int) Decider {
	t.Helper()
	d, err := NewDecider(Config{ReconciliationBudget: budget})
	if err != nil {
		t.Fatalf("NewDecider(%d): %v", budget, err)
	}
	return d
}

// baseInput is generation 1 with its intent recorded and no dispatch yet.
func baseInput(kind EffectKind) Input {
	return Input{
		Kind:              kind,
		Intent:            IntentRecord{Generation: 1},
		CurrentGeneration: 1,
		AttemptsUsed:      1,
		AttemptLimit:      3,
	}
}

// dispatchedInput is baseInput after generation 1 was dispatched.
func dispatchedInput(kind EffectKind) Input {
	in := baseInput(kind)
	in.DispatchedGeneration = 1
	return in
}

type decisionCase struct {
	name       string
	in         Input
	wantAction Action
	wantReason ReasonCode
	wantState  EffectState
}

func runDecisionCases(t *testing.T, d Decider, cases []decisionCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.Decide(tc.in)
			if got.Action != tc.wantAction || got.Reason != tc.wantReason || got.EffectState != tc.wantState {
				t.Fatalf("Decide() = {action %s, reason %s, state %s}, want {action %s, reason %s, state %s}",
					got.Action, got.Reason, got.EffectState, tc.wantAction, tc.wantReason, tc.wantState)
			}
		})
	}
}

func TestEffectKindAndIntent(t *testing.T) {
	d := mustDecider(t, 2)
	runDecisionCases(t, d, []decisionCase{
		{
			name:       "unknown kind is rejected",
			in:         baseInput(EffectKindUnknown),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "out of range kind is rejected",
			in:         baseInput(EffectKind(9)),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "pure compute not dispatched without intent retries",
			in:         Input{Kind: EffectKindPureCompute, CurrentGeneration: 1, AttemptsUsed: 1, AttemptLimit: 3},
			wantAction: ActionRetry, wantReason: ReasonPureComputeRetry, wantState: EffectStateNone,
		},
		{
			name:       "idempotent not dispatched without intent holds",
			in:         Input{Kind: EffectKindIdempotent, CurrentGeneration: 1, AttemptsUsed: 1, AttemptLimit: 3},
			wantAction: ActionHold, wantReason: ReasonIntentMissing, wantState: EffectStateNotDispatched,
		},
		{
			name:       "non-idempotent not dispatched without intent holds",
			in:         Input{Kind: EffectKindNonIdempotent, CurrentGeneration: 1, AttemptsUsed: 1, AttemptLimit: 3},
			wantAction: ActionHold, wantReason: ReasonIntentMissing, wantState: EffectStateNotDispatched,
		},
		{
			name:       "non-idempotent with intent but no dispatch retries, since no call was sent",
			in:         baseInput(EffectKindNonIdempotent),
			wantAction: ActionRetry, wantReason: ReasonNotDispatchedRetry, wantState: EffectStateNotDispatched,
		},
		{
			name: "non-idempotent after an earlier generation was dispatched is held",
			in: Input{
				Kind: EffectKindNonIdempotent, Intent: IntentRecord{Generation: 2},
				DispatchedGeneration: 1, CurrentGeneration: 2, AttemptsUsed: 1, AttemptLimit: 3,
			},
			wantAction: ActionHold, wantReason: ReasonNonIdempotentRetryNotAuthed, wantState: EffectStateNotDispatched,
		},
		{
			name:       "idempotent with intent but no dispatch retries",
			in:         baseInput(EffectKindIdempotent),
			wantAction: ActionRetry, wantReason: ReasonIdempotentRetry, wantState: EffectStateNotDispatched,
		},
		{
			name: "dispatch without recorded intent is invalid",
			in: func() Input {
				in := dispatchedInput(EffectKindNonIdempotent)
				in.Intent = IntentRecord{}
				return in
			}(),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "dispatched non-idempotent with no report is possibly dispatched and held",
			in:         dispatchedInput(EffectKindNonIdempotent),
			wantAction: ActionHold, wantReason: ReasonEffectOutcomeUnknown, wantState: EffectStateDispatchedUnknown,
		},
	})
}

func TestOutcomeConfirmation(t *testing.T) {
	d := mustDecider(t, 2)
	applied := dispatchedInput(EffectKindNonIdempotent)
	applied.Outcome = OutcomeReceiptApplied
	notApplied := dispatchedInput(EffectKindNonIdempotent)
	notApplied.Outcome = OutcomeReceiptNotApplied
	claimedOK := dispatchedInput(EffectKindNonIdempotent)
	claimedOK.Outcome = OutcomeWorkerClaimedSuccess
	claimedFail := dispatchedInput(EffectKindNonIdempotent)
	claimedFail.Outcome = OutcomeWorkerClaimedFailure
	receiptNeverDispatched := baseInput(EffectKindNonIdempotent)
	receiptNeverDispatched.Outcome = OutcomeReceiptNotApplied
	claimNeverDispatched := baseInput(EffectKindNonIdempotent)
	claimNeverDispatched.Outcome = OutcomeWorkerClaimedSuccess
	pureReceipt := dispatchedInput(EffectKindPureCompute)
	pureReceipt.Outcome = OutcomeReceiptApplied

	runDecisionCases(t, d, []decisionCase{
		{
			name:       "authoritative applied receipt completes",
			in:         applied,
			wantAction: ActionComplete, wantReason: ReasonEffectConfirmedApplied, wantState: EffectStateConfirmedApplied,
		},
		{
			name:       "authoritative not-applied receipt allows retry",
			in:         notApplied,
			wantAction: ActionRetry, wantReason: ReasonNotAppliedConfirmed, wantState: EffectStateConfirmedNotApplied,
		},
		{
			name:       "worker claimed success is not confirmation",
			in:         claimedOK,
			wantAction: ActionHold, wantReason: ReasonEffectOutcomeUnknown, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "worker claimed failure does not allow retry",
			in:         claimedFail,
			wantAction: ActionHold, wantReason: ReasonEffectOutcomeUnknown, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "not-applied receipt for a never dispatched generation is invalid",
			in:         receiptNeverDispatched,
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "worker claim for a never dispatched generation is invalid",
			in:         claimNeverDispatched,
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "pure compute cannot carry an external receipt",
			in:         pureReceipt,
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
	})
}

func TestRetryConditions(t *testing.T) {
	d := mustDecider(t, 2)
	pureLost := dispatchedInput(EffectKindPureCompute)
	pureLost.Outcome = OutcomeWorkerLost
	idemLost := dispatchedInput(EffectKindIdempotent)
	idemLost.Outcome = OutcomeWorkerLost
	idemClaimFail := dispatchedInput(EffectKindIdempotent)
	idemClaimFail.Outcome = OutcomeWorkerClaimedFailure
	nonIdemLost := dispatchedInput(EffectKindNonIdempotent)
	nonIdemLost.Outcome = OutcomeWorkerLost
	idemLostBeforeDispatch := baseInput(EffectKindIdempotent)
	idemLostBeforeDispatch.Outcome = OutcomeWorkerLost
	nonIdemLostBeforeDispatch := baseInput(EffectKindNonIdempotent)
	nonIdemLostBeforeDispatch.Outcome = OutcomeWorkerLost

	runDecisionCases(t, d, []decisionCase{
		{
			name:       "pure compute lost after dispatch retries",
			in:         pureLost,
			wantAction: ActionRetry, wantReason: ReasonPureComputeRetry, wantState: EffectStateNone,
		},
		{
			name:       "idempotent effect lost after dispatch retries with same identity",
			in:         idemLost,
			wantAction: ActionRetry, wantReason: ReasonIdempotentRetry, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "idempotent effect with claimed failure retries",
			in:         idemClaimFail,
			wantAction: ActionRetry, wantReason: ReasonIdempotentRetry, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "non-idempotent effect lost after dispatch is held, never retried",
			in:         nonIdemLost,
			wantAction: ActionHold, wantReason: ReasonEffectOutcomeUnknown, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "idempotent effect lost before dispatch retries",
			in:         idemLostBeforeDispatch,
			wantAction: ActionRetry, wantReason: ReasonIdempotentRetry, wantState: EffectStateNotDispatched,
		},
		{
			name:       "non-idempotent effect lost before dispatch retries, since no call was sent",
			in:         nonIdemLostBeforeDispatch,
			wantAction: ActionRetry, wantReason: ReasonNotDispatchedRetry, wantState: EffectStateNotDispatched,
		},
	})
}

func TestCancellation(t *testing.T) {
	d := mustDecider(t, 2)
	nonIdemOpen := dispatchedInput(EffectKindNonIdempotent)
	nonIdemOpen.Outcome = OutcomeWorkerLost
	nonIdemOpen.CancellationRequested = true
	idemOpen := dispatchedInput(EffectKindIdempotent)
	idemOpen.Outcome = OutcomeWorkerLost
	idemOpen.CancellationRequested = true
	pureCancel := dispatchedInput(EffectKindPureCompute)
	pureCancel.Outcome = OutcomeWorkerLost
	pureCancel.CancellationRequested = true
	nonIdemNotDispatched := baseInput(EffectKindNonIdempotent)
	nonIdemNotDispatched.CancellationRequested = true
	notApplied := dispatchedInput(EffectKindNonIdempotent)
	notApplied.Outcome = OutcomeReceiptNotApplied
	notApplied.CancellationRequested = true
	applied := dispatchedInput(EffectKindNonIdempotent)
	applied.Outcome = OutcomeReceiptApplied
	applied.CancellationRequested = true
	cancelNoIntent := Input{Kind: EffectKindNonIdempotent, CurrentGeneration: 1, AttemptsUsed: 1, AttemptLimit: 3, CancellationRequested: true}

	runDecisionCases(t, d, []decisionCase{
		{
			name:       "cancel keeps non-idempotent unknown effect open",
			in:         nonIdemOpen,
			wantAction: ActionHold, wantReason: ReasonEffectOpenCancelRequested, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "cancel reconciles idempotent unknown effect",
			in:         idemOpen,
			wantAction: ActionContinueReconcile, wantReason: ReasonEffectOpenCancelRequested, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "cancel of pure compute terminates normally",
			in:         pureCancel,
			wantAction: ActionNoop, wantReason: ReasonCancelledNoOpenEffect, wantState: EffectStateNone,
		},
		{
			name:       "cancel before any dispatch has no open effect",
			in:         nonIdemNotDispatched,
			wantAction: ActionNoop, wantReason: ReasonCancelledNoOpenEffect, wantState: EffectStateNotDispatched,
		},
		{
			name:       "cancel after confirmed not-applied has no open effect",
			in:         notApplied,
			wantAction: ActionNoop, wantReason: ReasonCancelledNoOpenEffect, wantState: EffectStateConfirmedNotApplied,
		},
		{
			name:       "cancel after confirmed applied does not roll back",
			in:         applied,
			wantAction: ActionNoop, wantReason: ReasonCancelAfterConfirmedEffect, wantState: EffectStateConfirmedApplied,
		},
		{
			name:       "cancel without recorded intent holds",
			in:         cancelNoIntent,
			wantAction: ActionHold, wantReason: ReasonIntentMissing, wantState: EffectStateNotDispatched,
		},
	})
}

func TestReconciliationBudget(t *testing.T) {
	d := mustDecider(t, 2)
	within := dispatchedInput(EffectKindNonIdempotent)
	within.Outcome = OutcomeWorkerLost
	within.ReconciliationsUsed = 1
	spent := within
	spent.ReconciliationsUsed = 2
	idemCancelSpent := dispatchedInput(EffectKindIdempotent)
	idemCancelSpent.Outcome = OutcomeWorkerLost
	idemCancelSpent.CancellationRequested = true
	idemCancelSpent.ReconciliationsUsed = 2
	nonIdemCancelSpent := spent
	nonIdemCancelSpent.CancellationRequested = true

	runDecisionCases(t, d, []decisionCase{
		{
			name:       "reconciliation within budget still holds unknown effect",
			in:         within,
			wantAction: ActionHold, wantReason: ReasonEffectOutcomeUnknown, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "reconciliation budget exhausted holds with its own reason",
			in:         spent,
			wantAction: ActionHold, wantReason: ReasonReconciliationBudgetOut, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "reconciliation budget exhausted under cancel holds for idempotent effect",
			in:         idemCancelSpent,
			wantAction: ActionHold, wantReason: ReasonReconciliationBudgetOut, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "reconciliation budget exhausted under cancel holds for non-idempotent effect",
			in:         nonIdemCancelSpent,
			wantAction: ActionHold, wantReason: ReasonReconciliationBudgetOut, wantState: EffectStateDispatchedUnknown,
		},
	})
}

func TestNewDeciderRejectsNonPositiveBudget(t *testing.T) {
	for _, budget := range []int{0, -1} {
		_, err := NewDecider(Config{ReconciliationBudget: budget})
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("NewDecider(budget %d) error = %v, want ErrInvalidConfig", budget, err)
		}
	}
	if _, err := NewDecider(Config{ReconciliationBudget: 1}); err != nil {
		t.Fatalf("NewDecider(budget 1) error = %v, want nil", err)
	}
}

func TestZeroValueDeciderFailsClosed(t *testing.T) {
	var d Decider
	got := d.Decide(baseInput(EffectKindPureCompute))
	if got.Action != ActionHold || got.Reason != ReasonInputInvalid {
		t.Fatalf("zero Decider Decide() = {%s %s}, want HOLD INPUT_INVALID", got.Action, got.Reason)
	}
}

func TestRetryBudget(t *testing.T) {
	d := mustDecider(t, 2)
	pureExhausted := dispatchedInput(EffectKindPureCompute)
	pureExhausted.Outcome = OutcomeWorkerLost
	pureExhausted.AttemptsUsed = 3
	idemExhausted := dispatchedInput(EffectKindIdempotent)
	idemExhausted.Outcome = OutcomeWorkerLost
	idemExhausted.AttemptsUsed = 3
	idemUndispatchedExhausted := baseInput(EffectKindIdempotent)
	idemUndispatchedExhausted.AttemptsUsed = 3
	nonIdemExhausted := dispatchedInput(EffectKindNonIdempotent)
	nonIdemExhausted.Outcome = OutcomeWorkerLost
	nonIdemExhausted.AttemptsUsed = 5
	notAppliedExhausted := dispatchedInput(EffectKindNonIdempotent)
	notAppliedExhausted.Outcome = OutcomeReceiptNotApplied
	notAppliedExhausted.AttemptsUsed = 3
	zeroLimit := baseInput(EffectKindPureCompute)
	zeroLimit.AttemptLimit = 0

	runDecisionCases(t, d, []decisionCase{
		{
			name:       "pure compute exhausted ends as terminal failure",
			in:         pureExhausted,
			wantAction: ActionFailTerminal, wantReason: ReasonRetryBudgetExhausted, wantState: EffectStateNone,
		},
		{
			name:       "idempotent unknown exhausted holds",
			in:         idemExhausted,
			wantAction: ActionHold, wantReason: ReasonRetryBudgetExhausted, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "idempotent not dispatched exhausted holds",
			in:         idemUndispatchedExhausted,
			wantAction: ActionHold, wantReason: ReasonRetryBudgetExhausted, wantState: EffectStateNotDispatched,
		},
		{
			name:       "non-idempotent unknown exhausted holds, never fails",
			in:         nonIdemExhausted,
			wantAction: ActionHold, wantReason: ReasonEffectOutcomeUnknown, wantState: EffectStateDispatchedUnknown,
		},
		{
			name:       "confirmed not-applied exhausted ends as terminal failure",
			in:         notAppliedExhausted,
			wantAction: ActionFailTerminal, wantReason: ReasonRetryBudgetExhausted, wantState: EffectStateConfirmedNotApplied,
		},
		{
			name:       "zero attempt limit is invalid",
			in:         zeroLimit,
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
	})
}

// A non-idempotent effect that was never dispatched still stops when the attempt
// budget is spent, so no retry is unbounded.
func TestNonIdempotentRetryIsBoundedByTheAttemptBudget(t *testing.T) {
	d := mustDecider(t, 2)
	spent := baseInput(EffectKindNonIdempotent)
	spent.AttemptsUsed = 3
	runDecisionCases(t, d, []decisionCase{{
		name:       "non-idempotent not dispatched and out of attempts holds",
		in:         spent,
		wantAction: ActionHold, wantReason: ReasonRetryBudgetExhausted, wantState: EffectStateNotDispatched,
	}})
}

func TestInconsistentInputFailsClosed(t *testing.T) {
	d := mustDecider(t, 2)
	mutate := func(kind EffectKind, f func(*Input)) Input {
		in := dispatchedInput(kind)
		in.Outcome = OutcomeWorkerLost
		f(&in)
		return in
	}
	cases := []decisionCase{
		{
			name:       "zero current generation",
			in:         mutate(EffectKindPureCompute, func(in *Input) { in.CurrentGeneration = 0; in.DispatchedGeneration = 0; in.Intent.Generation = 0 }),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "dispatched generation beyond current",
			in:         mutate(EffectKindIdempotent, func(in *Input) { in.DispatchedGeneration = 2 }),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "intent beyond current generation",
			in:         mutate(EffectKindIdempotent, func(in *Input) { in.Intent.Generation = 2 }),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "zero attempts used",
			in:         mutate(EffectKindIdempotent, func(in *Input) { in.AttemptsUsed = 0 }),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "negative reconciliations used",
			in:         mutate(EffectKindNonIdempotent, func(in *Input) { in.ReconciliationsUsed = -1 }),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "negative unknown effects in batch",
			in:         mutate(EffectKindNonIdempotent, func(in *Input) { in.UnknownEffectsInBatch = -1 }),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
		{
			name:       "outcome out of range",
			in:         mutate(EffectKindNonIdempotent, func(in *Input) { in.Outcome = ObservedOutcome(99) }),
			wantAction: ActionHold, wantReason: ReasonInputInvalid, wantState: EffectStateInvalid,
		},
	}
	runDecisionCases(t, d, cases)
}
