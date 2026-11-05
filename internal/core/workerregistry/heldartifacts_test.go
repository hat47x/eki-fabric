package workerregistry

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func digestOf(value string) contract.Digest { return contract.SHA256([]byte(value)) }

// A claim is a hint, so a broken one is dropped rather than repaired: a hint that is
// silently wrong is worse than no hint (ADR-0029 §5).
func TestAnEmptyClaimIsValid(t *testing.T) {
	if err := (HeldArtifacts{}).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want an empty claim accepted", err)
	}
}

func TestAClaimWithAnEmptyEntryIsRefused(t *testing.T) {
	claim := HeldArtifacts{digestOf("a"), {}}
	if !errors.Is(claim.Validate(), ErrHeldArtifacts) {
		t.Fatalf("Validate() = %v, want %v", claim.Validate(), ErrHeldArtifacts)
	}
}

func TestAClaimLongerThanTheLimitIsRefused(t *testing.T) {
	claim := make(HeldArtifacts, 0, MaxHeldArtifacts+1)
	for i := 0; i <= MaxHeldArtifacts; i++ {
		claim = append(claim, digestOf(string(rune('a'+i%26))+string(rune('0'+i/26))))
	}
	if !errors.Is(claim.Validate(), ErrHeldArtifacts) {
		t.Fatalf("Validate() = %v, want %v", claim.Validate(), ErrHeldArtifacts)
	}
}

func TestHoldsFindsTheDigest(t *testing.T) {
	held := digestOf("artifact-a")
	claim := HeldArtifacts{held, digestOf("artifact-b")}
	if !claim.Holds(held) {
		t.Fatal("Holds() = false for a digest in the claim")
	}
	if claim.Holds(digestOf("artifact-c")) {
		t.Fatal("Holds() = true for a digest not in the claim")
	}
}

// A claim that does not validate holds nothing, so a malformed list cannot be used
// to steer placement even partially.
func TestABrokenClaimHoldsNothing(t *testing.T) {
	held := digestOf("artifact-a")
	claim := HeldArtifacts{held, {}}
	if claim.Holds(held) {
		t.Fatal("a claim with an empty entry was used as a hint")
	}
}
