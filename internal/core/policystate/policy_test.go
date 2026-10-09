package policystate

import (
	"errors"
	"testing"
	"time"
)

func TestPolicyStateLifecycle(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	p1, err := NewActive(1, "policy-1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if !p1.ActiveRevision("policy-1") {
		t.Fatal("active policy did not recognize its revision")
	}
	revoked, err := Revoke(p1, 2, t0.Add(time.Minute), "emergency")
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Status != StatusRevoked || revoked.Epoch != 2 || revoked.Revision != p1.Revision {
		t.Fatalf("revoked state = %#v", revoked)
	}

	p2, err := NewActive(3, "policy-2", t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransition(revoked, p2); err != nil {
		t.Fatalf("revoked -> new active transition error = %v", err)
	}
}

func TestPolicyStateRejectsRollbackAndReactivation(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	p1, _ := NewActive(1, "policy-1", t0)
	revoked, _ := Revoke(p1, 2, t0.Add(time.Minute), "revoke")

	same, _ := NewActive(3, "policy-1", t0.Add(2*time.Minute))
	if err := ValidateTransition(revoked, same); !errors.Is(err, ErrInvalidPolicyState) {
		t.Fatalf("reactivation error = %v", err)
	}

	p2, _ := NewActive(3, "policy-2", t0.Add(2*time.Minute))
	p2.Epoch = 4
	if err := ValidateTransition(revoked, p2); !errors.Is(err, ErrInvalidPolicyState) {
		t.Fatalf("epoch jump error = %v", err)
	}
}

func TestPolicyStateRejectsMalformedRevocation(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	state := State{
		Epoch:       1,
		Revision:    "policy-1",
		Status:      StatusRevoked,
		ActivatedAt: t0,
		RevokedAt:   t0.Add(time.Minute),
	}
	if err := state.Validate(); !errors.Is(err, ErrInvalidPolicyState) {
		t.Fatalf("Validate() error = %v", err)
	}
}
