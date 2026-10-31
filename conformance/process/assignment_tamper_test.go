package process

// An assignment that no trusted authority signed must never reach the start
// gate. The delivery listing hands out whatever the emission records store, so
// the test plants an assignment signed by a foreign authority set (the emission
// rows themselves are immutable — the store rejects updates — so tampering is
// simulated at signing time, not by rewriting bytes) and requires the Agent to
// refuse it without consuming its nonce (ADR-0030 §2.3).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
)

func TestAgentDoesNotStartAnUntrustedAssignment(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)

	now := time.Now().UTC()
	// The authority the Agent's trust anchor pins, and a second, structurally
	// identical authority with different keys.
	auth := signedchain.New(t, now.Add(-2*time.Hour), 17)
	foreign := signedchain.New(t, now.Add(-2*time.Hour), 23)
	a := e.assignmentAgent(t, "a", auth, now)

	var planted signedchain.Placed
	func() {
		s := openState(t, e.controlDB)
		defer s.Close()
		policy, err := s.LoadCurrentPolicy(context.Background())
		must(t, err)
		planted = foreign.Place(t, s, policy, 4, a.worker, now)
	}()

	agentA := a.start(t)
	waitFor(t, "A decides on the untrusted assignment", func() (bool, string) {
		_, ok := eventFor(readEvents(t, a.events), planted.AttemptID.String())
		return ok, agentA.out.String()
	})
	ev, _ := eventFor(readEvents(t, a.events), planted.AttemptID.String())
	if ev.Outcome != "rejected" {
		t.Fatalf("an assignment signed by a foreign authority did not stop at verification: %+v", ev)
	}
	if !strings.HasPrefix(ev.Reason, "lease_") && !strings.HasPrefix(ev.Reason, "work_order_") {
		t.Fatalf("rejection reason %q names neither the Lease nor the WorkOrder check", ev.Reason)
	}
	// The nonce is unspent: the same bytes re-poll as rejected again rather than
	// replaying as a decided assignment.
	waitFor(t, "A keeps refusing the untrusted assignment", func() (bool, string) {
		count := 0
		for _, e := range readEvents(t, a.events) {
			if e.AttemptID == planted.AttemptID.String() {
				count++
			}
		}
		return count >= 2, agentA.out.String()
	})
}
