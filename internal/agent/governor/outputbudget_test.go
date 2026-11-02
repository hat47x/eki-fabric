package governor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/spool"
)

func newBudgetStore(t *testing.T) *spool.Store {
	t.Helper()
	store, err := spool.New(t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestABudgetWithoutALimitAdmits(t *testing.T) {
	budget, err := NewOutputBudget(newBudgetStore(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Admit(1 << 30); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
}

func TestABudgetAdmitsWhenTheOutputFits(t *testing.T) {
	budget, err := NewOutputBudget(newBudgetStore(t), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Admit(1024); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
}

func TestABudgetRefusesWhenTheOutputWouldNotFit(t *testing.T) {
	store := newBudgetStore(t)
	if _, err := store.Put(strings.NewReader(strings.Repeat("x", 900)), 1<<20); err != nil {
		t.Fatal(err)
	}
	budget, err := NewOutputBudget(store, 1000)
	if err != nil {
		t.Fatal(err)
	}
	err = budget.Admit(200)
	if !errors.Is(err, ErrOutputBudget) {
		t.Fatalf("error = %v, want %v", err, ErrOutputBudget)
	}
}

func TestANilBudgetAdmits(t *testing.T) {
	var budget *OutputBudget
	if err := budget.Admit(1 << 20); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
}
