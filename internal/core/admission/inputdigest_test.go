package admission

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

type mapResolver map[string]contract.Digest

func (m mapResolver) ResolveArtifact(_ context.Context, id string) (contract.Digest, error) {
	if d, ok := m[id]; ok {
		return d, nil
	}
	return contract.Digest{}, ErrUnresolvableArtifact
}

func ref(id string) ArtifactReference { return ArtifactReference{Name: id, ArtifactID: id} }

// Inputs are resolved in declaration order, and a repeated input stays repeated:
// the digests describe what the Task declared to read.
func TestInputDigestsResolveInOrder(t *testing.T) {
	first := contract.SHA256([]byte("a"))
	second := contract.SHA256([]byte("b"))
	resolver := mapResolver{"a": first, "b": second}

	got, err := InputDigests(context.Background(), []ArtifactReference{ref("b"), ref("a"), ref("b")}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	want := []contract.Digest{second, first, second}
	for i := range want {
		if got[i].Hex() != want[i].Hex() {
			t.Fatalf("digests = %v, want %v", got, want)
		}
	}
}

// One unresolvable input fails the whole list: a Task with a hole in its inputs is
// not admitted (ADR-0029 §5.3).
func TestAnUnresolvableInputFailsTheList(t *testing.T) {
	resolver := mapResolver{"a": contract.SHA256([]byte("a"))}
	if _, err := InputDigests(context.Background(), []ArtifactReference{ref("a"), ref("missing")}, resolver); !errors.Is(err, ErrUnresolvableArtifact) {
		t.Fatalf("InputDigests() error = %v, want %v", err, ErrUnresolvableArtifact)
	}
}

func TestATaskWithoutInputsHasNoDigests(t *testing.T) {
	got, err := InputDigests(context.Background(), nil, mapResolver{})
	if err != nil || got != nil {
		t.Fatalf("InputDigests() = %v, %v; want nothing", got, err)
	}
}

func TestAResolverThatReturnsNothingIsRefused(t *testing.T) {
	resolver := mapResolver{"": {}}
	if _, err := InputDigests(context.Background(), []ArtifactReference{ref("")}, resolver); !errors.Is(err, ErrUnresolvableArtifact) {
		t.Fatalf("InputDigests() error = %v, want %v", err, ErrUnresolvableArtifact)
	}
}

// The digest-hex resolver recognises digest hex and nothing else: a reference that
// merely looks like an identifier is not guessed at (ADR-0029 §5.4).
func TestTheDigestHexResolverAcceptsOnlyDigestHex(t *testing.T) {
	digest := contract.SHA256([]byte("a"))
	got, err := (DigestIDResolver{}).ResolveArtifact(context.Background(), digest.Hex())
	if err != nil || got.Hex() != digest.Hex() {
		t.Fatalf("ResolveArtifact(digest) = %v, %v", got, err)
	}
	for _, id := range []string{"art_1", "", digest.Hex() + "0", digest.Hex()[:63]} {
		if _, err := (DigestIDResolver{}).ResolveArtifact(context.Background(), id); !errors.Is(err, ErrUnresolvableArtifact) {
			t.Errorf("ResolveArtifact(%q) error = %v, want %v", id, err, ErrUnresolvableArtifact)
		}
	}
}
