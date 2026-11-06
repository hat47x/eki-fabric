package dispatch

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

type fakeInputs struct {
	digests []contract.Digest
	err     error
}

func (f fakeInputs) LoadTaskInputs(context.Context, ids.TaskID) ([]contract.Digest, error) {
	return f.digests, f.err
}

var taskForHints = ids.TaskID("tsk_00000000000000000000000000000001")

// A deployment that declared its inputs public lets the hint carry the class.
func TestPublicInputsGiveAPublicHint(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	hint, err := InputHints{Inputs: fakeInputs{digests: []contract.Digest{digest}}, PublicInputs: true}.PlacementHint(context.Background(), taskForHints)
	if err != nil {
		t.Fatal(err)
	}
	if !hint.Known || hint.DataClass != "public" || len(hint.Digests) != 1 {
		t.Fatalf("hint = %+v, want a known public hint", hint)
	}
}

// A deployment that did not declare its inputs public gets a hint the placement
// ignores: the claim may not steer where sensitive data goes (ADR-0029 §5.9).
func TestUndeclaredInputsGiveAnInternalHint(t *testing.T) {
	hint, err := InputHints{Inputs: fakeInputs{digests: []contract.Digest{contract.SHA256([]byte("input"))}}}.PlacementHint(context.Background(), taskForHints)
	if err != nil {
		t.Fatal(err)
	}
	if !hint.Known || hint.DataClass != "internal" {
		t.Fatalf("hint = %+v, want an internal hint", hint)
	}
	ordered := orderWorkers([]workerregistry.Worker{}, hint)
	if len(ordered) != 0 {
		t.Fatalf("order = %v, want none", ordered)
	}
}

// A Task without inputs has nothing to hint at.
func TestATaskWithoutInputsHasNoHint(t *testing.T) {
	hint, err := InputHints{Inputs: fakeInputs{}}.PlacementHint(context.Background(), taskForHints)
	if err != nil {
		t.Fatal(err)
	}
	if hint.Known {
		t.Fatalf("hint = %+v, want no hint", hint)
	}
}

// A store that cannot answer, or no store at all, leaves placement on resources.
func TestAFailingInputSourceLeavesNoHint(t *testing.T) {
	for _, hints := range []InputHints{{}, {Inputs: fakeInputs{err: errors.New("unavailable")}}} {
		hint, err := hints.PlacementHint(context.Background(), taskForHints)
		if err != nil || hint.Known {
			t.Fatalf("hint = %+v, %v; want no hint and no error", hint, err)
		}
	}
}
