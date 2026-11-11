package process

// Shared orchestration for the TEI conformance tests: one tei validate
// operation submitted through the Client API, placed by the dispatch loop, run
// by an enrolled Agent, and claimed back to the Control Plane.
//
// The tests that use this are separate files so that each one states its own
// claim: WP-4 reaches a real Attempt (D1/D2), WP-5 accepts the result (D3), and
// WP-11 checks a partial result body. They share the path so a change to it
// cannot make one test pass on a different code path than another.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	agentservice "github.com/hat47x/eki-fabric/internal/agent/service"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	teideleg "github.com/hat47x/eki-fabric/spikes/tei-eki-minimal-delegation"
)

// teiRun is one completed delegation: what EKI made, and what TEI compares
// against.
type teiRun struct {
	env      *mtlsEnv
	taskID   ids.TaskID
	attempt  string
	worker   string
	claim    resultclaim.Claim
	expected string
	report   string
}

// runTEIValidation performs the delegation and returns once the Agent has
// reported a claim. It fails the test when any step does not do what its name
// says; a caller that wants to observe a refusal uses its own path instead.
func runTEIValidation(t *testing.T) teiRun {
	t.Helper()
	teiBinary, workspace, expected := teiEnvironment(t)
	bundle, bundleDigest := teiWorkspaceBundle(t, workspace)
	wrapper := teiWrapper(t)

	oldLimits, oldSandbox := signedchain.Limits, signedchain.Sandbox
	signedchain.Limits = decisionpayload.ResourceLimits{
		CPUMillis: 2000, MemoryBytes: 1 << 30, WallTimeMillis: 600_000,
		OutputBytesMaximum: 1 << 20, TemporaryDiskBytes: 1 << 30,
	}
	signedchain.Sandbox = decisionpayload.SandboxPolicy{Profile: "native-fixed-spike"}
	t.Cleanup(func() { signedchain.Limits, signedchain.Sandbox = oldLimits, oldSandbox })

	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.approveExecutor(t)
	keyFile, controlKeys := controlSigningKeys(t, e)
	e.enableDispatch(t, keyFile)
	control := e.startControl(t)

	report := filepath.Join(t.TempDir(), "report.txt")
	execution := &agentservice.ExecutionConfig{
		Command: wrapper,
		Args: []string{
			filepath.Join(e.teiInputsDir("tei"), bundleDigest),
			teiBinary,
			report,
		},
	}
	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 41)
	a := e.teiAgent(t, "tei", auth, now, controlKeys, execution)
	agent := a.start(t)

	artifactID := e.uploadTEIInput(t, bundle)
	if artifactID != bundleDigest {
		t.Fatalf("the deployment named the input %s, TEI built %s", artifactID, bundleDigest)
	}
	request := fmt.Sprintf(`{
  "schema": "eki.task-request/v1",
  "executor": {"id": %q, "version_constraint": "^1"},
  "inputs": [{"name": "workspace", "artifact_id": %q}],
  "parameters": {"tei": {"operation": "tei.validate", "executable_sha256": %q}},
  "resources": {"cpu_millis": 2000, "memory_bytes": 1073741824, "wall_time_seconds": 600, "output_bytes_max": 1048576, "network_bytes_max": 0},
  "reliability": {"retryable": true, "preemptible": true, "checkpointable": false, "deterministic": true, "max_attempts": 2, "effect_mode": "none"},
  "priority": "normal"
}`, teiExecutorID, artifactID, teiExecutableDigest(t, teiBinary))
	req, err := http.NewRequest(http.MethodPost, "http://"+e.clientAddr+"/v1/tasks", strings.NewReader(request))
	must(t, err)
	req.Header.Set("Authorization", "Bearer "+clientToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "tei-wp5-task-0001")
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	must(t, err)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit = %d: %s", resp.StatusCode, body)
	}
	var accepted struct {
		TaskID string `json:"task_id"`
	}
	must(t, json.Unmarshal(body, &accepted))
	taskID, err := ids.ParseTaskID(accepted.TaskID)
	must(t, err)

	e.releaseTask(t, taskID)
	attempt := e.assignedAttempt(t, taskID)

	waitFor(t, "the Agent ran the TEI operation and claimed", func() (bool, string) {
		executed, claimed := false, false
		for _, ev := range readEvents(t, a.events) {
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
	claim, found, err := store.LoadResultClaim(context.Background(), attempt.ID)
	must(t, err)
	if !found {
		t.Fatal("no ResultClaim was stored: the execution never reached the Control Plane")
	}
	task, err := store.LoadTask(context.Background(), taskID)
	must(t, err)
	if task.Phase != taskstate.TaskPhaseVerifying {
		t.Fatalf("task phase = %s, want %s after an accepted claim", task.Phase, taskstate.TaskPhaseVerifying)
	}

	return teiRun{
		env: e, taskID: taskID, attempt: attempt.ID.String(), worker: attempt.WorkerIdentity,
		claim: claim, expected: expected, report: report,
	}
}

// resultBody fetches the accepted result through the public Client API and
// returns the raw answer, so a test can assert on the bytes the Client sees.
func (r teiRun) resultBody(t *testing.T) (int, []byte) {
	t.Helper()
	return r.get(t, "/v1/tasks/"+r.taskID.String()+"/result")
}

// get performs an authenticated Client API GET and returns the status and body.
func (r teiRun) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+r.env.clientAddr+path, nil)
	must(t, err)
	req.Header.Set("Authorization", "Bearer "+clientToken)
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	must(t, err)
	return resp.StatusCode, raw
}

// teiReceipt reads the Task through the public Client API and returns TEI's
// receipt, using the spike package's own rules. The test does not decide the
// outcome itself: that would be a second implementation of the rule under test.
func (r teiRun) teiReceipt(t *testing.T) (teideleg.Receipt, error) {
	t.Helper()
	client := &teideleg.Client{BaseURL: "http://" + r.env.clientAddr, Token: clientToken}
	_, receipt, err := teideleg.EvaluateTask(context.Background(), client, r.taskID.String(),
		&teideleg.LocalRun{Schema: teideleg.LocalRunSchema, ExitCode: 0, OutputSHA256: r.expected},
		0, 100*time.Millisecond, nil)
	return receipt, err
}

// teiTaskStatus is the public status the tests read. The fields are only the
// ones the contract projects: no Worker, Lease or Attempt identity.
type teiTaskStatus struct {
	Status       string `json:"status"`
	TerminalCode string `json:"terminal_code"`
	AttemptCount int    `json:"attempt_count"`
}

// taskStatus performs the authenticated Client API read of a Task's public
// status. It is shared: a test that used its own read could disagree with the
// others about what the Client sees.
func (e *mtlsEnv) taskStatus(t *testing.T, taskID ids.TaskID) teiTaskStatus {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+e.clientAddr+"/v1/tasks/"+taskID.String(), nil)
	must(t, err)
	req.Header.Set("Authorization", "Bearer "+clientToken)
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	must(t, err)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var status teiTaskStatus
	must(t, json.Unmarshal(raw, &status))
	return status
}
