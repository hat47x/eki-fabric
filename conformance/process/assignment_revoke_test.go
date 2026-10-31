package process

// The reclaim side of assignment delivery (ADR-0030 §2.2) across real process
// boundaries: eki-control, holding a delivery it already EMITTED for the
// Worker, revokes it in the State Store; the Worker's next poll answers with
// lease_state "revoked" and eki-agent refuses the assignment without running
// it.
//
// TEST PROFILE (/test-profile/v1/assignments), not the Worker Protocol (#198).

import (
	"context"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestAgentStopsOnARevokedAssignment(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)

	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 17)
	a := e.assignmentAgent(t, "a", auth, now)

	var placed signedchain.Placed
	func() {
		s := openState(t, e.controlDB)
		defer s.Close()
		policy, err := s.LoadCurrentPolicy(context.Background())
		must(t, err)
		placed = auth.Place(t, s, policy, 1, a.worker, now)
	}()

	// Before the revocation the Agent starts the assignment.
	agent := a.start(t)
	waitFor(t, "the Agent starts the live assignment", func() (bool, string) {
		ev, ok := eventFor(readEvents(t, a.events), placed.AttemptID.String())
		return ok && ev.Outcome == "ready", agent.out.String()
	})
	agent.kill()

	// The Control Plane reclaims the delivery. The emission rows stay
	// immutable; the revocation is its own delivery fact and the backing
	// Attempt closes as reclaimed in the same write.
	func() {
		s := openState(t, e.controlDB)
		defer s.Close()
		err := s.RevokeAssignment(context.Background(), statestore.AssignmentRevocation{
			LeaseID:     placed.LeaseID,
			CloseReason: attemptstate.CloseReasonReclaimed,
			Reason:      "process reclaim test",
			CommittedAt: time.Now().UTC(),
		})
		must(t, err)
	}()

	// The restarted Agent polls again, reads lease_state revoked and refuses
	// the very assignment it had started before — without starting it a
	// second time. Revocation is a fact of the listing, so the refusal does
	// not consult the signed bytes and repeats on every poll while the
	// revocation stays listed.
	restarted := a.start(t)
	defer restarted.kill()
	waitFor(t, "the Agent reports the revocation", func() (bool, string) {
		for _, ev := range readEvents(t, a.events) {
			if ev.AttemptID == placed.AttemptID.String() && ev.Outcome == "rejected" && ev.Reason == "lease_revoked" {
				return true, restarted.out.String()
			}
		}
		return false, restarted.out.String()
	})
	for _, ev := range readEvents(t, a.events) {
		if ev.AttemptID == placed.AttemptID.String() && ev.Outcome == "ready" && ev.Reason == "lease_revoked" {
			t.Fatalf("the revoked assignment was treated as fresh: %+v", ev)
		}
	}
}
