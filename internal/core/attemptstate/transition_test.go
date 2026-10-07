package attemptstate

import (
	"errors"
	"testing"
	"time"
)

func TestAttemptTransitionLifecycle(t *testing.T) {
	current := validAttempt(t)

	placing := current
	placing.Phase = AttemptPhasePlacing
	placing.WorkerIdentity = "worker-a"
	placing.Revision++
	placing.UpdatedAt = placing.UpdatedAt.Add(time.Second)
	mustTransition(t, current, placing)

	replanned := placing
	replanned.WorkerIdentity = "worker-b"
	replanned.Revision++
	replanned.UpdatedAt = replanned.UpdatedAt.Add(time.Second)
	mustTransition(t, placing, replanned)

	assigned := replanned
	assigned.Phase = AttemptPhaseAssigned
	assigned.LeaseID = mustLeaseID(t)
	assigned.Revision++
	assigned.UpdatedAt = assigned.UpdatedAt.Add(time.Second)
	mustTransition(t, replanned, assigned)

	dispatched := assigned
	dispatched.Phase = AttemptPhaseDispatched
	dispatched.Revision++
	dispatched.UpdatedAt = dispatched.UpdatedAt.Add(time.Second)
	mustTransition(t, assigned, dispatched)

	claimed := dispatched
	claimed.Phase = AttemptPhaseClaimed
	claimed.Revision++
	claimed.UpdatedAt = claimed.UpdatedAt.Add(time.Second)
	mustTransition(t, dispatched, claimed)

	verifying := claimed
	verifying.Phase = AttemptPhaseVerifying
	verifying.Revision++
	verifying.UpdatedAt = verifying.UpdatedAt.Add(time.Second)
	mustTransition(t, claimed, verifying)

	closed := verifying
	closed.Phase = AttemptPhaseClosed
	closed.CloseReason = CloseReasonResultAccepted
	closed.Revision++
	closed.UpdatedAt = closed.UpdatedAt.Add(time.Second)
	mustTransition(t, verifying, closed)

	reopen := closed
	reopen.Phase = AttemptPhaseAssigned
	reopen.CloseReason = CloseReasonNone
	reopen.Revision++
	reopen.UpdatedAt = reopen.UpdatedAt.Add(time.Second)
	if err := ValidateTransition(closed, reopen); !errors.Is(err, ErrInvalidAttemptTransition) {
		t.Fatalf("closed reopen error = %v, want ErrInvalidAttemptTransition", err)
	}
}

func TestAttemptTransitionRejectsAuthorityRebinding(t *testing.T) {
	placing := validAttempt(t)
	placing.Phase = AttemptPhasePlacing
	placing.WorkerIdentity = "worker-a"

	assigned := placing
	assigned.Phase = AttemptPhaseAssigned
	assigned.LeaseID = mustLeaseID(t)
	assigned.Revision++
	assigned.UpdatedAt = assigned.UpdatedAt.Add(time.Second)
	mustTransition(t, placing, assigned)

	t.Run("worker while assigning", func(t *testing.T) {
		next := placing
		next.Phase = AttemptPhaseAssigned
		next.WorkerIdentity = "worker-b"
		next.LeaseID = mustLeaseID(t)
		next.Revision++
		next.UpdatedAt = next.UpdatedAt.Add(time.Second)
		if err := ValidateTransition(placing, next); !errors.Is(err, ErrInvalidAttemptTransition) {
			t.Fatalf("error = %v, want ErrInvalidAttemptTransition", err)
		}
	})

	t.Run("worker after assignment", func(t *testing.T) {
		next := assigned
		next.Phase = AttemptPhaseDispatched
		next.WorkerIdentity = "worker-b"
		next.Revision++
		next.UpdatedAt = next.UpdatedAt.Add(time.Second)
		if err := ValidateTransition(assigned, next); !errors.Is(err, ErrInvalidAttemptTransition) {
			t.Fatalf("error = %v, want ErrInvalidAttemptTransition", err)
		}
	})

	t.Run("lease after assignment", func(t *testing.T) {
		next := assigned
		next.Phase = AttemptPhaseDispatched
		next.LeaseID = "les_00000000000000000000000000000002"
		next.Revision++
		next.UpdatedAt = next.UpdatedAt.Add(time.Second)
		if err := ValidateTransition(assigned, next); !errors.Is(err, ErrInvalidAttemptTransition) {
			t.Fatalf("error = %v, want ErrInvalidAttemptTransition", err)
		}
	})

	t.Run("skip assignment", func(t *testing.T) {
		next := placing
		next.Phase = AttemptPhaseDispatched
		next.LeaseID = mustLeaseID(t)
		next.Revision++
		next.UpdatedAt = next.UpdatedAt.Add(time.Second)
		if err := ValidateTransition(placing, next); !errors.Is(err, ErrInvalidAttemptTransition) {
			t.Fatalf("error = %v, want ErrInvalidAttemptTransition", err)
		}
	})
}

func mustTransition(t testing.TB, current, next AttemptRecord) {
	t.Helper()
	if err := ValidateTransition(current, next); err != nil {
		t.Fatalf("ValidateTransition(%s -> %s) error = %v", current.Phase, next.Phase, err)
	}
}
