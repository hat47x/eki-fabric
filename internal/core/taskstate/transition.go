package taskstate

import (
	"errors"
	"fmt"
)

// ErrInvalidTaskTransition means a durable Task write is not a transition the
// state model allows. Adapters report it separately from ErrConflict: a
// conflict says "read again and retry", while an invalid transition says "this
// write must not happen at all".
var ErrInvalidTaskTransition = errors.New("invalid task transition")

// ValidateTransition checks one durable Task state transition.
//
// The Task phase moves forward through its lifecycle and never returns from
// terminal. Same-phase writes are allowed because a Task carries state beside
// its phase (the cancellation intent, for example) that changes in place.
//
// Completion is refused here (#280, #365): a Task only ends as completed
// through the accepted-result commit, which writes the AcceptedResult, the
// closed Attempt and the Task in one transaction. The generic CAS cannot see
// the result, so it cannot carry the evidence completion claims. A completed
// write still reaches this function through CommitAcceptedResult's own
// validation path, which checks the record directly.
func ValidateTransition(current, next TaskRecord) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("%w: current: %v", ErrInvalidTaskTransition, err)
	}
	if err := next.Validate(); err != nil {
		return fmt.Errorf("%w: next: %v", ErrInvalidTaskTransition, err)
	}
	if next.Phase == TaskPhaseTerminal && next.TerminalOutcome == TerminalOutcomeCompleted {
		return fmt.Errorf("%w: completion is written by the accepted-result commit, not a generic write", ErrInvalidTaskTransition)
	}
	if current.ID != next.ID || current.TaskIRDigest != next.TaskIRDigest || !current.CreatedAt.Equal(next.CreatedAt) {
		return fmt.Errorf("%w: immutable Task identity changed", ErrInvalidTaskTransition)
	}
	if next.Revision != current.Revision+1 {
		return fmt.Errorf("%w: revision %d does not follow %d", ErrInvalidTaskTransition, next.Revision, current.Revision)
	}
	if next.UpdatedAt.Before(current.UpdatedAt) {
		return fmt.Errorf("%w: updated time moved backwards", ErrInvalidTaskTransition)
	}
	if current.Phase == TaskPhaseTerminal {
		return fmt.Errorf("%w: terminal Task cannot transition", ErrInvalidTaskTransition)
	}
	if !allowsPhase(current.Phase, next.Phase) {
		return fmt.Errorf("%w: phase %q -> %q", ErrInvalidTaskTransition, current.Phase, next.Phase)
	}
	return nil
}

// allowsPhase reports whether the lifecycle may move from one phase to
// another. The edges follow architecture/TASK_STATE_MODEL.md §5:
//
//   - admitted: an Attempt has not been prepared yet; it becomes ready, or
//     active when the first Attempt is created without a queueing step.
//   - ready: a new Attempt may start, so the next phase is active.
//   - active: verification begins (verifying), the last Attempt closed and a
//     retry is possible (ready), or the outcome is unknown (reconciling).
//   - verifying: a rejected result allows another Attempt (active), and an
//     unknown external effect needs reconciliation (reconciling).
//   - reconciling: the effect is settled and work resumes (active).
//   - held: an operator released the Task to work (ready or active).
//
// Every non-terminal phase may become held, and any phase may end. A Task that
// ended never moves again.
func allowsPhase(from, to TaskPhase) bool {
	if from == to {
		return true
	}
	switch from {
	case TaskPhaseAdmitted:
		return to == TaskPhaseReady || to == TaskPhaseActive || to == TaskPhaseHeld || to == TaskPhaseTerminal
	case TaskPhaseReady:
		return to == TaskPhaseActive || to == TaskPhaseHeld || to == TaskPhaseTerminal
	case TaskPhaseActive:
		return to == TaskPhaseVerifying || to == TaskPhaseReady || to == TaskPhaseReconciling ||
			to == TaskPhaseHeld || to == TaskPhaseTerminal
	case TaskPhaseVerifying:
		return to == TaskPhaseActive || to == TaskPhaseReconciling || to == TaskPhaseHeld || to == TaskPhaseTerminal
	case TaskPhaseReconciling:
		return to == TaskPhaseActive || to == TaskPhaseHeld || to == TaskPhaseTerminal
	case TaskPhaseHeld:
		return to == TaskPhaseReady || to == TaskPhaseActive || to == TaskPhaseTerminal
	}
	return false
}
