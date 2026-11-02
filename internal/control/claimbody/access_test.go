package claimbody

import "testing"

func TestTheOwnerSeesTheBody(t *testing.T) {
	if got := Decide("alice", "alice", true, false); got != OutcomeDeliver {
		t.Fatalf("Decide() = %v, want %v", got, OutcomeDeliver)
	}
}

func TestAnotherPrincipalCannotTellExistence(t *testing.T) {
	for _, exists := range []bool{true, false} {
		if got := Decide("mallory", "alice", exists, false); got != OutcomeNotFound {
			t.Fatalf("Decide(exists=%v) = %v, want %v", exists, got, OutcomeNotFound)
		}
	}
}

func TestAnExpiredArtifactIsOnlyReportedToItsOwner(t *testing.T) {
	if got := Decide("alice", "alice", true, true); got != OutcomeExpired {
		t.Fatalf("Decide() = %v, want %v", got, OutcomeExpired)
	}
	if got := Decide("mallory", "alice", true, true); got != OutcomeNotFound {
		t.Fatalf("Decide(foreign) = %v, want %v", got, OutcomeNotFound)
	}
}

func TestAMissingArtifactIsNotFoundEvenForTheOwner(t *testing.T) {
	if got := Decide("alice", "alice", false, true); got != OutcomeNotFound {
		t.Fatalf("Decide() = %v, want %v", got, OutcomeNotFound)
	}
}

func TestAnArtifactWithoutAnOwnerIsNotFound(t *testing.T) {
	if got := Decide("alice", "", true, false); got != OutcomeNotFound {
		t.Fatalf("Decide() = %v, want %v", got, OutcomeNotFound)
	}
}
