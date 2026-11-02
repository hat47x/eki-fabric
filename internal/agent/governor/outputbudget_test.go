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

func TestAShortBudgetSweepsBeforeItRefuses(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	clock := &now
	store, err := spool.New(t.TempDir(), time.Hour, func() time.Time { return *clock })
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
	// The body is past its retention, so the space is reclaimable.
	*clock = clock.Add(2 * time.Hour)
	if err := budget.Admit(500); err != nil {
		t.Fatalf("Admit() error = %v, want the sweep to make room", err)
	}
	usage, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if usage.Bodies != 0 {
		t.Fatalf("bodies = %d, want 0 after the sweep", usage.Bodies)
	}
}

func TestAShortBudgetWithNoExpiredBodyStillRefuses(t *testing.T) {
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
	if err := budget.Admit(500); !errors.Is(err, ErrOutputBudget) {
		t.Fatalf("error = %v, want %v", err, ErrOutputBudget)
	}
	if usage, err := store.Usage(); err != nil || usage.Bodies != 1 {
		t.Fatalf("usage = %+v, %v: a live body was removed", usage, err)
	}
}
