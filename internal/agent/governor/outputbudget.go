package governor

import (
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/agent/spool"
)

// OutputBudget answers one question before a Task starts: would the output it is
// expected to produce fit what this host has agreed to hold (ADR-0037 §5, #498).
//
// It is deliberately separate from the resource checks in governor.go. Those ask
// whether the machine can run the Task now; this asks whether the disk can keep
// what the Task leaves behind, which is a different limit and a different owner
// (the host's owner, not the Task's requester).
type OutputBudget struct {
	store *spool.Store
	// limitBytes is how much the host agreed to hold. Zero means the check is
	// off, which keeps a deployment that has not set a budget running as before.
	limitBytes int64
}

// NewOutputBudget returns a budget over store with the given limit. A nil store
// means no bodies are kept, so there is nothing to check.
func NewOutputBudget(store *spool.Store, limitBytes int64) (*OutputBudget, error) {
	if limitBytes < 0 {
		return nil, fmt.Errorf("governor: negative output budget")
	}
	return &OutputBudget{store: store, limitBytes: limitBytes}, nil
}

// Admit reports whether a Task whose output may reach nextBytes may start.
// When the budget is not set, or no store is configured, it admits.
//
// If the budget looks short, it sweeps first and asks again once. Bodies past
// their retention are already lost to the requester (ADR-0037 §5), so refusing
// work for space that is reclaimable would turn a full disk into a refusal that
// was not necessary. Sweeping here never removes a body that is still within its
// retention, so a Task in flight is never affected.
func (b *OutputBudget) Admit(nextBytes int64) error {
	if b == nil || b.store == nil || b.limitBytes == 0 {
		return nil
	}
	err := b.check(nextBytes)
	if err == nil || !errors.Is(err, ErrOutputBudget) {
		return err
	}
	if _, sweepErr := b.store.Sweep(); sweepErr != nil {
		// The sweep is a courtesy, not a precondition: a failure to remove an old
		// body must not hide the answer that the budget is full.
		return err
	}
	return b.check(nextBytes)
}

// check answers the question without sweeping.
func (b *OutputBudget) check(nextBytes int64) error {
	usage, err := b.store.Usage()
	if err != nil {
		// Fail closed: a budget that cannot be read is not a budget that is met.
		return fmt.Errorf("governor: read the output budget: %w", err)
	}
	if err := usage.Within(b.limitBytes, nextBytes); err != nil {
		if errors.Is(err, spool.ErrOverBudget) {
			return fmt.Errorf("%w: %v", ErrOutputBudget, err)
		}
		return err
	}
	return nil
}

// ErrOutputBudget means the Task would put the host over what it agreed to hold.
var ErrOutputBudget = errors.New("the task's output does not fit the host's budget")
