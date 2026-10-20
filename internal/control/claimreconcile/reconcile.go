// Package claimreconcile decides what the Control Plane does when an Attempt
// ends without a ResultClaim.
//
// A missing claim is not a failure of the Task by itself. The Worker may still
// be running after its Lease expired, or the claim may still be in the Agent's
// queue. The Control Plane waits out a grace period, then either closes the
// Attempt as lease-expired (safe for retryable work) or keeps it in
// reconciliation (when an external effect may have happened and no
// authoritative receipt exists).
package claimreconcile

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidInput = errors.New("invalid claim reconciliation input")

// Decision is what the Control Plane should do for one Attempt without a claim.
type Decision string

const (
	// NoAction: the Attempt is closed, or a claim exists. Nothing to reconcile.
	NoAction Decision = "no_action"
	// AwaitClaim: the Lease expired, but the grace period has not passed.
	AwaitClaim Decision = "await_claim"
	// CloseLeaseExpired: close the Attempt as lease_expired. Recovery decides
	// whether a new Attempt may start.
	CloseLeaseExpired Decision = "close_lease_expired"
	// Reconcile: an external effect may have happened. Keep the Attempt open
	// until an authoritative receipt or a human decision resolves it.
	Reconcile Decision = "reconcile"
)

// Input is read from durable state in the same operation that applies the
// decision.
type Input struct {
	AttemptOpen bool
	// HasClaim is true when any claim, including a stale one, is recorded.
	HasClaim bool
	// LeaseExpiresAt is the current Lease's expiry for this Attempt.
	LeaseExpiresAt time.Time
	// Grace is how long the Control Plane waits after the Lease expiry before
	// concluding that no claim is coming.
	Grace time.Duration
	// PossibleExternalEffect is true when the Task may have changed something
	// outside EKI. Without an authoritative receipt, such an Attempt cannot be
	// closed as retryable.
	PossibleExternalEffect bool
}

// Decide returns the action for one Attempt at now.
func Decide(now time.Time, input Input) (Decision, error) {
	if now.IsZero() {
		return "", fmt.Errorf("%w: now is required", ErrInvalidInput)
	}
	if input.Grace < 0 {
		return "", fmt.Errorf("%w: grace must not be negative", ErrInvalidInput)
	}
	if !input.AttemptOpen || input.HasClaim {
		return NoAction, nil
	}
	if input.LeaseExpiresAt.IsZero() {
		return "", fmt.Errorf("%w: Lease expiry is required for an open Attempt", ErrInvalidInput)
	}
	now = now.UTC()
	if now.Before(input.LeaseExpiresAt.UTC().Add(input.Grace)) {
		return AwaitClaim, nil
	}
	if input.PossibleExternalEffect {
		return Reconcile, nil
	}
	return CloseLeaseExpired, nil
}
