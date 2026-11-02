package executor

// BodyOf returns the completed output bytes behind a Runner, when the Runner
// offers them (ADR-0034 2.1: the body stays on the Worker until the claim is
// queued and the body sent). Runners that report no body simply have nothing
// to send; the Supervisor's Outcome is unchanged.
func BodyOf(runner Runner) ([]byte, bool) {
	type bodyer interface{ BodyOf() ([]byte, bool) }
	if b, ok := any(runner).(bodyer); ok {
		return b.BodyOf()
	}
	return nil, false
}
