package dispatch

import (
	"context"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

// PlacementHint is what the placement decision may know about a Task's inputs
// beyond the Task record: the digests, and the class the deployment declared for
// them (ADR-0029 §5.3, §5.5).
//
// The class travels with the digests because the hint is only allowed for what the
// deployment called public: a Task whose inputs say nothing is placed by resources
// alone, the same as before this existed.
type PlacementHint struct {
	Digests   []contract.Digest
	DataClass string
	// Known is false when the deployment could not say anything about the inputs.
	// A hint that is merely empty and a hint nobody could produce are different
	// answers, and only the second one means "do not look".
	Known bool
}

// PlacementHintSource answers what the placement decision may know about a Task's
// inputs. A nil source keeps placement on resources alone.
type PlacementHintSource interface {
	PlacementHint(ctx context.Context, taskID ids.TaskID) (PlacementHint, error)
}

// orderWorkers gives the order in which usable Workers should be tried, using the
// locality hint when it is allowed and ignoring it otherwise.
//
// The hint only reorders: every usable Worker stays in the list, so a Worker that
// fits is never dropped for not claiming the inputs (ADR-0029 §5).
func orderWorkers(workers []workerregistry.Worker, hint PlacementHint) []workerregistry.Worker {
	usable := make([]workerregistry.Worker, 0, len(workers))
	for _, w := range workers {
		if w.Usable() {
			usable = append(usable, w)
		}
	}
	if len(usable) < 2 || !hint.Known || len(hint.Digests) == 0 {
		return usable
	}
	locality := LocalityHint{DataClass: hint.DataClass}
	if !locality.Allowed() {
		return usable
	}
	holds := make([]bool, len(usable))
	for i, w := range usable {
		holds[i] = holdsAny(w.HeldArtifacts, hint.Digests)
	}
	order := locality.PreferOrders(holds)
	ordered := make([]workerregistry.Worker, 0, len(usable))
	for _, i := range order {
		ordered = append(ordered, usable[i])
	}
	return ordered
}

// holdsAny reports whether the Worker's claim includes any of the digests. A claim
// that does not validate holds nothing, which HeldArtifacts.Holds already enforces.
func holdsAny(claim workerregistry.HeldArtifacts, digests []contract.Digest) bool {
	for _, digest := range digests {
		if claim.Holds(digest) {
			return true
		}
	}
	return false
}

// pickWorkerWithHint applies the hint source when one is configured. A source that
// fails does not fail placement: the hint is an optimisation, so the decision falls
// back to the resource order it would have used anyway.
func (l *Loop) pickWorkerWithHint(ctx context.Context, taskID ids.TaskID, usable []workerregistry.Worker) []workerregistry.Worker {
	if l.cfg.Hints == nil || len(usable) < 2 {
		return usable
	}
	hint, err := l.cfg.Hints.PlacementHint(ctx, taskID)
	if err != nil || !hint.Known {
		return usable
	}
	return orderWorkers(usable, hint)
}
