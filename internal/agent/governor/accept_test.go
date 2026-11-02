package governor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/spool"
)

func acceptingDecision() Decision {
	return Decision{AcceptNewLeases: true, Ceilings: map[Resource]int{ResourceCPU: 80, ResourceMemory: 80}}
}

func TestAcceptAppliesBothTheResourcesAndTheBudget(t *testing.T) {
	store, err := spool.New(t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(strings.NewReader(strings.Repeat("x", 900)), 1<<20); err != nil {
		t.Fatal(err)
	}
	budget, err := NewOutputBudget(store, 1000)
	if err != nil {
		t.Fatal(err)
	}
	need := map[Resource]int{ResourceCPU: 10}
	if err := acceptingDecision().Accept(need, budget, 200); !errors.Is(err, ErrOutputBudget) {
		t.Fatalf("error = %v, want %v", err, ErrOutputBudget)
	}
}

func TestAcceptReportsTheResourceReasonFirst(t *testing.T) {
	store, err := spool.New(t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := NewOutputBudget(store, 0)
	if err != nil {
		t.Fatal(err)
	}
	need := map[Resource]int{ResourceCPU: 90}
	if err := acceptingDecision().Accept(need, budget, 1<<20); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("error = %v, want %v", err, ErrCapacityExceeded)
	}
}

func TestAcceptWithoutABudgetStillAppliesTheResources(t *testing.T) {
	if err := acceptingDecision().Accept(map[Resource]int{ResourceCPU: 10}, nil, 1<<20); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	declining := Decision{AcceptNewLeases: false}
	if err := declining.Accept(map[Resource]int{ResourceCPU: 10}, nil, 1); !errors.Is(err, ErrNotAccepting) {
		t.Fatalf("error = %v, want %v", err, ErrNotAccepting)
	}
}
