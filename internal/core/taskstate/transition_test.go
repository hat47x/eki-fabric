package taskstate

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func transitionRecord(phase TaskPhase) TaskRecord {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	record := TaskRecord{
		ID:           "tsk_00000000000000000000000000000001",
		TaskIRDigest: contract.SHA256([]byte("task ir")),
		Phase:        phase,
		Revision:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if phase == TaskPhaseTerminal {
		record.TerminalOutcome = TerminalOutcomeCancelled
	}
	return record
}

func nextRecord(current TaskRecord, phase TaskPhase, outcome TerminalOutcome) TaskRecord {
	next := current
	next.Phase = phase
	next.TerminalOutcome = outcome
	next.Revision = current.Revision + 1
	next.UpdatedAt = current.UpdatedAt.Add(time.Second)
	return next
}

func TestValidateTransitionAllowsTheLifecycle(t *testing.T) {
	admitted := transitionRecord(TaskPhaseAdmitted)
	ready := nextRecord(admitted, TaskPhaseReady, TerminalOutcomeNone)
	active := nextRecord(ready, TaskPhaseActive, TerminalOutcomeNone)
	verifying := nextRecord(active, TaskPhaseVerifying, TerminalOutcomeNone)
	failed := nextRecord(verifying, TaskPhaseTerminal, TerminalOutcomeFailed)

	for _, tc := range []struct {
		name          string
		current, next TaskRecord
	}{
		{"admitted to ready", admitted, ready},
		{"ready to active", ready, active},
		{"active to verifying", active, verifying},
		{"verifying to terminal", verifying, failed},
		{"active to ready (retry after a closed Attempt)", active, nextRecord(active, TaskPhaseReady, TerminalOutcomeNone)},
		{"active to reconciling", active, nextRecord(active, TaskPhaseReconciling, TerminalOutcomeNone)},
		{"reconciling to active", nextRecord(active, TaskPhaseReconciling, TerminalOutcomeNone),
			nextRecord(nextRecord(active, TaskPhaseReconciling, TerminalOutcomeNone), TaskPhaseActive, TerminalOutcomeNone)},
		{"held to ready", nextRecord(active, TaskPhaseHeld, TerminalOutcomeNone), nextRecord(nextRecord(active, TaskPhaseHeld, TerminalOutcomeNone), TaskPhaseReady, TerminalOutcomeNone)},
		{"active to terminal cancelled", active, nextRecord(active, TaskPhaseTerminal, TerminalOutcomeCancelled)},
		{"active to terminal exhausted", active, nextRecord(active, TaskPhaseTerminal, TerminalOutcomeExhausted)},
		{"same phase keeps the cancellation intent", active, func() TaskRecord {
			next := nextRecord(active, TaskPhaseActive, TerminalOutcomeNone)
			next.CancellationRequested = true
			return next
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTransition(tc.current, tc.next); err != nil {
				t.Fatalf("ValidateTransition() error = %v", err)
			}
		})
	}
}

func TestValidateTransitionRefusesImpossibleWrites(t *testing.T) {
	admitted := transitionRecord(TaskPhaseAdmitted)
	ready := nextRecord(admitted, TaskPhaseReady, TerminalOutcomeNone)
	active := nextRecord(ready, TaskPhaseActive, TerminalOutcomeNone)
	terminal := nextRecord(active, TaskPhaseTerminal, TerminalOutcomeCancelled)

	reopen := nextRecord(terminal, TaskPhaseReady, TerminalOutcomeNone)

	staleRevision := nextRecord(active, TaskPhaseVerifying, TerminalOutcomeNone)
	staleRevision.Revision = active.Revision + 2

	olderUpdate := nextRecord(active, TaskPhaseVerifying, TerminalOutcomeNone)
	olderUpdate.UpdatedAt = active.UpdatedAt.Add(-time.Second)

	rebound := nextRecord(active, TaskPhaseVerifying, TerminalOutcomeNone)
	rebound.TaskIRDigest = contract.SHA256([]byte("another task ir"))

	newIdentity := nextRecord(active, TaskPhaseVerifying, TerminalOutcomeNone)
	newIdentity.CreatedAt = active.CreatedAt.Add(time.Hour)

	for _, tc := range []struct {
		name          string
		current, next TaskRecord
	}{
		{"terminal cannot reopen", terminal, reopen},
		{"terminal cannot stay terminal", terminal, nextRecord(terminal, TaskPhaseTerminal, TerminalOutcomeFailed)},
		{"ready cannot go back to admitted", ready, nextRecord(ready, TaskPhaseAdmitted, TerminalOutcomeNone)},
		{"admitted cannot skip to verifying", admitted, nextRecord(admitted, TaskPhaseVerifying, TerminalOutcomeNone)},
		{"reconciling cannot go back to verifying", nextRecord(active, TaskPhaseReconciling, TerminalOutcomeNone),
			nextRecord(nextRecord(active, TaskPhaseReconciling, TerminalOutcomeNone), TaskPhaseVerifying, TerminalOutcomeNone)},
		{"revision must follow by one", active, staleRevision},
		{"updated time cannot move backwards", active, olderUpdate},
		{"TaskIR digest is immutable", active, rebound},
		{"creation time is immutable", active, newIdentity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTransition(tc.current, tc.next)
			if !errors.Is(err, ErrInvalidTaskTransition) {
				t.Fatalf("ValidateTransition() error = %v, want %v", err, ErrInvalidTaskTransition)
			}
		})
	}
}

func TestValidateTransitionRefusesMalformedRecords(t *testing.T) {
	admitted := transitionRecord(TaskPhaseAdmitted)
	unknownPhase := nextRecord(admitted, TaskPhase("sleeping"), TerminalOutcomeNone)
	if err := ValidateTransition(admitted, unknownPhase); !errors.Is(err, ErrInvalidTaskTransition) {
		t.Fatalf("unknown phase error = %v, want %v", err, ErrInvalidTaskTransition)
	}
}
