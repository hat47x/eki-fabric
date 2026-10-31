package process

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	"github.com/hat47x/eki-fabric/internal/agent/service"
	"github.com/hat47x/eki-fabric/internal/control/resultacceptance"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
)

func TestAgentExecutesAndSubmitsResultClaim(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	control := e.startControl(t)

	oldLimits, oldSandbox := signedchain.Limits, signedchain.Sandbox
	signedchain.Limits = decisionpayload.ResourceLimits{
		CPUMillis: 1000, MemoryBytes: 1 << 28, WallTimeMillis: 300_000, OutputBytesMaximum: 1 << 20,
	}
	signedchain.Sandbox = decisionpayload.SandboxPolicy{Profile: "native-fixed-spike"}
	defer func() { signedchain.Limits, signedchain.Sandbox = oldLimits, oldSandbox }()

	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 23)
	command, args := "/bin/echo", []string{"hello"}
	if runtime.GOOS == "windows" {
		command, args = "cmd.exe", []string{"/c", "echo", "hello"}
	}
	a := e.assignmentAgent(t, "exec", auth, now, &service.ExecutionConfig{
		Command: command,
		Args:    args,
	})

	var placed signedchain.Placed
	func() {
		s := openState(t, e.controlDB)
		defer s.Close()
		policy, err := s.LoadCurrentPolicy(context.Background())
		must(t, err)
		placed = auth.Place(t, s, policy, 1, a.worker, now)
		attempt, err := s.LoadAttempt(context.Background(), placed.AttemptID)
		must(t, err)
		dispatched := attempt
		dispatched.Phase, dispatched.UpdatedAt = attemptstate.AttemptPhaseDispatched, now
		dispatched.Revision++
		must(t, s.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, dispatched))
	}()

	agent := a.start(t)
	waitFor(t, "execution and result claim", func() (bool, string) {
		executed, claimed := false, false
		for _, ev := range readEvents(t, a.events) {
			if ev.AttemptID != placed.AttemptID.String() {
				continue
			}
			if ev.ExecutionStatus == "completed" {
				executed = true
			}
			if ev.ClaimDecision != "" {
				claimed = true
			}
		}
		return executed && claimed, agent.out.String() + "\n" + control.out.String()
	})

	store := openState(t, e.controlDB)
	defer store.Close()
	claim, found, err := store.LoadResultClaim(context.Background(), placed.AttemptID)
	must(t, err)
	if !found {
		t.Fatal("claim was not stored")
	}
	policy := resultaccept.Policy{
		Level: resultaccept.LevelRA2, Threshold: 2,
		DistinctDimensions: []resultaccept.Dimension{resultaccept.DimensionPrincipal, resultaccept.DimensionOperator},
	}
	producers := fixedProducers{executorDigest: claim.ExecutorDigest().Hex()}
	service, err := resultacceptance.New(resultacceptance.Config{
		Attempts: store, Tasks: store, Claims: store, Results: store, Rejections: store,
		Policies:  fixedPolicy{policy: resultacceptance.ResultPolicy{Policy: policy, Output: resultaccept.OutputMechanical}},
		Producers: producers, Clock: systemClock{},
	})
	must(t, err)
	verification := func(id, principal, operator string) resultaccept.Verification {
		return resultaccept.Verification{
			VerifierID: id, TaskID: claim.TaskID().String(), AttemptID: claim.AttemptID().String(),
			Check: resultaccept.CheckReproduced, ObservedDigest: claim.OutputDigest().Hex(),
			Dimensions: map[resultaccept.Dimension]string{
				resultaccept.DimensionPrincipal: principal, resultaccept.DimensionOperator: operator,
				resultaccept.DimensionExecutorDigest: contract.SHA256([]byte("verifier-" + id)).Hex(),
			},
		}
	}
	result, err := service.Accept(context.Background(), resultacceptance.Request{
		AttemptID: placed.AttemptID,
		Verifications: []resultaccept.Verification{
			verification("verifier-1", "p-1", "o-1"),
			verification("verifier-2", "p-2", "o-2"),
		},
	})
	must(t, err)
	if result.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result outcome = %s, want accepted: %+v", result.Outcome(), result)
	}
}

// The claim from a finished execution is queued on the Agent before any send.
// With the Control Plane down at that moment, and the Agent restarted too, the
// claim still reaches the Control Plane once it is back, and events_file tells
// the failed tries from the delivery.
func TestAgentKeepsExecutionClaimWhileControlIsDownAcrossAgentRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixed command is a POSIX shell script")
	}
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	control := e.startControl(t)

	oldLimits, oldSandbox := signedchain.Limits, signedchain.Sandbox
	signedchain.Limits = decisionpayload.ResourceLimits{
		CPUMillis: 1000, MemoryBytes: 1 << 28, WallTimeMillis: 300_000, OutputBytesMaximum: 1 << 20,
	}
	signedchain.Sandbox = decisionpayload.SandboxPolicy{Profile: "native-fixed-spike"}
	defer func() { signedchain.Limits, signedchain.Sandbox = oldLimits, oldSandbox }()

	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 29)
	started := filepath.Join(t.TempDir(), "started")
	a := e.assignmentAgent(t, "keep", auth, now, &service.ExecutionConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo go > '" + started + "'; sleep 2; echo hello"},
		Env:     []string{"PATH=/usr/bin:/bin"},
	})

	var placed signedchain.Placed
	func() {
		s := openState(t, e.controlDB)
		defer s.Close()
		policy, err := s.LoadCurrentPolicy(context.Background())
		must(t, err)
		placed = auth.Place(t, s, policy, 1, a.worker, now)
		attempt, err := s.LoadAttempt(context.Background(), placed.AttemptID)
		must(t, err)
		dispatched := attempt
		dispatched.Phase, dispatched.UpdatedAt = attemptstate.AttemptPhaseDispatched, now
		dispatched.Revision++
		must(t, s.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, dispatched))
	}()

	agent := a.start(t)
	waitFor(t, "the fixed command to start", func() (bool, string) {
		_, err := os.Stat(started)
		return err == nil, agent.out.String()
	})
	// The command is still sleeping: the Control Plane goes away before the
	// claim exists.
	control.kill()

	type seen struct{ Failure, Decision string }
	outcomes := func() map[string]seen {
		got := map[string]seen{}
		for _, ev := range readEvents(t, a.events) {
			if ev.AttemptID == placed.AttemptID.String() {
				got[ev.Outcome] = seen{Failure: ev.Failure, Decision: ev.ClaimDecision}
			}
		}
		return got
	}
	waitFor(t, "claim queued and a failed delivery recorded", func() (bool, string) {
		got := outcomes()
		_, queued := got["result_claim_queued"]
		failed, ok := got["result_claim_delivery_failed"]
		return queued && ok && failed.Failure != "", agent.out.String()
	})
	if _, delivered := outcomes()["result_claim_submitted"]; delivered {
		t.Fatal("a delivery was recorded while the Control Plane was down")
	}
	if v := attemptView(t, e.controlDB, placed.AttemptID); v.phase != attemptstate.AttemptPhaseDispatched || v.claimed {
		t.Fatalf("control changed without a delivery: %+v", v)
	}

	// The Agent dies with the claim only in its queue; both come back.
	agent.kill()
	e.startControl(t)
	a.start(t)
	waitFor(t, "the queued claim delivered after both restarts", func() (bool, string) {
		got := outcomes()
		return got["result_claim_submitted"].Decision != "", ""
	})
	if v := attemptView(t, e.controlDB, placed.AttemptID); v.phase != attemptstate.AttemptPhaseVerifying || !v.claimed {
		t.Fatalf("control Attempt after delivery = %+v, want verifying with the claim recorded", v)
	}
}

func attemptView(t *testing.T, statePath string, id ids.AttemptID) controlView {
	t.Helper()
	s := openState(t, statePath)
	defer s.Close()
	a, err := s.LoadAttempt(context.Background(), id)
	must(t, err)
	_, found, err := s.LoadResultClaim(context.Background(), id)
	must(t, err)
	return controlView{a.Phase, a.Revision, found}
}

type fixedPolicy struct {
	policy resultacceptance.ResultPolicy
}

func (p fixedPolicy) ResultPolicy(context.Context, ids.TaskID) (resultacceptance.ResultPolicy, error) {
	return p.policy, nil
}

type fixedProducers struct {
	executorDigest string
}

func (p fixedProducers) ProducerDimensions(context.Context, string) (map[resultaccept.Dimension]string, error) {
	return map[resultaccept.Dimension]string{
		resultaccept.DimensionPrincipal:      "producer-principal",
		resultaccept.DimensionOperator:       "producer-operator",
		resultaccept.DimensionExecutorDigest: p.executorDigest,
	}, nil
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }
