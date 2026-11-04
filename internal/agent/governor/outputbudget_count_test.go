package governor

import (
	"bytes"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/spool"
)

func budgetWithBody(t *testing.T, limit int64) (*OutputBudget, func() int) {
	t.Helper()
	store, err := spool.New(t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(bytes.NewReader(bytes.Repeat([]byte("x"), 100)), 1024); err != nil {
		t.Fatal(err)
	}
	budget, err := NewOutputBudget(store, limit)
	if err != nil {
		t.Fatal(err)
	}
	counted := 0
	budget.SetRefusalHook(func() { counted++ })
	return budget, func() int { return counted }
}

func TestARefusalIsCounted(t *testing.T) {
	budget, counted := budgetWithBody(t, 100)
	if err := budget.Admit(50); err == nil {
		t.Fatal("Admit() accepted an output that cannot fit")
	}
	if got := counted(); got != 1 {
		t.Fatalf("refusals = %d, want 1", got)
	}
}

func TestAnAdmissionIsNotCounted(t *testing.T) {
	budget, counted := budgetWithBody(t, 1000)
	if err := budget.Admit(10); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	if got := counted(); got != 0 {
		t.Fatalf("refusals = %d, want 0", got)
	}
}

func TestWithoutAHookARefusalStillHappens(t *testing.T) {
	store, err := spool.New(t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(bytes.NewReader(bytes.Repeat([]byte("x"), 100)), 1024); err != nil {
		t.Fatal(err)
	}
	budget, err := NewOutputBudget(store, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Admit(50); err == nil {
		t.Fatal("Admit() accepted an output that cannot fit")
	}
}
