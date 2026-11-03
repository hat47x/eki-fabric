package startgate

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
)

func TestARequestWithinTheCeilingPasses(t *testing.T) {
	decision := governor.Decision{Ceilings: map[governor.Resource]int{governor.ResourceCPU: 50, governor.ResourceMemory: 40}}
	need := map[governor.Resource]int{governor.ResourceCPU: 25, governor.ResourceMemory: 40}
	if err := RequireWithinCeiling(decision, need); err != nil {
		t.Fatalf("RequireWithinCeiling() error = %v", err)
	}
}

func TestARequestAboveTheCeilingIsRefusedWithNumbers(t *testing.T) {
	decision := governor.Decision{Ceilings: map[governor.Resource]int{governor.ResourceCPU: 20}}
	need := map[governor.Resource]int{governor.ResourceCPU: 60}
	err := RequireWithinCeiling(decision, need)
	if !errors.Is(err, ErrNeedExceedsCeiling) {
		t.Fatalf("RequireWithinCeiling() error = %v, want %v", err, ErrNeedExceedsCeiling)
	}
	for _, fragment := range []string{"60%", "20%"} {
		if !contains(err.Error(), fragment) {
			t.Fatalf("error %q does not say %q: the operator needs the numbers", err, fragment)
		}
	}
}

func TestAnEmptyNeedPasses(t *testing.T) {
	if err := RequireWithinCeiling(governor.Decision{}, nil); err != nil {
		t.Fatalf("RequireWithinCeiling() error = %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
