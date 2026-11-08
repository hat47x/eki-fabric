package transportfixture

import (
	"encoding/json"
	"os"
	"testing"
)

// The reference evaluator is test-only. It is not a delivery adapter, a
// verification authority, or an implementation of EKI's production state model.
type referenceEvent struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Generation string `json:"generation"`
	Sequence   *int   `json:"sequence"`
	Epoch      string `json:"epoch"`
}

type referenceFixture struct {
	Authority struct {
		LeaseEpoch string `json:"lease_epoch"`
	} `json:"authority_boundary"`
	Events   []referenceEvent  `json:"events"`
	Expected map[string]string `json:"independent_expected"`
}

func readReferenceFixture(t *testing.T) referenceFixture {
	t.Helper()
	raw, err := os.ReadFile("fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var f referenceFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.Authority.LeaseEpoch == "" || len(f.Events) == 0 {
		t.Fatal("missing lease or events")
	}
	return f
}

// referenceDispositions derives classifications from event order, not from
// independent_expected. Unknown authority cannot be turned into acceptance.
func referenceDispositions(leaseEpoch string, events []referenceEvent) map[string]string {
	lastSeq := map[string]int{}
	seenGen := map[string]bool{}
	revoked := false
	observations := make(map[string]string, len(events))
	for _, e := range events {
		switch e.Kind {
		case "observation":
			if e.Sequence == nil || e.Generation == "" || e.Epoch != leaseEpoch || revoked {
				observations[e.ID] = "observation-unusable"
			} else if seenGen[e.Generation] && *e.Sequence <= lastSeq[e.Generation] {
				observations[e.ID] = "stale-rejected"
			} else {
				lastSeq[e.Generation] = *e.Sequence
				seenGen[e.Generation] = true
				observations[e.ID] = "fresh-candidate-not-consumption-proof"
			}
		case "reconnect":
			if e.Generation == "" || e.Epoch != leaseEpoch || revoked {
				observations[e.ID] = "reconnect-not-authorized"
			} else {
				observations[e.ID] = "new-stream-generation-not-new-authority"
			}
		case "lease_revoked":
			if e.Epoch != leaseEpoch {
				observations[e.ID] = "wrong-lease"
			} else {
				revoked = true
				observations[e.ID] = "lease-revoked"
			}
		case "result_claim":
			if e.Epoch != leaseEpoch {
				observations[e.ID] = "claim-with-unrecognized-authority"
			} else if revoked {
				observations[e.ID] = "claim-not-accepted"
			} else {
				observations[e.ID] = "claim-unverified-not-accepted"
			}
		default:
			observations[e.ID] = "unsupported-event"
		}
	}
	return observations
}

func oracleMatches(f referenceFixture, events []referenceEvent) bool {
	observed := referenceDispositions(f.Authority.LeaseEpoch, events)
	if len(observed) != len(f.Expected) {
		return false
	}
	for id, want := range f.Expected {
		if observed[id] != want {
			return false
		}
	}
	return true
}

func TestReferenceSemanticsMatchFrozenOracle(t *testing.T) {
	f := readReferenceFixture(t)
	if !oracleMatches(f, f.Events) {
		got := referenceDispositions(f.Authority.LeaseEpoch, f.Events)
		t.Fatalf("test-only semantics disagree with frozen, preexisting oracle: got=%v want=%v", got, f.Expected)
	}
}

func TestFrozenOracleRejectsMutations(t *testing.T) {
	f := readReferenceFixture(t)
	if len(f.Events) != 6 {
		t.Fatal("fixture assumptions changed; review frozen oracle before updating reference tests")
	}
	cases := []struct {
		name   string
		mutate func([]referenceEvent)
	}{
		{"reorder-stale-as-fresh", func(es []referenceEvent) { fortyOne := 41; es[1].Sequence = &fortyOne }},
		{"reuse-old-generation", func(es []referenceEvent) { es[3].Generation = "gA" }},
		{"result-before-revocation", func(es []referenceEvent) { es[4], es[5] = es[5], es[4] }},
		{"unrecognized-result-epoch", func(es []referenceEvent) { es[5].Epoch = "e8" }},
		{"missing-generation", func(es []referenceEvent) { es[3].Generation = "" }},
		{"unknown-event-kind", func(es []referenceEvent) { es[0].Kind = "not-an-event" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := append([]referenceEvent(nil), f.Events...)
			tc.mutate(events)
			if oracleMatches(f, events) {
				t.Fatal("altered trace was accepted against fixed expected")
			}
		})
	}
}
