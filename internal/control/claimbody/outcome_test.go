package claimbody

import "testing"

func TestTheOutcomeMapsToTheContract(t *testing.T) {
	cases := []struct {
		outcome Outcome
		status  int
		code    string
	}{
		{OutcomeDeliver, 200, ""},
		{OutcomeNotFound, 404, "artifact_not_found"},
		{OutcomeExpired, 410, "artifact_expired"},
		{OutcomeWithdrawn, 410, "artifact_withdrawn"},
		{OutcomeUnreadable, 500, "artifact_unreadable"},
	}
	for _, c := range cases {
		if got := c.outcome.Status(); got != c.status {
			t.Fatalf("%v.Status() = %d, want %d", c.outcome, got, c.status)
		}
		if got := c.outcome.Code(); got != c.code {
			t.Fatalf("%v.Code() = %q, want %q", c.outcome, got, c.code)
		}
	}
}

func TestAnUnknownOutcomeIsTreatedAsNotFound(t *testing.T) {
	unknown := Outcome(99)
	if got := unknown.Status(); got != 404 {
		t.Fatalf("Status() = %d, want 404", got)
	}
	if got := unknown.Code(); got != "artifact_not_found" {
		t.Fatalf("Code() = %q, want artifact_not_found", got)
	}
}
