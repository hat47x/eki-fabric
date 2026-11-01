package process

// An HTTP Client API submission reaches an enrolled Agent and is executed.
// The dispatch loop issues the Lease and WorkOrder, the Agent fetches it over
// the mTLS Worker listener, runs the fixed command and submits a ResultClaim.
//
// This is the TEST PROFILE. It does not show Worker Protocol (#198)
// compatibility, result acceptance, or a production authority.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	agentservice "github.com/hat47x/eki-fabric/internal/agent/service"
	"github.com/hat47x/eki-fabric/internal/agent/trustanchor"
	"github.com/hat47x/eki-fabric/internal/control/resultacceptance"
	"github.com/hat47x/eki-fabric/internal/control/signingkeys"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

const httpTaskRequest = `{
  "schema": "eki.task-request/v1",
  "executor": {"id": "dev.example.transform", "version_constraint": "^1"},
  "inputs": [],
  "parameters": {"mode": "normalize"},
  "resources": {"cpu_millis": 1000, "memory_bytes": 268435456, "wall_time_seconds": 60, "output_bytes_max": 1048576, "network_bytes_max": 1048576},
  "reliability": {"retryable": true, "preemptible": true, "checkpointable": false, "deterministic": true, "max_attempts": 3, "effect_mode": "none"},
  "priority": "normal"
}`

func controlSigningKeys(t *testing.T, e *mtlsEnv) (string, []trustanchor.Key) {
	t.Helper()
	path := e.writeSigningKeys(t)
	raw, err := os.ReadFile(path)
	must(t, err)
	var doc signingkeys.File
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	keys := make([]trustanchor.Key, 0, len(doc.Keys))
	for _, k := range doc.Keys {
		keys = append(keys, trustanchor.Key{
			KeyID: k.KeyID, Role: k.Role, Principal: k.Principal, Algorithm: "EdDSA",
			PublicKey: k.PublicKey, SigningNotBefore: k.SigningNotBefore,
			SigningNotAfter: k.SigningNotAfter, KeyPolicy: k.KeyPolicy,
		})
	}
	return path, keys
}

func (e *mtlsEnv) assignmentAgentWithSigningKeys(t *testing.T, name string, auth *signedchain.Authorities, validatedAt time.Time, controlKeys []trustanchor.Key, execution *agentservice.ExecutionConfig) agentSide {
	t.Helper()
	stateDir, worker := e.enrollAgent(t, name)
	a := agentSide{
		stateDir: stateDir, worker: worker, cfgPath: filepath.Join(e.dir, name+"-agent.json"),
		events: filepath.Join(e.dir, name+"-events.jsonl"), replayDB: filepath.Join(e.dir, name+"-replay.db"),
	}
	anchor := auth.TrustAnchor(validatedAt, "policy-1")
	anchor.Keys = append(anchor.Keys, controlKeys...)
	rawAnchor, err := json.Marshal(anchor)
	must(t, err)
	anchorPath := filepath.Join(e.dir, name+"-trust-anchor.json")
	must(t, os.WriteFile(anchorPath, rawAnchor, 0o600))
	cfg := agentservice.Config{
		ControlURL: e.workerURL, QueueDB: filepath.Join(e.dir, name+"-queue.db"), StateDir: stateDir,
		Assignments: &agentservice.AssignmentConfig{
			TrustAnchorFile: anchorPath, ReplayDB: a.replayDB, EventsFile: a.events,
			ApprovedExecutors: []agentservice.ExecutorApprovalConfig{{
				DecisionID:     signedchain.ExecutorApproval.DecisionID,
				PayloadDigest:  signedchain.ExecutorApproval.PayloadDigest.Hex(),
				ExecutorDigest: signedchain.ExecutorApproval.ExecutorDigest.Hex(),
			}},
			Machine: agentservice.MachineConfig{CPUMillis: 8000, MemoryBytes: 8 << 30}, CeilingPercent: 50,
			Execution: execution,
		},
	}
	raw, err := json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(a.cfgPath, raw, 0o600))
	return a
}

func TestHTTPTaskSubmissionIsExecutedByTheAgent(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)

	oldLimits, oldSandbox := signedchain.Limits, signedchain.Sandbox
	signedchain.Limits = decisionpayload.ResourceLimits{
		CPUMillis: 1000, MemoryBytes: 1 << 28, WallTimeMillis: 300_000, OutputBytesMaximum: 1 << 20,
	}
	signedchain.Sandbox = decisionpayload.SandboxPolicy{Profile: "native-fixed-spike"}
	defer func() { signedchain.Limits, signedchain.Sandbox = oldLimits, oldSandbox }()

	e.writeConfig(t)
	keyFile, controlKeys := controlSigningKeys(t, e)
	e.enableDispatch(t, keyFile)

	control := e.startControl(t)

	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 31)
	command, args := "/bin/echo", []string{"hello"}
	if runtime.GOOS == "windows" {
		command, args = "cmd.exe", []string{"/c", "echo", "hello"}
	}
	agentSide := e.assignmentAgentWithSigningKeys(t, "http", auth, now, controlKeys,
		&agentservice.ExecutionConfig{Command: command, Args: args})
	agent := agentSide.start(t)

	request, err := http.NewRequest(http.MethodPost, "http://"+e.clientAddr+"/v1/tasks", bytes.NewReader([]byte(httpTaskRequest)))
	must(t, err)
	request.Header.Set("Authorization", "Bearer "+clientToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "http-e2e-key-0004")
	response, err := http.DefaultClient.Do(request)
	must(t, err)
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	must(t, err)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("submit = %d: %s", response.StatusCode, body)
	}
	var accepted struct {
		TaskID string `json:"task_id"`
	}
	must(t, json.Unmarshal(body, &accepted))
	taskID, err := ids.ParseTaskID(accepted.TaskID)
	must(t, err)

	// Admission leaves the Task ADMITTED; the dispatch loop consumes READY.
	// The test performs the explicit release an operator side would.
	store := openState(t, e.controlDB)
	// The dispatch loop promotes an ADMITTED Task to READY, and to ACTIVE once
	// it creates the first Attempt, so the phase can change between the read
	// and the write. Retry: a revision conflict means the loop got there first.
	released := false
	for attempt := 0; attempt < 10 && !released; attempt++ {
		task, err := store.LoadTask(context.Background(), taskID)
		must(t, err)
		switch task.Phase {
		case taskstate.TaskPhaseAdmitted:
			next := task
			next.Phase, next.UpdatedAt = taskstate.TaskPhaseReady, time.Now().UTC()
			next.Revision = task.Revision + 1
			if err := store.CompareAndSwapTask(context.Background(), taskID, task.Revision, next); err == nil {
				released = true
			} else {
				time.Sleep(10 * time.Millisecond)
			}
		case taskstate.TaskPhaseReady, taskstate.TaskPhaseActive:
			// The dispatch loop promoted the Task before this read.
			released = true
		default:
			t.Fatalf("task phase = %s", task.Phase)
		}
	}
	if !released {
		t.Fatal("the Task was not released to the dispatch loop")
	}
	store.Close()

	waitFor(t, "agent execution and claim", func() (bool, string) {
		executed, claimed := false, false
		for _, ev := range readEvents(t, agentSide.events) {
			if ev.ExecutionStatus == "completed" {
				executed = true
			}
			if ev.ClaimDecision != "" {
				claimed = true
			}
		}
		return executed && claimed, agent.out.String() + "\n" + control.out.String()
	})

	finalStore := openState(t, e.controlDB)
	defer finalStore.Close()
	attempts, err := finalStore.ListAttemptsByTask(context.Background(), taskID)
	must(t, err)
	var found attemptstate.AttemptRecord
	for _, attempt := range attempts {
		if attempt.WorkerIdentity == agentSide.worker {
			found = attempt
		}
	}
	if found.ID == "" {
		t.Fatalf("the submitted Task was not assigned to the enrolled Agent: %+v", attempts)
	}
	claim, ok, err := finalStore.LoadResultClaim(context.Background(), found.ID)
	must(t, err)
	if !ok {
		t.Fatal("the Agent's ResultClaim was not stored")
	}
	t.Logf("task=%s attempt=%s claim=%s", taskID, found.ID, claim.OutputDigest().Hex())

	// Verification by re-execution: run the same fixed command outside the
	// Agent and compare the digest with the claim. This is reproducible on
	// this host; it is not an independent verifier.
	reproduced, err := reexecuteOutputDigest()
	must(t, err)
	if reproduced != claim.OutputDigest().Hex() {
		t.Fatalf("re-execution digest %s does not match claim %s", reproduced, claim.OutputDigest().Hex())
	}
	policy := resultaccept.Policy{
		Level: resultaccept.LevelRA2, Threshold: 2,
		DistinctDimensions: []resultaccept.Dimension{resultaccept.DimensionPrincipal, resultaccept.DimensionOperator},
	}
	acceptance, err := resultacceptance.New(resultacceptance.Config{
		Attempts: finalStore, Tasks: finalStore, Claims: finalStore, Results: finalStore,
		Rejections: finalStore,
		Policies:   fixedPolicy{policy: resultacceptance.ResultPolicy{Policy: policy, Output: resultaccept.OutputMechanical}},
		Producers:  fixedProducers{executorDigest: claim.ExecutorDigest().Hex()},
		Clock:      systemClock{},
	})
	must(t, err)
	verification := func(id, principal, operator string) resultaccept.Verification {
		return resultaccept.Verification{
			VerifierID: id, TaskID: claim.TaskID().String(), AttemptID: claim.AttemptID().String(),
			Check: resultaccept.CheckReproduced, ObservedDigest: reproduced,
			Dimensions: map[resultaccept.Dimension]string{
				resultaccept.DimensionPrincipal: principal, resultaccept.DimensionOperator: operator,
				resultaccept.DimensionExecutorDigest: contract.SHA256([]byte("verifier-" + id)).Hex(),
			},
		}
	}
	acceptanceResult, err := acceptance.Accept(context.Background(), resultacceptance.Request{
		AttemptID: found.ID,
		Verifications: []resultaccept.Verification{
			verification("verifier-1", "p-1", "o-1"),
			verification("verifier-2", "p-2", "o-2"),
		},
	})
	must(t, err)
	if acceptanceResult.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("acceptance outcome = %s (rejected: %v)", acceptanceResult.Outcome(), acceptanceResult.Rejected)
	}
	if !acceptanceResult.Accepted.Valid() || acceptanceResult.Accepted.TaskID() != taskID {
		t.Fatalf("accepted result = %+v", acceptanceResult.Accepted)
	}
	t.Logf("accepted result digest=%s", acceptanceResult.Accepted.OutputDigest().Hex())
}
