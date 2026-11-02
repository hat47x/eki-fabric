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
// A body that is past its retention still occupies the disk, so the caller that
// wants the space back must sweep before asking; that ordering is the caller's
// decision because only it knows whether losing an old body is acceptable.
func (b *OutputBudget) Admit(nextBytes int64) error {
	if b == nil || b.store == nil || b.limitBytes == 0 {
		return nil
	}
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
