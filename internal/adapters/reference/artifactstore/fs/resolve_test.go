package fs

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// An input is accepted when this deployment holds a body for it (ADR-0029 §5.12).
func TestResolveAcceptsAHeldBody(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	digest := put(t, store, "input-bytes")

	got, err := store.Resolve(context.Background(), digest.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if got.Hex() != digest.Hex() {
		t.Fatalf("Resolve() = %s, want %s", got.Hex(), digest.Hex())
	}
}

// An identifier this deployment cannot resolve is refused, whether it is not a
// digest or a digest with no body: admission must not create a Task it cannot feed.
func TestResolveRefusesWhatTheDeploymentCannotProduce(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	absent := contract.SHA256([]byte("absent")).Hex()

	for _, id := range []string{"art_1", "", absent} {
		if _, err := store.Resolve(context.Background(), id); !errors.Is(err, admission.ErrUnresolvableArtifact) {
			t.Errorf("Resolve(%q) error = %v, want %v", id, err, admission.ErrUnresolvableArtifact)
		}
	}
}
