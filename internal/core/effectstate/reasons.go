package effectstate

// Action is the single recovery action the Control Plane may take for a Task.
type Action string

const (
	// ActionRetry allows a new Attempt. It is returned only under the three
	// retry conditions: pure compute, idempotent effect, or confirmed
	// not-applied.
	ActionRetry Action = "RETRY"
	// ActionHold stops automatic progress until reconciliation or a human
	// decision. It is the conservative result for every unresolved case.
	ActionHold Action = "HOLD"
	// ActionFailTerminal ends the Task as a terminal failure. It is used only
	// when no effect is open, for example when a retry budget is exhausted for
	// pure compute or the effect is confirmed not applied.
	ActionFailTerminal Action = "FAIL_TERMINAL"
	// ActionComplete reports that an external effect is confirmed applied.
	// Application-level completion policy is evaluated by the caller.
	ActionComplete Action = "COMPLETE"
	// ActionContinueReconcile keeps an open effect under reconciliation without
	// starting a new Attempt. It consumes one reconciliation unit.
	ActionContinueReconcile Action = "CONTINUE_RECONCILE"
	// ActionNoop changes nothing in the state. It is used for stale messages
	// and for cancellation paths that have no open effect.
	ActionNoop Action = "NOOP"
)

// ReasonCode is a stable, machine-readable explanation of a Decision.
type ReasonCode string

const (
	ReasonInputInvalid                ReasonCode = "INPUT_INVALID"
	ReasonEffectConfirmedApplied      ReasonCode = "EFFECT_CONFIRMED_APPLIED"
	ReasonNotAppliedConfirmed         ReasonCode = "NOT_APPLIED_CONFIRMED"
	ReasonPureComputeRetry            ReasonCode = "PURE_COMPUTE_RETRY"
	ReasonIdempotentRetry             ReasonCode = "IDEMPOTENT_RETRY_ALLOWED"
	ReasonNotDispatchedRetry          ReasonCode = "NOT_DISPATCHED_RETRY_ALLOWED"
	ReasonRetryBudgetExhausted        ReasonCode = "RETRY_BUDGET_EXHAUSTED"
	ReasonReconciliationBudgetOut     ReasonCode = "RECONCILIATION_BUDGET_EXHAUSTED"
	ReasonEffectOutcomeUnknown        ReasonCode = "EFFECT_OUTCOME_UNKNOWN"
	ReasonEffectOpenCancelRequested   ReasonCode = "EFFECT_OPEN_CANCELLATION_REQUESTED"
	ReasonCancelledNoOpenEffect       ReasonCode = "CANCELLED_NO_OPEN_EFFECT"
	ReasonCancelAfterConfirmedEffect  ReasonCode = "CANCELLATION_AFTER_CONFIRMED_EFFECT"
	ReasonNonIdempotentRetryNotAuthed ReasonCode = "NON_IDEMPOTENT_RETRY_NOT_AUTHORIZED"
	ReasonIntentMissing               ReasonCode = "EFFECT_INTENT_MISSING"
	ReasonStaleMessageIgnored         ReasonCode = "STALE_MESSAGE_IGNORED"
)

// EffectState is the external-effect state that a Decision is based on.
type EffectState string

const (
	// EffectStateNone applies to pure compute, which has no external effect.
	EffectStateNone EffectState = "E-NONE"
	// EffectStateNotDispatched means no dispatch was attempted for the current
	// generation.
	EffectStateNotDispatched EffectState = "E-NOT-DISPATCHED"
	// EffectStateDispatchedUnknown means dispatch may have happened and no
	// authoritative receipt has confirmed the outcome.
	EffectStateDispatchedUnknown EffectState = "E-DISPATCHED-UNCONFIRMED"
	// EffectStateConfirmedApplied means an authoritative receipt proves the
	// effect was applied.
	EffectStateConfirmedApplied EffectState = "E-CONFIRMED-APPLIED"
	// EffectStateConfirmedNotApplied means an authoritative receipt proves the
	// effect was not applied.
	EffectStateConfirmedNotApplied EffectState = "E-CONFIRMED-NOT-APPLIED"
	// EffectStateInvalid means the input was rejected and no effect state
	// could be established.
	EffectStateInvalid EffectState = "E-INVALID"
)

// Decision is the output of a decision. It is a plain value with no hidden
// state, so the same Input always produces an equal Decision.
type Decision struct {
	// Action is the single recovery action.
	Action Action
	// Reason is the stable reason code for Action.
	Reason ReasonCode
	// EffectState is the external-effect state the Decision is based on.
	EffectState EffectState
	// NotGuaranteed lists properties this Decision does not establish. It is a
	// fresh slice on every call.
	NotGuaranteed []string
	// UnknownEffectsInBatch echoes Input.UnknownEffectsInBatch. It is data for
	// the caller and does not influence Action or Reason.
	UnknownEffectsInBatch int
}
