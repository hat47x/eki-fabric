package statestoretest

import (
	"context"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// HeartbeatTestStore is a State Store that can also record the running-Lease
// reports of a polling Worker (ADR-0030 §2.2), read the newest report back,
// and revoke a delivery.
type HeartbeatTestStore interface {
	AssignmentTestStore
	statestore.AssignmentHeartbeatStore
	// LatestHeartbeat returns the newest accepted report of one Lease.
	LatestHeartbeat(ctx context.Context, leaseID ids.LeaseID) (statestore.Heartbeat, error)
	statestore.AssignmentRevokeStore
}

// HeartbeatFactory builds one HeartbeatTestStore per subtest.
type HeartbeatFactory func(t testing.TB) HeartbeatTestStore

// newHeartbeatStore builds a heartbeat store whose listing clock sits inside
// the fixture Leases' validity windows.
func newHeartbeatStore(t testing.TB, newStore HeartbeatFactory) HeartbeatTestStore {
	t.Helper()
	store := newStore(t)
	if c, ok := store.(interface{ SetListingClock(func() time.Time) }); ok {
		c.SetListingClock(func() time.Time { return leaseNow.Add(listingClockOffset) })
	}
	return store
}

// heartbeatAt is the recorded time the contract reports at: inside the
// fixture Leases' windows.
var heartbeatAt = leaseNow.Add(listingClockOffset)

// RunAssignmentHeartbeatContract pins the Worker's heartbeat (ADR-0030 §2.2):
// the report of a Lease the Worker is executing is recorded as a liveness
// fact, only the newest report per Lease is kept, a report about a Lease the
// Worker does not hold is refused, and a report never changes the Attempt.
func RunAssignmentHeartbeatContract(t *testing.T, newStore HeartbeatFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("a-running-lease-report-is-recorded", func(t *testing.T) {
		store := newHeartbeatStore(t, newStore)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("hb-live")
		if _, err := fx.issuer.Issue(ctx, fx.request(lease)); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordHeartbeats(ctx, lease.WorkerIdentity, []string{lease.ID.String()}, heartbeatAt); err != nil {
			t.Fatalf("RecordHeartbeats() error = %v", err)
		}
		got, err := store.LatestHeartbeat(ctx, lease.ID)
		if err != nil {
			t.Fatalf("LatestHeartbeat() error = %v", err)
		}
		if got.LeaseID != lease.ID || got.WorkerIdentity != lease.WorkerIdentity || !got.RecordedAt.Equal(heartbeatAt) {
			t.Fatalf("heartbeat = %+v, want Lease %s of %s at %s", got, lease.ID, lease.WorkerIdentity, heartbeatAt)
		}
		if got.AttemptID != lease.AttemptID {
			t.Fatalf("heartbeat names Attempt %s, want %s", got.AttemptID, lease.AttemptID)
		}
		// A heartbeat is an observation: the Attempt keeps its phase.
		attempt, err := store.LoadAttempt(ctx, lease.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.Phase != attemptstate.AttemptPhaseAssigned {
			t.Fatalf("after heartbeat: Attempt phase = %s, want assigned", attempt.Phase)
		}
	})

	t.Run("only-the-newest-report-per-lease-is-kept", func(t *testing.T) {
		store := newHeartbeatStore(t, newStore)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("hb-latest")
		if _, err := fx.issuer.Issue(ctx, fx.request(lease)); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordHeartbeats(ctx, lease.WorkerIdentity, []string{lease.ID.String()}, heartbeatAt); err != nil {
			t.Fatal(err)
		}
		later := heartbeatAt.Add(time.Second)
		if err := store.RecordHeartbeats(ctx, lease.WorkerIdentity, []string{lease.ID.String()}, later); err != nil {
			t.Fatal(err)
		}
		got, err := store.LatestHeartbeat(ctx, lease.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.RecordedAt.Equal(later) || got.Revision != 2 {
			t.Fatalf("after two beats: %+v, want recorded at %s revision 2", got, later)
		}
		// An older report arriving late is a replay and changes nothing.
		if err := store.RecordHeartbeats(ctx, lease.WorkerIdentity, []string{lease.ID.String()}, heartbeatAt); err != nil {
			t.Fatalf("replayed beat refused: %v", err)
		}
		got, err = store.LatestHeartbeat(ctx, lease.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.RecordedAt.Equal(later) || got.Revision != 2 {
			t.Fatalf("after a late older beat: %+v, want the newest report unchanged", got)
		}
	})

	t.Run("a-report-about-a-lease-the-worker-does-not-hold-is-refused", func(t *testing.T) {
		store := newHeartbeatStore(t, newStore)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("hb-owned")
		if _, err := fx.issuer.Issue(ctx, fx.request(lease)); err != nil {
			t.Fatal(err)
		}
		for name, tc := range map[string]struct {
			worker string
			id     string
		}{
			"another worker's lease": {worker: lease.WorkerIdentity + "-other", id: lease.ID.String()},
			"an unknown lease id":    {worker: lease.WorkerIdentity, id: "lease-does-not-exist"},
		} {
			if err := store.RecordHeartbeats(ctx, tc.worker, []string{tc.id}, heartbeatAt); err == nil {
				t.Fatalf("%s: RecordHeartbeats() was accepted, want refusal", name)
			}
		}
		if _, err := store.LatestHeartbeat(ctx, lease.ID); err == nil {
			t.Fatal("LatestHeartbeat() found a beat that was never accepted")
		}
	})

	t.Run("a-revoked-lease-stops-carrying-liveness", func(t *testing.T) {
		store := newHeartbeatStore(t, newStore)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("hb-revoked")
		if _, err := fx.issuer.Issue(ctx, fx.request(lease)); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordHeartbeats(ctx, lease.WorkerIdentity, []string{lease.ID.String()}, heartbeatAt); err != nil {
			t.Fatal(err)
		}
		// The emission is aborted; the Worker can no longer report liveness
		// for the Lease it was running, and the recorded fact stays as it
		// was: the last beat before the revocation.
		if err := store.RevokeAssignment(ctx, statestore.AssignmentRevocation{
			LeaseID: lease.ID, CloseReason: attemptstate.CloseReasonReclaimed,
			Reason: "reclaim before expiry", CommittedAt: heartbeatAt,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordHeartbeats(ctx, lease.WorkerIdentity, []string{lease.ID.String()}, heartbeatAt.Add(time.Second)); err == nil {
			t.Fatal("a heartbeat after revocation was accepted, want refusal")
		}
		got, err := store.LatestHeartbeat(ctx, lease.ID)
		if err != nil {
			t.Fatalf("the last beat before the revocation vanished: %v", err)
		}
		if !got.RecordedAt.Equal(heartbeatAt) {
			t.Fatalf("after revocation: %+v, want the last beat before it", got)
		}
	})

	t.Run("a-report-without-a-recorded-time-is-refused", func(t *testing.T) {
		store := newHeartbeatStore(t, newStore)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("hb-no-time")
		if _, err := fx.issuer.Issue(ctx, fx.request(lease)); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordHeartbeats(ctx, lease.WorkerIdentity, []string{lease.ID.String()}, time.Time{}); err == nil {
			t.Fatal("a report without a recorded time was accepted")
		}
		if _, err := store.LatestHeartbeat(ctx, lease.ID); err == nil {
			t.Fatal("a report without a recorded time was stored")
		}
	})

	t.Run("an-empty-report-changes-nothing", func(t *testing.T) {
		store := newHeartbeatStore(t, newStore)
		if err := store.RecordHeartbeats(ctx, "worker-without-running-leases", nil, heartbeatAt); err != nil {
			t.Fatalf("empty report error = %v", err)
		}
	})
}
