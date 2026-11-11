package process

// Duplicate submission and a passing deadline, with an Attempt that already
// exists.
//
// The earlier observations covered these two cases only while the Task had no
// Attempt (trace-2.json and probe.json in the spike). This test reaches the
// state the plan asks for: the dispatch loop has created and dispatched the
// Attempt, and the Agent is enrolled but silent, so the Attempt stays put while
// the test resubmits and lets the deadline pass.
//
// What it shows:
//   - resubmitting the same Idempotency-Key and body answers replayed, names the
//     same Task, and creates no second Attempt;
//   - a deadline that passes after the Attempt was dispatched does not move the
//     Task to a terminal state and does not end the Attempt.
//
// What it does not show: that a deadline is eventually acted on at all. Nothing
// in this revision ends a Task when its deadline passes (#111).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// submitTEITask posts one task request and returns the Task id it was given.
func submitTEITask(t *testing.T, e *mtlsEnv, key, request string) (ids.TaskID, bool) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+e.clientAddr+"/v1/tasks", strings.NewReader(request))
	must(t, err)
	req.Header.Set("Authorization", "Bearer "+clientToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	must(t, err)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit %s = %d: %s", key, resp.StatusCode, body)
	}
	var accepted struct {
		TaskID   string `json:"task_id"`
		Replayed bool   `json:"replayed"`
	}
	must(t, json.Unmarshal(body, &accepted))
	taskID, err := ids.ParseTaskID(accepted.TaskID)
	must(t, err)
	return taskID, accepted.Replayed
}

func TestTEIDuplicateAndDeadlineWithALiveAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixed command is a POSIX wrapper; see #312")
	}
	teiBinary, workspace, _ := teiEnvironment(t)
	bundle, bundleDigest := teiWorkspaceBundle(t, workspace)

	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.approveExecutor(t)
	keyFile, controlKeys := controlSigningKeys(t, e)
	e.enableDispatch(t, keyFile)
	control := e.startControl(t)
	defer func() {
		if t.Failed() {
			t.Logf("eki-control output:\n%s", control.out.String())
		}
	}()

	// The Agent is enrolled so the Worker is usable, but it is not started: the
	// Attempt therefore stays DISPATCHED for the whole test.
	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 47)
	e.teiAgent(t, "quiet", auth, now, controlKeys, nil)

	artifactID := e.uploadTEIInput(t, bundle)
	if artifactID != bundleDigest {
		t.Fatalf("the deployment named the input %s, TEI built %s", artifactID, bundleDigest)
	}

	// The deadline is short but still in the future at admission: a past
	// deadline is refused with DEADLINE_IN_PAST, which is a different case.
	deadline := time.Now().UTC().Add(4 * time.Second).Format(time.RFC3339)
	request := fmt.Sprintf(`{
  "schema": "eki.task-request/v1",
  "executor": {"id": %q, "version_constraint": "^1"},
  "inputs": [{"name": "workspace", "artifact_id": %q}],
  "parameters": {"tei": {"operation": "tei.validate", "executable_sha256": %q}},
  "resources": {"cpu_millis": 2000, "memory_bytes": 1073741824, "wall_time_seconds": 600, "output_bytes_max": 1048576, "network_bytes_max": 0},
  "reliability": {"retryable": true, "preemptible": true, "checkpointable": false, "deterministic": true, "max_attempts": 2, "effect_mode": "none"},
  "deadline": %q,
  "priority": "normal"
}`, teiExecutorID, artifactID, teiExecutableDigest(t, teiBinary), deadline)

	key := "tei-live-attempt-0001"
	taskID, replayed := submitTEITask(t, e, key, request)
	if replayed {
		t.Fatal("the first submission was answered as a replay")
	}
	e.releaseTask(t, taskID)
	attempt := e.assignedAttempt(t, taskID)

	// A second submission of the same key and body, now that an Attempt exists.
	againID, againReplayed := submitTEITask(t, e, key, request)
	if !againReplayed {
		t.Fatal("the same key and body was not answered as a replay")
	}
	if againID != taskID {
		t.Fatalf("the replay named task %s, want %s", againID, taskID)
	}

	// Give the dispatch loop more than one tick to act on the replay.
	time.Sleep(3 * time.Second)
	s := openState(t, e.controlDB)
	attempts, err := s.ListAttemptsByTask(context.Background(), taskID)
	must(t, err)
	_ = s.Close()
	if len(attempts) != 1 || attempts[0].ID != attempt.ID {
		t.Fatalf("a replayed submission changed the Attempts: %+v", attempts)
	}
	for _, a := range attempts {
		if a.Phase == attemptstate.AttemptPhaseClosed {
			t.Fatalf("the Attempt was closed by a replayed submission: %+v", a)
		}
	}
	t.Logf("replay with a live Attempt: still %d Attempt (%s), status %s",
		len(attempts), attempt.ID, e.rawStatus(t, taskID).Status)

	// Let the deadline pass. The Attempt was dispatched before it.
	time.Sleep(6 * time.Second)
	status := e.rawStatus(t, taskID)
	if status.TerminalCode != "" {
		t.Fatalf("a passing deadline produced terminal_code %q; #111 says it does not", status.TerminalCode)
	}
	if status.Status == "completed" || status.Status == "failed" || status.Status == "cancelled" {
		t.Fatalf("a passing deadline moved the Task to %s", status.Status)
	}

	s = openState(t, e.controlDB)
	after, err := s.LoadAttempt(context.Background(), attempt.ID)
	must(t, err)
	_ = s.Close()
	if after.Phase == attemptstate.AttemptPhaseClosed {
		t.Fatalf("a passing deadline closed the Attempt: %+v", after)
	}
	t.Logf("after the deadline: status=%s terminal_code=%q attempt_phase=%s attempt_count=%d",
		status.Status, status.TerminalCode, after.Phase, status.AttemptCount)

	// The result is still not available, and the answer is the retryable one.
	code, body := (teiRun{env: e, taskID: taskID}).get(t, "/v1/tasks/"+taskID.String()+"/result")
	if code != http.StatusAlreadyReported && code != http.StatusConflict {
		t.Logf("result after the deadline: %d (the contract says 409)", code)
	}
	var problem struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(body, &problem); err == nil && problem.Code != "" {
		t.Logf("result problem: code=%s retryable=%v", problem.Code, problem.Retryable)
		if problem.Code != "RESULT_NOT_READY" {
			t.Errorf("a dispatched Attempt that has not reported should answer RESULT_NOT_READY, got %s", problem.Code)
		}
	}
}
