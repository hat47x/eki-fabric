package claimreconcile

import (
	"errors"
	"testing"
	"time"
)

var (
	expiry = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	grace  = 30 * time.Second
)

func input() Input {
	return Input{AttemptOpen: true, LeaseExpiresAt: expiry, Grace: grace}
}

func TestNoActionForAClosedAttemptOrAnExistingClaim(t *testing.T) {
	closed := input()
	closed.AttemptOpen = false
	if got, err := Decide(expiry.Add(time.Hour), closed); err != nil || got != NoAction {
		t.Fatalf("closed: Decide() = %q, %v", got, err)
	}
	claimed := input()
	claimed.HasClaim = true
	if got, err := Decide(expiry.Add(time.Hour), claimed); err != nil || got != NoAction {
		t.Fatalf("claimed: Decide() = %q, %v", got, err)
	}
}

func TestAwaitClaimDuringTheGracePeriod(t *testing.T) {
	if got, err := Decide(expiry.Add(-time.Second), input()); err != nil || got != AwaitClaim {
		t.Fatalf("before expiry: Decide() = %q, %v", got, err)
	}
	if got, err := Decide(expiry.Add(grace-time.Millisecond), input()); err != nil || got != AwaitClaim {
		t.Fatalf("inside grace: Decide() = %q, %v", got, err)
	}
}

func TestCloseLeaseExpiredAfterTheGracePeriod(t *testing.T) {
	if got, err := Decide(expiry.Add(grace), input()); err != nil || got != CloseLeaseExpired {
		t.Fatalf("at grace boundary: Decide() = %q, %v", got, err)
	}
	if got, err := Decide(expiry.Add(time.Hour), input()); err != nil || got != CloseLeaseExpired {
		t.Fatalf("after grace: Decide() = %q, %v", got, err)
	}
}

func TestReconcileWhenAnExternalEffectIsPossible(t *testing.T) {
	withEffect := input()
	withEffect.PossibleExternalEffect = true
	if got, err := Decide(expiry.Add(time.Hour), withEffect); err != nil || got != Reconcile {
		t.Fatalf("Decide() = %q, %v; want reconcile", got, err)
	}
	// The grace period still applies: the claim may simply be late.
	if got, err := Decide(expiry.Add(time.Second), withEffect); err != nil || got != AwaitClaim {
		t.Fatalf("early Decide() = %q, %v; want await", got, err)
	}
}

func TestInvalidInputIsRejected(t *testing.T) {
	if _, err := Decide(time.Time{}, input()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero now error = %v, want %v", err, ErrInvalidInput)
	}
	negative := input()
	negative.Grace = -time.Second
	if _, err := Decide(expiry, negative); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative grace error = %v, want %v", err, ErrInvalidInput)
	}
	noExpiry := input()
	noExpiry.LeaseExpiresAt = time.Time{}
	if _, err := Decide(expiry, noExpiry); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing expiry error = %v, want %v", err, ErrInvalidInput)
	}
}
