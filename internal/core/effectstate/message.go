package effectstate

// MessageKind identifies a late-arriving message that refers to one dispatch
// generation.
type MessageKind int

const (
	// MessageKindUnknown is the zero value. It is always rejected.
	MessageKindUnknown MessageKind = iota
	// MessageKindDispatchAck acknowledges that a Worker accepted a dispatch.
	MessageKindDispatchAck
	// MessageKindResult carries a Worker's result for a dispatch.
	MessageKindResult
)

// Message is a late-arriving message tagged with the generation it refers to.
type Message struct {
	Kind MessageKind
	// Generation is the dispatch generation the message refers to. Zero is
	// invalid.
	Generation uint64
}

// MessageClass is the classification of a message against the current
// generation.
type MessageClass int

const (
	// MessageInvalid means the message cannot be interpreted. It covers an
	// unknown kind, a zero generation, and a generation that was never issued.
	MessageInvalid MessageClass = iota
	// MessageCurrent means the message refers to the current generation.
	MessageCurrent
	// MessageStale means the message refers to an older generation. It must
	// not change state decisions.
	MessageStale
)

// ClassifyMessage compares a message with the current generation. A message
// from an older generation is stale. A message from a generation later than the
// current one is invalid, because that generation was never issued, so the
// input fails closed.
func ClassifyMessage(currentGeneration uint64, msg Message) MessageClass {
	if currentGeneration == 0 || msg.Generation == 0 {
		return MessageInvalid
	}
	if msg.Kind != MessageKindDispatchAck && msg.Kind != MessageKindResult {
		return MessageInvalid
	}
	switch {
	case msg.Generation == currentGeneration:
		return MessageCurrent
	case msg.Generation < currentGeneration:
		return MessageStale
	default:
		return MessageInvalid
	}
}

// DecideMessage applies the message classification to a decision. A stale
// message yields NOOP and leaves the state as it is. An invalid message yields
// HOLD. A current message yields the same decision as Decide(in), because the
// caller has already folded the message into in.
func (d Decider) DecideMessage(in Input, msg Message) Decision {
	switch ClassifyMessage(in.CurrentGeneration, msg) {
	case MessageStale:
		return newDecision(ActionNoop, ReasonStaleMessageIgnored, effectStateOf(in),
			in.UnknownEffectsInBatch, ngStaleMessage)
	case MessageCurrent:
		return d.Decide(in)
	default:
		return newDecision(ActionHold, ReasonInputInvalid, EffectStateInvalid,
			in.UnknownEffectsInBatch, ngInputInvalid)
	}
}

// effectStateOf reports the effect state of the current generation without
// making a decision. It is used to label a NOOP for a stale message.
func effectStateOf(in Input) EffectState {
	if !validInput(in) {
		return EffectStateInvalid
	}
	if in.Kind == EffectKindPureCompute {
		return EffectStateNone
	}
	switch in.Outcome {
	case OutcomeReceiptApplied:
		return EffectStateConfirmedApplied
	case OutcomeReceiptNotApplied:
		return EffectStateConfirmedNotApplied
	}
	if in.possiblyDispatched() {
		return EffectStateDispatchedUnknown
	}
	return EffectStateNotDispatched
}
