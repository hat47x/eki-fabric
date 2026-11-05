package dispatch

import "testing"

// The claim is the Worker's, not a fact: it may steer public inputs and nothing
// else (ADR-0029 §5).
func TestTheLocalityHintIsAllowedOnlyForPublicData(t *testing.T) {
	cases := []struct {
		class string
		want  bool
	}{
		{"public", true},
		{"internal", false},
		{"restricted", false},
		{"", false},      // declared nothing: not evidence that it is safe to move
		{"other", false}, // outside the vocabulary the deployment declares
	}
	for _, c := range cases {
		if got := (LocalityHint{DataClass: c.class}).Allowed(); got != c.want {
			t.Errorf("class %q: Allowed() = %v, want %v", c.class, got, c.want)
		}
	}
}

// A holder comes first, but the others keep their order: this is a tie-break, not a
// filter, so a Worker that fits is never dropped for not claiming the artifact.
func TestThePreferenceReordersWithoutDropping(t *testing.T) {
	order := LocalityHint{DataClass: "public"}.PreferOrders([]bool{false, true, false, true})
	if len(order) != 4 {
		t.Fatalf("order = %v, want all four candidates", order)
	}
	want := []int{1, 3, 0, 2}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// No candidate claims it: the order is unchanged.
func TestThePreferenceKeepsTheOrderWhenNobodyHoldsIt(t *testing.T) {
	order := LocalityHint{DataClass: "public"}.PreferOrders([]bool{false, false, false})
	want := []int{0, 1, 2}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
