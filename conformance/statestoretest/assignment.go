package statestoretest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// AssignmentTestStore is a State Store that can emit a Lease and a WorkOrder
// and list the assignments of a Worker.
type AssignmentTestStore interface {
	WorkOrderSigningTestStore
	statestore.AssignmentStore

	// SetListingClock replaces the clock the listing compares Lease expiry
	// against, so the contract runs on the fixture clock instead of the wall
	// clock.
	SetListingClock(now func() time.Time)
}

type AssignmentFactory func(t testing.TB) AssignmentTestStore

// listingClockOffset places the listing clock inside the fixture Leases'
// validity windows, which open at leaseNow.
const listingClockOffset = 10 * time.Second

func newListingStore(t testing.TB, newStore AssignmentFactory) AssignmentTestStore {
	t.Helper()
	store := newStore(t)
	store.SetListingClock(func() time.Time { return leaseNow.Add(listingClockOffset) })
	return store
}

// RunAssignmentContract checks the read the Worker delivery route relies on:
// the exact EMITTED bytes for the Worker that holds the Attempt, nothing for
// anyone else, nothing before both records are EMITTED, and the same answer
// when asked again.
func RunAssignmentContract(t *testing.T, newStore AssignmentFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("lists-the-exact-emitted-bytes-for-the-holder-only", func(t *testing.T) {
		store := newListingStore(t, newStore)
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
		store := newListingStore(t, newStore)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("asg-no-order")
		if got, err := store.ListEmittedAssignments(ctx, lease.WorkerIdentity, 0); err != nil || len(got) != 0 {
			t.Fatalf("ListEmittedAssignments() = %+v, %v, want nothing before the WorkOrder is emitted", got, err)
		}
	})

	t.Run("a-reserved-but-unsigned-lease-is-not-listed", func(t *testing.T) {
		store := newListingStore(t, newStore)
		fx := newWorkOrderFixture(t, store)
		lease := fx.lse.reserve("asg-reserved")
		if got, err := store.ListEmittedAssignments(ctx, lease.WorkerIdentity, 0); err != nil || len(got) != 0 {
			t.Fatalf("ListEmittedAssignments() = %+v, %v, want nothing for a RESERVED Lease", got, err)
		}
	})

	// The expiry filter is pinned by RunAssignmentExpiryProbe, which moves the
	// listing clock instead of waiting on the wall clock.

	t.Run("a-dispatched-attempt-is-still-listed-and-a-closed-one-is-not", func(t *testing.T) {
		store := newListingStore(t, newStore)
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

// RunAssignmentExpiryProbe pins the expiry filter both adapters must apply:
// a Lease whose validity window has closed is not listed, because the Worker
// refuses expired material anyway and a stale row would crowd out live ones.
// The probe moves the listing clock across the stored ExpiresAt, so it runs
// in no wall-clock time and does not depend on how long the other contract
// subtests take.
func RunAssignmentExpiryProbe(t *testing.T, newStore AssignmentFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("a-lease-is-listed-until-its-window-closes", func(t *testing.T) {
		store := newListingStore(t, newStore)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("asg-expiry")
		if _, err := fx.issuer.Issue(ctx, fx.request(lease)); err != nil {
			t.Fatal(err)
		}
		stored, err := store.LoadLease(ctx, lease.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name string
			at   time.Time
			want int
		}{
			{"while the window is open", leaseNow.Add(listingClockOffset), 1},
			{"one nanosecond before ExpiresAt", stored.ExpiresAt.Add(-time.Nanosecond), 1},
			{"at ExpiresAt", stored.ExpiresAt, 0},
			{"long after ExpiresAt", stored.ExpiresAt.Add(time.Hour), 0},
		} {
			at := tc.at
			store.SetListingClock(func() time.Time { return at })
			got, err := store.ListEmittedAssignments(ctx, lease.WorkerIdentity, 0)
			if err != nil || len(got) != tc.want {
				t.Fatalf("%s: ListEmittedAssignments() = %+v, %v, want %d", tc.name, got, err, tc.want)
			}
		}
	})
}
