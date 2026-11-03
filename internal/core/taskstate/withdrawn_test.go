package taskstate

import "testing"

func TestARunningTaskIsNotWithdrawn(t *testing.T) {
	record := TaskRecord{Phase: TaskPhaseActive}
	if record.Withdrawn() {
		t.Fatal("Withdrawn() = true for a running Task")
	}
}

func TestARequestToCancelWithdrawsStraightaway(t *testing.T) {
	record := TaskRecord{Phase: TaskPhaseActive, CancellationRequested: true}
	if !record.Withdrawn() {
		t.Fatal("Withdrawn() = false after a cancellation was requested: waiting for the terminal state would keep serving a body the requester asked to stop")
	}
}

func TestACancelledTaskIsWithdrawn(t *testing.T) {
	record := TaskRecord{Phase: TaskPhaseTerminal, TerminalOutcome: TerminalOutcomeCancelled}
	if !record.Withdrawn() {
		t.Fatal("Withdrawn() = false for a cancelled Task")
	}
}

func TestAnotherTerminalOutcomeIsNotWithdrawn(t *testing.T) {
	for _, outcome := range []TerminalOutcome{TerminalOutcomeCompleted, TerminalOutcomeFailed, TerminalOutcomeExhausted, TerminalOutcomeAbandoned} {
		record := TaskRecord{Phase: TaskPhaseTerminal, TerminalOutcome: outcome}
		if record.Withdrawn() {
			t.Fatalf("Withdrawn() = true for outcome %q: a finished Task's result is still the requester's", outcome)
		}
	}
}
