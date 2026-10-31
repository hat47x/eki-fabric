package process

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	"github.com/hat47x/eki-fabric/internal/agent/service"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
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
}
