package taskstate

// Withdrawn reports whether the Task ended, or was asked to end, before its
// result could be handed over (ADR-0037 §9, #592).
//
// It answers one question for the artifact path: may this Task's body still be
// fetched? A Task that was cancelled, or that a cancellation was requested for,
// answers no even while the body is still kept for an investigation. The request
// counts as withdrawn too: waiting for the terminal state would keep serving a body
// the requester has already asked to stop producing.
func (r TaskRecord) Withdrawn() bool {
	if r.CancellationRequested {
		return true
	}
	return r.TerminalOutcome == TerminalOutcomeCancelled
}
