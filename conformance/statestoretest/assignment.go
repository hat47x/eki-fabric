package statestoretest

import (
	"bytes"
	"context"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// AssignmentTestStore is a State Store that can emit a Lease and a WorkOrder
// and list the assignments of a Worker.
type AssignmentTestStore interface {
	WorkOrderSigningTestStore
	statestore.AssignmentStore
}

type AssignmentFactory func(t testing.TB) AssignmentTestStore

// RunAssignmentContract checks the read the Worker delivery route relies on:
// the exact EMITTED bytes for the Worker that holds the Attempt, nothing for
// anyone else, nothing before both records are EMITTED, and the same answer
// when asked again.
func RunAssignmentContract(t *testing.T, newStore AssignmentFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("lists-the-exact-emitted-bytes-for-the-holder-only", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, leaseResult := fx.emittedLease("asg-happy")
		order, err := fx.issuer.Issue(ctx, fx.request(lease))
		if err != nil {
			t.Fatalf("Issue() error = %v", err)
		}

		got, err := store.ListEmittedAssignments(ctx, lease.WorkerIdentity, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].LeaseID != lease.ID || got[0].AttemptID != lease.AttemptID ||
			!bytes.Equal(got[0].Lease.SignedEnvelope(), leaseResult.SignedLease) ||
			!bytes.Equal(got[0].WorkOrder.SignedEnvelope(), order.SignedWorkOrder) {
			t.Fatalf("ListEmittedAssignments(%q) = %+v, want the stored bytes of %s", lease.WorkerIdentity, got, lease.ID)
		}
		// Nothing is marked delivered: asking again returns the same bytes.
		again, err := store.ListEmittedAssignments(ctx, lease.WorkerIdentity, 0)
		if err != nil || len(again) != 1 || !bytes.Equal(again[0].WorkOrder.SignedEnvelope(), order.SignedWorkOrder) {
			t.Fatalf("second listing = %+v, %v", again, err)
		}
		for _, other := range []string{"worker-other", "", lease.WorkerIdentity + "x"} {
			if got, err := store.ListEmittedAssignments(ctx, other, 0); err != nil || len(got) != 0 {
				t.Fatalf("ListEmittedAssignments(%q) = %+v, %v, want nothing", other, got, err)
			}
		}
	})

	t.Run("a-lease-without-a-workorder-is-not-listed", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("asg-no-order")
		if got, err := store.ListEmittedAssignments(ctx, lease.WorkerIdentity, 0); err != nil || len(got) != 0 {
			t.Fatalf("ListEmittedAssignments() = %+v, %v, want nothing before the WorkOrder is emitted", got, err)
		}
	})

	t.Run("a-reserved-but-unsigned-lease-is-not-listed", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease := fx.lse.reserve("asg-reserved")
		if got, err := store.ListEmittedAssignments(ctx, lease.WorkerIdentity, 0); err != nil || len(got) != 0 {
			t.Fatalf("ListEmittedAssignments() = %+v, %v, want nothing for a RESERVED Lease", got, err)
		}
	})

	t.Run("a-dispatched-attempt-is-still-listed-and-a-closed-one-is-not", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("asg-phases")
		if _, err := fx.issuer.Issue(ctx, fx.request(lease)); err != nil {
			t.Fatal(err)
		}
		attempt, err := store.LoadAttempt(ctx, lease.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		dispatched := attempt
		dispatched.Phase = attemptstate.AttemptPhaseDispatched
		dispatched.Revision++
		dispatched.UpdatedAt = attempt.UpdatedAt.Add(1)
		if err := store.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, dispatched); err != nil {
			t.Fatal(err)
		}
		if got, err := store.ListEmittedAssignments(ctx, lease.WorkerIdentity, 0); err != nil || len(got) != 1 {
			t.Fatalf("dispatched: ListEmittedAssignments() = %+v, %v, want 1", got, err)
		}
		closed := dispatched
		closed.Phase, closed.CloseReason = attemptstate.AttemptPhaseClosed, attemptstate.CloseReasonWorkerLost
		closed.Revision++
		closed.UpdatedAt = dispatched.UpdatedAt.Add(1)
		if err := store.CompareAndSwapAttempt(ctx, attempt.ID, dispatched.Revision, closed); err != nil {
			t.Fatal(err)
		}
		if got, err := store.ListEmittedAssignments(ctx, lease.WorkerIdentity, 0); err != nil || len(got) != 0 {
			t.Fatalf("closed: ListEmittedAssignments() = %+v, %v, want nothing", got, err)
		}
	})

}
