package service

import (
	"bytes"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/spool"
)

func TestWithoutALimitNothingIsRefused(t *testing.T) {
	budget, err := openBudget(nil, 0)
	if err != nil {
		t.Fatalf("openBudget() error = %v", err)
	}
	if err := budget.Admit(1 << 20); err != nil {
		t.Fatalf("Admit() error = %v, want nil", err)
	}
}

func TestWithASpoolAndALimitTheBudgetRefuses(t *testing.T) {
	store, err := spool.New(t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(bytes.NewReader(bytes.Repeat([]byte("x"), 100)), 1024); err != nil {
		t.Fatal(err)
	}
	budget, err := openBudget(store, 100)
	if err != nil {
		t.Fatalf("openBudget() error = %v", err)
	}
	if err := budget.Admit(50); err == nil {
		t.Fatal("Admit() accepted an output that cannot fit")
	}
}

func TestANegativeLimitIsRefused(t *testing.T) {
	if _, err := openBudget(nil, -1); err == nil {
		t.Fatal("openBudget() accepted a negative limit")
	}
}
