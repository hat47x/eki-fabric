package effectstate

import (
	"reflect"
	"testing"
)

func TestClassifyMessage(t *testing.T) {
	cases := []struct {
		name    string
		current uint64
		msg     Message
		want    MessageClass
	}{
		{"ack for current generation", 2, Message{Kind: MessageKindDispatchAck, Generation: 2}, MessageCurrent},
		{"result for current generation", 2, Message{Kind: MessageKindResult, Generation: 2}, MessageCurrent},
		{"ack for older generation is stale", 2, Message{Kind: MessageKindDispatchAck, Generation: 1}, MessageStale},
		{"result for older generation is stale", 3, Message{Kind: MessageKindResult, Generation: 1}, MessageStale},
		{"generation never issued is invalid", 2, Message{Kind: MessageKindResult, Generation: 3}, MessageInvalid},
		{"zero generation is invalid", 2, Message{Kind: MessageKindResult, Generation: 0}, MessageInvalid},
		{"unknown kind is invalid", 2, Message{Kind: MessageKindUnknown, Generation: 2}, MessageInvalid},
		{"zero current generation is invalid", 0, Message{Kind: MessageKindResult, Generation: 1}, MessageInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyMessage(tc.current, tc.msg); got != tc.want {
				t.Fatalf("ClassifyMessage(%d, %+v) = %d, want %d", tc.current, tc.msg, got, tc.want)
			}
		})
	}
}

func TestStaleMessageDoesNotChangeDecision(t *testing.T) {
	d := mustDecider(t, 2)
	in := dispatchedInput(EffectKindNonIdempotent)
	in.Outcome = OutcomeWorkerLost
	in.CurrentGeneration = 2
	in.DispatchedGeneration = 2
	in.Intent.Generation = 2

	stale := d.DecideMessage(in, Message{Kind: MessageKindResult, Generation: 1})
	if stale.Action != ActionNoop || stale.Reason != ReasonStaleMessageIgnored {
		t.Fatalf("stale DecideMessage = {%s %s}, want NOOP STALE_MESSAGE_IGNORED", stale.Action, stale.Reason)
	}
	if stale.EffectState != EffectStateDispatchedUnknown {
		t.Fatalf("stale DecideMessage state = %s, want %s", stale.EffectState, EffectStateDispatchedUnknown)
	}

	// The state itself is unchanged by the stale message.
	held := d.Decide(in)
	if held.Action != ActionHold {
		t.Fatalf("Decide after stale message = %s, want HOLD", held.Action)
	}
}

func TestDecideMessageCurrentAndInvalid(t *testing.T) {
	d := mustDecider(t, 2)
	in := dispatchedInput(EffectKindIdempotent)
	in.Outcome = OutcomeWorkerLost

	current := d.DecideMessage(in, Message{Kind: MessageKindResult, Generation: in.CurrentGeneration})
	if !reflect.DeepEqual(current, d.Decide(in)) {
		t.Fatalf("current DecideMessage differs from Decide: %+v vs %+v", current, d.Decide(in))
	}

	invalid := d.DecideMessage(in, Message{Kind: MessageKindResult, Generation: in.CurrentGeneration + 5})
	if invalid.Action != ActionHold || invalid.Reason != ReasonInputInvalid {
		t.Fatalf("invalid-message DecideMessage = {%s %s}, want HOLD INPUT_INVALID", invalid.Action, invalid.Reason)
	}
}
