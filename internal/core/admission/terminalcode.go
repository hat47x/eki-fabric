package admission

import "github.com/hat47x/eki-fabric/internal/core/taskstate"

// Public terminal codes. These are contract values (spec/external/openapi.json,
// TaskStatus.terminal_code), not the spelling of the internal TerminalOutcome:
// the internal enum may gain values without changing what a Client sees, and a
// Client branch on a code keeps its meaning (#281).
const (
	// TerminalCompleted means a verified result was accepted. Do not resubmit
	// the work: it was done.
	TerminalCompleted = "COMPLETED"
	// TerminalCancelled means the Task was cancelled on request. Resubmitting
	// is safe: cancellation stops work rather than finishing it.
	TerminalCancelled = "CANCELLED"
	// TerminalFailed means execution failed without a recoverable outcome.
	// Resubmit only after checking whether the failed execution left a side
	// effect: the code does not claim that it did not.
	TerminalFailed = "FAILED"
	// TerminalExhausted means the retry or recovery budget was spent.
	// Resubmitting starts a new Task with a new budget, so it is a decision for
	// the caller, not a retry.
	TerminalExhausted = "EXHAUSTED"
	// TerminalAbandoned means the Task cannot be completed on its current path
	// (a permanent refusal, a policy change with no migration). Do not
	// resubmit the same request unchanged.
	TerminalAbandoned = "ABANDONED"
)

// publicTerminalCode maps one internal TerminalOutcome to its public code. The
// second result is false when this version has no mapping, which happens only
// if the internal enum grew: the caller reports the internal error and uses
// TerminalFailed, the safe answer, because it claims neither completion nor
// cancellation.
//
// The mapping is deliberately a switch over named outcomes rather than the
// spelling of the enum. A new internal value must be added here on purpose;
// the test that walks every outcome fails until it is.
func publicTerminalCode(outcome taskstate.TerminalOutcome) (string, bool) {
	switch outcome {
	case taskstate.TerminalOutcomeCompleted:
		return TerminalCompleted, true
	case taskstate.TerminalOutcomeCancelled:
		return TerminalCancelled, true
	case taskstate.TerminalOutcomeFailed:
		return TerminalFailed, true
	case taskstate.TerminalOutcomeExhausted:
		return TerminalExhausted, true
	case taskstate.TerminalOutcomeAbandoned:
		return TerminalAbandoned, true
	default:
		return "", false
	}
}
