package taskstate

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var ErrInvalidTaskRecord = errors.New("invalid task record")

type TaskPhase string

const (
	TaskPhaseAdmitted    TaskPhase = "admitted"
	TaskPhaseReady       TaskPhase = "ready"
	TaskPhaseActive      TaskPhase = "active"
	TaskPhaseVerifying   TaskPhase = "verifying"
	TaskPhaseReconciling TaskPhase = "reconciling"
	TaskPhaseHeld        TaskPhase = "held"
	TaskPhaseTerminal    TaskPhase = "terminal"
)

type TerminalOutcome string

const (
	TerminalOutcomeNone      TerminalOutcome = ""
	TerminalOutcomeCompleted TerminalOutcome = "completed"
	TerminalOutcomeCancelled TerminalOutcome = "cancelled"
	TerminalOutcomeFailed    TerminalOutcome = "failed"
	TerminalOutcomeExhausted TerminalOutcome = "exhausted"
	TerminalOutcomeAbandoned TerminalOutcome = "abandoned"
)

type TaskRecord struct {
	ID                    ids.TaskID
	TaskIRDigest          contract.Digest
	Phase                 TaskPhase
	TerminalOutcome       TerminalOutcome
	CancellationRequested bool
	Revision              uint64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func NewTask(id ids.TaskID, taskIRDigest contract.Digest, now time.Time) (TaskRecord, error) {
	record := TaskRecord{
		ID:           id,
		TaskIRDigest: taskIRDigest,
		Phase:        TaskPhaseAdmitted,
		Revision:     1,
		CreatedAt:    now.UTC(),
		UpdatedAt:    now.UTC(),
	}
	if err := record.Validate(); err != nil {
		return TaskRecord{}, err
	}
	return record, nil
}

func (r TaskRecord) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("%w: task ID is empty", ErrInvalidTaskRecord)
	}
	if r.TaskIRDigest.IsZero() {
		return fmt.Errorf("%w: TaskIR digest is empty", ErrInvalidTaskRecord)
	}
	if !validPhase(r.Phase) {
		return fmt.Errorf("%w: unknown phase %q", ErrInvalidTaskRecord, r.Phase)
	}
	if r.Revision == 0 {
		return fmt.Errorf("%w: revision must be positive", ErrInvalidTaskRecord)
	}
	if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: timestamps are required", ErrInvalidTaskRecord)
	}
	if r.UpdatedAt.Before(r.CreatedAt) {
		return fmt.Errorf("%w: updated time precedes created time", ErrInvalidTaskRecord)
	}
	if r.Phase == TaskPhaseTerminal {
		if !validOutcome(r.TerminalOutcome) || r.TerminalOutcome == TerminalOutcomeNone {
			return fmt.Errorf("%w: terminal task requires a terminal outcome", ErrInvalidTaskRecord)
		}
	} else if r.TerminalOutcome != TerminalOutcomeNone {
		return fmt.Errorf("%w: non-terminal task cannot have terminal outcome %q", ErrInvalidTaskRecord, r.TerminalOutcome)
	}
	return nil
}

func validPhase(v TaskPhase) bool {
	switch v {
	case TaskPhaseAdmitted, TaskPhaseReady, TaskPhaseActive, TaskPhaseVerifying, TaskPhaseReconciling, TaskPhaseHeld, TaskPhaseTerminal:
		return true
	default:
		return false
	}
}

// AcceptsResult reports whether a verified result may still complete a Task in
// this phase. A Task that is terminal, held or reconciling is not completed by a
// late result.
func (p TaskPhase) AcceptsResult() bool {
	switch p {
	case TaskPhaseReady, TaskPhaseActive, TaskPhaseVerifying:
		return true
	}
	return false
}

func validOutcome(v TerminalOutcome) bool {
	switch v {
	case TerminalOutcomeNone, TerminalOutcomeCompleted, TerminalOutcomeCancelled, TerminalOutcomeFailed, TerminalOutcomeExhausted, TerminalOutcomeAbandoned:
		return true
	default:
		return false
	}
}
