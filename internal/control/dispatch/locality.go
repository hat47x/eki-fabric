package dispatch

// LocalityHint says whether a Worker's claim to hold an artifact may be used to
// prefer it, or only to break ties between Workers that are already equally good.
//
// The claim is what the Worker says, not what EKI checked (ADR-0029 §5). Using it
// to decide where sensitive data goes would let the claim steer the data, so the
// hint is allowed only for what a deployment declared public. Everything else is
// placed by resources alone.
//
// The empty class means "declared nothing" (#596), and an undeclared artifact is
// not evidence that it is safe to move around: it is treated like the strictest
// case, because the alternative would reward not declaring.
type LocalityHint struct {
	// DataClass is the declaration on the artifact being placed.
	DataClass string
}

// Allowed reports whether the claim may influence the choice of Worker.
func (h LocalityHint) Allowed() bool {
	return h.DataClass == "public"
}

// PreferOrders reorders candidates that have already been selected for fit. A
// Worker whose claim includes the artifact comes first; the rest keep their order.
//
// It is deliberately a tie-breaker rather than a filter: a Worker that cannot hold
// the Task is never chosen because it claims to hold the input, so the resource
// decision stays the decision and the claim only says which of two adequate Workers
// to try first.
func (h LocalityHint) PreferOrders(holds []bool) []int {
	order := make([]int, 0, len(holds))
	for i, held := range holds {
		if held {
			order = append(order, i)
		}
	}
	for i, held := range holds {
		if !held {
			order = append(order, i)
		}
	}
	return order
}
