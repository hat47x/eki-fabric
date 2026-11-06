package dispatch

import (
	"context"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// TaskInputSource reads a Task's input digests in declaration order. The state store
// implements it; a deployment without one leaves placement on resources alone.
type TaskInputSource interface {
	LoadTaskInputs(ctx context.Context, taskID ids.TaskID) ([]contract.Digest, error)
}

// InputHints answers the placement question from what the deployment recorded: the
// Task's input digests, and whether this deployment declared its inputs public
// (ADR-0029 §5.9).
//
// The class is not read per Task and not taken from a Worker's claim: a deployment
// that says "my inputs are public" is making a statement about itself, and that is
// the only statement the hint is allowed to rely on.
type InputHints struct {
	Inputs TaskInputSource
	// PublicInputs is the deployment's declaration. False keeps the hint off.
	PublicInputs bool
}

// PlacementHint implements PlacementHintSource.
func (h InputHints) PlacementHint(ctx context.Context, taskID ids.TaskID) (PlacementHint, error) {
	if h.Inputs == nil {
		return PlacementHint{}, nil
	}
	digests, err := h.Inputs.LoadTaskInputs(ctx, taskID)
	if err != nil {
		// The hint is an optimisation: a store that cannot answer leaves the
		// decision on resources instead of failing placement.
		return PlacementHint{}, nil
	}
	if len(digests) == 0 {
		return PlacementHint{}, nil
	}
	class := "internal"
	if h.PublicInputs {
		class = "public"
	}
	return PlacementHint{Known: true, DataClass: class, Digests: digests}, nil
}

var _ PlacementHintSource = InputHints{}

func (h InputHints) String() string {
	return fmt.Sprintf("InputHints{public=%t}", h.PublicInputs)
}
