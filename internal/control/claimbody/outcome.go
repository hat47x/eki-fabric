package claimbody

// Status is the HTTP status a retrieval is answered with (spec/external/README.md,
// #494).
func (o Outcome) Status() int {
	switch o {
	case OutcomeExpired:
		return 410
	case OutcomeDeliver:
		return 200
	default:
		return 404
	}
}

// Code is the machine-readable code that goes with Status. It is the value a
// client can branch on without parsing prose.
func (o Outcome) Code() string {
	switch o {
	case OutcomeExpired:
		return "artifact_expired"
	case OutcomeDeliver:
		return ""
	default:
		return "artifact_not_found"
	}
}
