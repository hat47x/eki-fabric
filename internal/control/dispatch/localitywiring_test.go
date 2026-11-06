package dispatch

import (
	"context"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

func claimWorker(t *testing.T, seed string, held ...contract.Digest) workerregistry.Worker {
	t.Helper()
	w := workerregistry.Worker{
		Identity:      ids.WorkerIdentity("wrk_" + seed),
		TrustDomain:   "example.org",
		State:         workerregistry.StateRegistered,
		Revision:      1,
		HeldArtifacts: workerregistry.HeldArtifacts(held),
	}
	return w
}

// A public input puts the Worker that claims it first, and keeps the others.
func TestAPublicInputPutsTheHolderFirst(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	plain := claimWorker(t, "00000000000000000000000000000001")
	holder := claimWorker(t, "00000000000000000000000000000002", digest)

	ordered := orderWorkers([]workerregistry.Worker{plain, holder}, PlacementHint{Known: true, DataClass: "public", Digests: []contract.Digest{digest}})
	if ordered[0].Identity != holder.Identity {
		t.Fatalf("first = %s, want the Worker that claims the input", ordered[0].Identity)
	}
	if len(ordered) != 2 {
		t.Fatalf("order = %v, want both Workers kept", ordered)
	}
}

// A non-public input leaves the order alone: the claim may not steer where
// sensitive data goes (ADR-0029 §5).
func TestANonPublicInputKeepsTheOrder(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	plain := claimWorker(t, "00000000000000000000000000000001")
	holder := claimWorker(t, "00000000000000000000000000000002", digest)

	for _, class := range []string{"internal", "restricted", ""} {
		ordered := orderWorkers([]workerregistry.Worker{plain, holder}, PlacementHint{Known: true, DataClass: class, Digests: []contract.Digest{digest}})
		if ordered[0].Identity != plain.Identity {
			t.Fatalf("class %q: first = %s, want the order unchanged", class, ordered[0].Identity)
		}
	}
}

// A hint nobody could produce, and a Task without inputs, both leave placement as
// it was: the hint is an optimisation, not a requirement.
func TestAnUnknownHintKeepsTheOrder(t *testing.T) {
	plain := claimWorker(t, "00000000000000000000000000000001")
	holder := claimWorker(t, "00000000000000000000000000000002", contract.SHA256([]byte("input")))

	for _, hint := range []PlacementHint{{}, {Known: true, DataClass: "public"}} {
		ordered := orderWorkers([]workerregistry.Worker{plain, holder}, hint)
		if ordered[0].Identity != plain.Identity {
			t.Fatalf("hint %+v: first = %s, want the order unchanged", hint, ordered[0].Identity)
		}
	}
}

type failingHint struct{}

func (failingHint) PlacementHint(context.Context, ids.TaskID) (PlacementHint, error) {
	return PlacementHint{}, errHint
}

var errHint = contractError("hint unavailable")

type contractError string

func (e contractError) Error() string { return string(e) }

// A hint source that fails does not fail placement: the decision falls back to the
// resource order rather than refusing to place the Task.
func TestAFailingHintDoesNotFailPlacement(t *testing.T) {
	plain := claimWorker(t, "00000000000000000000000000000001")
	holder := claimWorker(t, "00000000000000000000000000000002", contract.SHA256([]byte("input")))
	loop := &Loop{cfg: Config{Hints: failingHint{}}}

	ordered := loop.pickWorkerWithHint(context.Background(), ids.TaskID("tsk_00000000000000000000000000000001"), []workerregistry.Worker{plain, holder})
	if len(ordered) != 2 || ordered[0].Identity != plain.Identity {
		t.Fatalf("order = %v, want the resource order", ordered)
	}
}
