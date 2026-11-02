package governor

// Accept is the one call a Control Plane-facing path makes before it takes a
// Lease: it applies both the resource decision and the host's output budget
// (ADR-0037 §5, #498).
//
// Keeping the two checks together is deliberate. A host that has room to run the
// Task but no room to keep what it leaves behind would have to fail after the
// work is done, which wastes the work and leaves the Task's result unreachable.
func (d Decision) Accept(need map[Resource]int, budget *OutputBudget, expectedOutputBytes int64) error {
	if err := d.Admit(need); err != nil {
		return err
	}
	return budget.Admit(expectedOutputBytes)
}
