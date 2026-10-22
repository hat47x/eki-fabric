package taskstate

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func TestNewTaskStartsAdmittedAtRevisionOne(t *testing.T) {
	id, err := ids.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() error = %v", err)
	}
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	record, err := NewTask(id, contract.SHA256([]byte("task-ir")), now)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	if record.Phase != TaskPhaseAdmitted {
		t.Fatalf("Phase = %q", record.Phase)
	}
	if record.Revision != 1 {
		t.Fatalf("Revision = %d", record.Revision)
	}
	if record.TerminalOutcome != TerminalOutcomeNone {
		t.Fatalf("TerminalOutcome = %q", record.TerminalOutcome)
	}
}

func TestTaskRecordRequiresOutcomeOnlyWhenTerminal(t *testing.T) {
	id, err := ids.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() error = %v", err)
	}
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	base, err := NewTask(id, contract.SHA256([]byte("task-ir")), now)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	bad := base
	bad.TerminalOutcome = TerminalOutcomeCompleted
	if err := bad.Validate(); !errors.Is(err, ErrInvalidTaskRecord) {
		t.Fatalf("non-terminal outcome error = %v", err)
	}

	bad = base
	bad.Phase = TaskPhaseTerminal
	if err := bad.Validate(); !errors.Is(err, ErrInvalidTaskRecord) {
		t.Fatalf("missing terminal outcome error = %v", err)
	}

	good := base
	good.Phase = TaskPhaseTerminal
	good.TerminalOutcome = TerminalOutcomeCompleted
	good.Revision = 2
	good.UpdatedAt = now.Add(time.Second)
	if err := good.Validate(); err != nil {
		t.Fatalf("terminal Validate() error = %v", err)
	}
}

func TestOnlyAnOpenPhaseAcceptsAResult(t *testing.T) {
	want := map[TaskPhase]bool{
		TaskPhaseAdmitted: false, TaskPhaseReady: true, TaskPhaseActive: true, TaskPhaseVerifying: true,
		TaskPhaseReconciling: false, TaskPhaseHeld: false, TaskPhaseTerminal: false,
	}
	for phase, accepts := range want {
		if phase.AcceptsResult() != accepts {
			t.Fatalf("%s.AcceptsResult() = %v, want %v", phase, !accepts, accepts)
		}
	}
	if TaskPhase("unknown").AcceptsResult() {
		t.Fatal("an unknown phase accepts a result")
	}
}
