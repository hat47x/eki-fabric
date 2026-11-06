package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/claimreconcile"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// RecallGrace is how long the Control Plane waits after a Lease expires before
// concluding that no ResultClaim is coming (claimreconcile). It is short because
// the Worker has already lost the Lease: the Attempt cannot be handed on until it
// is closed, so a long wait holds the Task open for no benefit.
const RecallGrace = 30 * time.Second

// recallCloser is the reclaimer of the recall loop. It closes the Attempt of an
// expired Lease when no ResultClaim arrived, and then removes the Lease from the
// registry (#745).
//
// The two belong together. An Attempt closed while its Lease stays in the
// registry is swept again next minute, and a Lease removed while its Attempt
// stays open is forgotten: nobody would ever close it. Closing first also means a
// failed close leaves the registry entry in place, which reads as "still open" —
// the safe direction.
type recallCloser struct {
	attempts  statestore.AttemptStore
	claims    statestore.ResultClaimStore
	reclaimer interface {
		Reclaim(ctx context.Context, lease leasestate.LeaseRecord, reason string) error
	}
	grace time.Duration
	now   func() time.Time
}

// Reclaim performs the Attempt transition and the registry removal for one
// expired Lease.
func (c recallCloser) Reclaim(ctx context.Context, lease leasestate.LeaseRecord, reason string) error {
	attempt, err := c.attempts.LoadAttempt(ctx, lease.AttemptID)
	if err != nil {
		return fmt.Errorf("recall: load attempt %s: %w", lease.AttemptID, err)
	}
	hasClaim := false
	if attempt.Phase != attemptstate.AttemptPhaseClosed {
		_, hasClaim, err = c.claims.LoadResultClaim(ctx, lease.AttemptID)
		if err != nil {
			return fmt.Errorf("recall: claim of %s: %w", lease.AttemptID, err)
		}
	}
	decision, err := CloseDecision(attempt, hasClaim, lease, c.now(), c.grace)
	if err != nil {
		return err
	}
	if decision != claimreconcile.CloseLeaseExpired {
		// AwaitClaim keeps the entry for the next sweep. Reconcile keeps it too:
		// an Attempt that may have had an external effect stays open until a
		// human decides, and its Lease must stay visible while it is open.
		return nil
	}
	next := attempt
	next.Phase = attemptstate.AttemptPhaseClosed
	next.CloseReason = attemptstate.CloseReasonLeaseExpired
	next.Revision = attempt.Revision + 1
	next.UpdatedAt = c.now().UTC()
	if err := c.attempts.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, next); err != nil {
		return fmt.Errorf("recall: close %s: %w", attempt.ID, err)
	}
	return c.reclaimer.Reclaim(ctx, lease, reason)
}

// CloseDecision is the claimreconcile decision for one Attempt. It is exported so
// the reading can be tested without storage.
func CloseDecision(attempt attemptstate.AttemptRecord, hasClaim bool, lease leasestate.LeaseRecord, now time.Time, grace time.Duration) (claimreconcile.Decision, error) {
	if attempt.Phase == attemptstate.AttemptPhaseClosed {
		return claimreconcile.NoAction, nil
	}
	return claimreconcile.Decide(now, claimreconcile.Input{
		AttemptOpen:    true,
		HasClaim:       hasClaim,
		LeaseExpiresAt: lease.ExpiresAt,
		Grace:          grace,
		// No Task model carries an external-effect marker yet (#745 の未決).
		// Every expired Attempt is therefore closed as retryable, and the
		// recovery admission decides whether the Task runs again.
		PossibleExternalEffect: false,
	})
}

var errRecallStoreMissing = errors.New("recall: the state store is required")

func (c recallCloser) validate() error {
	if c.attempts == nil || c.claims == nil || c.reclaimer == nil {
		return errRecallStoreMissing
	}
	if c.grace < 0 {
		return fmt.Errorf("recall: grace must not be negative")
	}
	return nil
}
