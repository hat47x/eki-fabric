package process

// The Agent side of assignment delivery (MVP #57) across real process
// boundaries: eki-control serves the signed Lease and WorkOrder stored by the
// real PLC-1, LSE-1 and WOR-1 code, and eki-agent, authenticated by its own
// certificate, fetches them, verifies them against a trust anchor read from a
// file, claims the nonce in its durable replay database, passes the start
// gate, and records the decision in its events file.
//
// TEST PROFILE (/test-profile/v1/assignments), not the Worker Protocol (#198).
// The Agent does not execute Tasks: "ready" means verified, admitted and
// start-gated, and handed to an executor interface that does nothing yet.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	"github.com/hat47x/eki-fabric/internal/agent/service"
)

type assignmentEvent struct {
	AttemptID       string `json:"attempt_id"`
	Outcome         string `json:"outcome"`
	Reason          string `json:"reason"`
	CPUMillis       uint64 `json:"cpu_millis"`
	ExecutionStatus string `json:"execution_status"`
	ClaimDecision   string `json:"claim_decision"`
}

func readEvents(t *testing.T, path string) []assignmentEvent {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	must(t, err)
	defer f.Close()
	var out []assignmentEvent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev assignmentEvent
		must(t, json.Unmarshal(sc.Bytes(), &ev))
		out = append(out, ev)
	}
	return out
}

func eventFor(events []assignmentEvent, attempt string) (assignmentEvent, bool) {
	for _, ev := range events {
		if ev.AttemptID == attempt {
			return ev, true
		}
	}
	return assignmentEvent{}, false
}

// agentSide is one enrolled Worker with the trust anchor and configuration
// files of its Agent.
type agentSide struct {
	stateDir, worker string
	cfgPath          string
	events           string
	replayDB         string
}

func (e *mtlsEnv) assignmentAgent(t *testing.T, name string, auth *signedchain.Authorities, validatedAt time.Time, execution ...*service.ExecutionConfig) agentSide {
	t.Helper()
	stateDir, worker := e.enrollAgent(t, name)
	a := agentSide{
		stateDir: stateDir, worker: worker, cfgPath: filepath.Join(e.dir, name+"-agent.json"),
		events: filepath.Join(e.dir, name+"-events.jsonl"), replayDB: filepath.Join(e.dir, name+"-replay.db"),
	}
	anchor, err := json.Marshal(auth.TrustAnchor(validatedAt, "policy-1"))
	must(t, err)
	anchorPath := filepath.Join(e.dir, name+"-trust-anchor.json")
	must(t, os.WriteFile(anchorPath, anchor, 0o600))
	cfg := service.Config{
		ControlURL: e.workerURL, QueueDB: filepath.Join(e.dir, name+"-queue.db"), StateDir: stateDir,
		Assignments: &service.AssignmentConfig{
			TrustAnchorFile: anchorPath, ReplayDB: a.replayDB, EventsFile: a.events,
			ApprovedExecutors: []service.ExecutorApprovalConfig{{
				DecisionID:     signedchain.ExecutorApproval.DecisionID,
				PayloadDigest:  signedchain.ExecutorApproval.PayloadDigest.Hex(),
				ExecutorDigest: signedchain.ExecutorApproval.ExecutorDigest.Hex(),
			}},
			Machine: service.MachineConfig{CPUMillis: 8000, MemoryBytes: 8 << 30}, CeilingPercent: 50,
		},
	}
	if len(execution) > 0 {
		cfg.Assignments.Execution = execution[0]
	}
	raw, err := json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(a.cfgPath, raw, 0o600))
	return a
}

func (a agentSide) start(t *testing.T) *proc {
	t.Helper()
	return startProc(t, "eki-agent", "--config", a.cfgPath, "--interval", "100ms", "--retry-base", "100ms", "--retry-max", "500ms")
}

func TestAgentVerifiesAndGateStartsDeliveredAssignments(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)

	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 17)
	a := e.assignmentAgent(t, "a", auth, now)
	b := e.assignmentAgent(t, "b", auth, now)

	var good, expired, others signedchain.Placed
	func() {
		s := openState(t, e.controlDB)
		defer s.Close()
		policy, err := s.LoadCurrentPolicy(context.Background())
		must(t, err)
		good = auth.Place(t, s, policy, 1, a.worker, now)
		// A Lease the Control Plane signed ten minutes ago has run out. The
		// delivery listing drops it, so the Agent never sees it; the Agent's own
		// refusal of expired material is pinned in internal/agent/assignment.
		expired = auth.Place(t, s, policy, 2, a.worker, now.Add(-10*time.Minute))
		others = auth.Place(t, s, policy, 3, b.worker, now)

		listed, err := s.ListEmittedAssignments(context.Background(), a.worker, 0)
		must(t, err)
		if len(listed) != 1 || listed[0].AttemptID != good.AttemptID {
			t.Fatalf("A's delivery listing = %+v, want only the live assignment %s (not the expired %s)", listed, good.AttemptID, expired.AttemptID)
		}
	}()

	agentA := a.start(t)
	waitFor(t, "A decides its live assignment", func() (bool, string) {
		_, g := eventFor(readEvents(t, a.events), good.AttemptID.String())
		return g, agentA.out.String()
	})
	events := readEvents(t, a.events)
	if ev, _ := eventFor(events, good.AttemptID.String()); ev.Outcome != "ready" || ev.CPUMillis != signedchain.Limits.CPUMillis {
		t.Fatalf("the genuine assignment: %+v", ev)
	}
	if ev, found := eventFor(events, expired.AttemptID.String()); found {
		t.Fatalf("the expired Lease reached the Agent: %+v", ev)
	}
	// B's Attempt was never delivered to A.
	if _, found := eventFor(events, others.AttemptID.String()); found {
		t.Fatal("A's Agent received B's assignment")
	}

	// B's own Agent gets B's, and only B's.
	agentB := b.start(t)
	waitFor(t, "B decides its assignment", func() (bool, string) {
		_, ok := eventFor(readEvents(t, b.events), others.AttemptID.String())
		return ok, agentB.out.String()
	})
	for _, ev := range readEvents(t, b.events) {
		if ev.AttemptID != others.AttemptID.String() {
			t.Fatalf("B's Agent decided another Worker's Attempt: %+v", ev)
		}
	}

	// A restart with the same replay database sees the same bytes again and
	// does not start them a second time.
	agentA.kill()
	before := len(readEvents(t, a.events))
	restarted := a.start(t)
	waitFor(t, "the restarted Agent reports the replay", func() (bool, string) {
		ev := readEvents(t, a.events)
		for _, x := range ev[before:] {
			if x.AttemptID == good.AttemptID.String() && x.Outcome == "replayed" {
				return true, ""
			}
		}
		return false, restarted.out.String()
	})
	for _, x := range readEvents(t, a.events)[before:] {
		if x.Outcome == "ready" {
			t.Fatalf("a WorkOrder was started twice: %+v", x)
		}
	}
}

func TestAgentWithAnotherTrustAnchorRefusesGenuineAssignments(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)

	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 17)
	other := signedchain.New(t, now.Add(-2*time.Hour), 99) // keys the Control Plane never used
	a := e.assignmentAgent(t, "a", other, now)
	var placed signedchain.Placed
	func() {
		s := openState(t, e.controlDB)
		defer s.Close()
		policy, err := s.LoadCurrentPolicy(context.Background())
		must(t, err)
		placed = auth.Place(t, s, policy, 1, a.worker, now)
	}()
	agent := a.start(t)
	waitFor(t, "the Agent refuses the assignment", func() (bool, string) {
		_, ok := eventFor(readEvents(t, a.events), placed.AttemptID.String())
		return ok, agent.out.String()
	})
	if ev, _ := eventFor(readEvents(t, a.events), placed.AttemptID.String()); ev.Outcome != "rejected" || !strings.HasPrefix(ev.Reason, "lease_") {
		t.Fatalf("decision = %+v, want a rejected Lease", ev)
	}
}

func TestRevokedWorkersAgentReceivesNothing(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)

	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 17)
	a := e.assignmentAgent(t, "a", auth, now)
	func() {
		s := openState(t, e.controlDB)
		defer s.Close()
		policy, err := s.LoadCurrentPolicy(context.Background())
		must(t, err)
		auth.Place(t, s, policy, 1, a.worker, now)
	}()
	e.workerState(t, a.worker, "revoked")
	agent := a.start(t)
	waitFor(t, "the refused poll is logged", func() (bool, string) {
		return strings.Contains(agent.out.String(), "assignment poll failed"), agent.out.String()
	})
	if ev := readEvents(t, a.events); len(ev) != 0 {
		t.Fatalf("a revoked Worker's Agent decided assignments: %+v", ev)
	}
}

func TestAgentRefusesToStartWithoutATrustAnchor(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)

	now := time.Now().UTC()
	a := e.assignmentAgent(t, "a", signedchain.New(t, now.Add(-2*time.Hour), 17), now)
	must(t, os.Remove(filepath.Join(e.dir, "a-trust-anchor.json")))
	agent := a.start(t)
	select {
	case <-agent.exited:
	case <-time.After(20 * time.Second):
		t.Fatalf("the Agent kept running without a trust anchor:\n%s", agent.out.String())
	}
	if agent.cmd.ProcessState == nil || agent.cmd.ProcessState.ExitCode() == 0 || !strings.Contains(agent.out.String(), "trust anchor") {
		t.Fatalf("exit %v, output:\n%s", agent.cmd.ProcessState, agent.out.String())
	}
}
