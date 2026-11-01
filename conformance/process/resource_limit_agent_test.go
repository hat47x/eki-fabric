package process

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	"github.com/hat47x/eki-fabric/internal/agent/service"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
)

func TestAgentStopsAssignmentWhenMemoryLimitIsExceeded(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	control := e.startControl(t)

	oldLimits, oldSandbox := signedchain.Limits, signedchain.Sandbox
	signedchain.Limits = decisionpayload.ResourceLimits{
		CPUMillis: 1000, MemoryBytes: 1 << 20, WallTimeMillis: 300_000, OutputBytesMaximum: 1 << 20,
	}
	signedchain.Sandbox = decisionpayload.SandboxPolicy{Profile: "native-fixed-spike"}
	defer func() { signedchain.Limits, signedchain.Sandbox = oldLimits, oldSandbox }()

	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 31)
	command, args := "/bin/sh", []string{"-c", "echo hello; sleep 5"}
	env := []string{"PATH=/usr/bin:/bin"}
	if runtime.GOOS == "windows" {
		command, args = "cmd.exe", []string{"/c", "echo hello & ping -n 10 127.0.0.1 >nul"}
		env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH")}
	}
	a := e.assignmentAgent(t, "limit", auth, now, &service.ExecutionConfig{Command: command, Args: args, Env: env})
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
	waitFor(t, "resource limit failure to reach Control", func() (bool, string) {
		failed, claimed := false, false
		for _, ev := range readEvents(t, a.events) {
			if ev.AttemptID != placed.AttemptID.String() {
				continue
			}
			if ev.ExecutionStatus == "failed" && strings.Contains(ev.Failure, "memory limit") {
				failed = true
			}
			if ev.ClaimDecision != "" {
				claimed = true
			}
		}
		return failed && claimed, agent.out.String() + "\n" + control.out.String()
	})
	if v := attemptView(t, e.controlDB, placed.AttemptID); v.phase != attemptstate.AttemptPhaseClosed {
		t.Fatalf("Attempt phase = %s, want closed", v.phase)
	}
}
