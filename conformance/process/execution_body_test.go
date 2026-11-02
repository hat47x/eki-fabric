package process

import (
	"context"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
	"github.com/hat47x/eki-fabric/internal/agent/service"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
)

// The bytes the Agent's Runner produced reach the Control Plane's body store
// under the Attempt that produced them (#465, ADR-0034). Before the output was
// handed from the Runner that executed to the sender that delivers, the sender
// read an empty copy and skipped the body without saying so.
func TestAgentSendsTheBodyItsRunnerProduced(t *testing.T) {
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
	a := e.assignmentAgent(t, "body", auth, now, &service.ExecutionConfig{Command: command, Args: args})

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
	waitFor(t, "result claim and its body sent", func() (bool, string) {
		sent := false
		for _, ev := range readEvents(t, a.events) {
			if ev.AttemptID == placed.AttemptID.String() && ev.Outcome == "claim_body_sent" {
				sent = true
			}
		}
		return sent, agent.out.String() + "\n" + control.out.String()
	})

	store := openState(t, e.controlDB)
	defer store.Close()
	claim, found, err := store.LoadResultClaim(context.Background(), placed.AttemptID)
	must(t, err)
	if !found {
		t.Fatal("claim was not stored")
	}
	bodies, err := artifactfile.New(e.artifactsDir)
	must(t, err)
	file, size, err := bodies.Open(claim.OutputDigest().Hex())
	if err != nil {
		t.Fatalf("the body the claim reported (%s) is not kept on the Control Plane: %v", claim.OutputDigest().Hex(), err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	must(t, err)
	if int64(len(got)) != size || uint64(len(got)) != claim.OutputBytes() {
		t.Fatalf("kept body is %d bytes (store says %d), claim reported %d", len(got), size, claim.OutputBytes())
	}
	if contract.SHA256(got) != claim.OutputDigest() {
		t.Fatalf("kept body does not measure to the claimed digest")
	}
}
