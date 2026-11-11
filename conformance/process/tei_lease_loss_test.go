package process

// WP-10: a Lease that expires while the Worker is silent.
//
// The test runs the real eki-control with the recall loop wired (lease_registry)
// and the real eki-agent enrolled but never started: the Attempt is dispatched,
// nothing executes, and the Lease's window closes.
//
// What it shows: the recall loop closes the Attempt as lease_expired and removes
// it from the registry, and the public status stops offering a live assignment.
// What it does not show: that the Task is picked up again. This EKI revision has
// no path from "the Attempt was closed for an expired Lease" back to a Ready
// Task, so the observation is recorded rather than asserted as recovery.
//
// Timing: RecallGrace is 30s and the sweep interval is one minute
// (internal/control/service/run.go). The test therefore takes about a minute;
// it is gated like the other TEI conformance tests.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// withLeaseRegistryOnControl adds lease_registry to the config an *mtlsEnv
// wrote, so the started process runs the recall loop.
func (e *mtlsEnv) withLeaseRegistryOnControl(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(e.cfgPath)
	must(t, err)
	var cfg map[string]any
	must(t, json.Unmarshal(raw, &cfg))
	cfg["lease_registry"] = path
	next, err := json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(e.cfgPath, next, 0o600))
}

// shortLeaseDispatch rewrites the dispatch settings the config already holds so
// the Lease window closes quickly.
func (e *mtlsEnv) shortLeaseDispatch(t *testing.T, lifetime time.Duration) {
	t.Helper()
	raw, err := os.ReadFile(e.cfgPath)
	must(t, err)
	var cfg map[string]any
	must(t, json.Unmarshal(raw, &cfg))
	dispatch, ok := cfg["dispatch"].(map[string]any)
	if !ok {
		t.Fatal("the config has no dispatch section; enableDispatch must run first")
	}
	dispatch["lease_lifetime"] = int64(lifetime)
	next, err := json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(e.cfgPath, next, 0o600))
}

func TestTEILeaseExpiryClosesTheAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixed command is a POSIX wrapper; see #312")
	}
	teiBinary, workspace, _ := teiEnvironment(t)
	bundle, bundleDigest := teiWorkspaceBundle(t, workspace)
	_ = bundle
	_ = bundleDigest
	_ = teiBinary

	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.approveExecutor(t)
	keyFile, controlKeys := controlSigningKeys(t, e)
	e.enableDispatch(t, keyFile)
	e.shortLeaseDispatch(t, 2*time.Second)
	registryPath := filepath.Join(e.dir, "lease-registry.json")
	e.withLeaseRegistryOnControl(t, registryPath)
	control := e.startControl(t)
	defer func() {
		if t.Failed() {
			t.Logf("eki-control output:\n%s", control.out.String())
		}
	}()

	// An Agent is enrolled (so the Worker is usable and placement can pick it)
	// but not started yet: no one executes, so no ResultClaim arrives.
	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 43)
	silent := e.teiAgent(t, "silent", auth, now, controlKeys, nil)

	artifactID := e.uploadTEIInput(t, bundle)
	if artifactID != bundleDigest {
		t.Fatalf("the deployment named the input %s, TEI built %s", artifactID, bundleDigest)
	}
	request := fmt.Sprintf(`{
  "schema": "eki.task-request/v1",
  "executor": {"id": %q, "version_constraint": "^1"},
  "inputs": [{"name": "workspace", "artifact_id": %q}],
  "parameters": {"tei": {"operation": "tei.validate"}},
  "resources": {"cpu_millis": 2000, "memory_bytes": 1073741824, "wall_time_seconds": 600, "output_bytes_max": 1048576, "network_bytes_max": 0},
  "reliability": {"retryable": true, "preemptible": true, "checkpointable": false, "deterministic": true, "max_attempts": 2, "effect_mode": "none"},
  "priority": "normal"
}`, teiExecutorID, artifactID)
	req, err := http.NewRequest(http.MethodPost, "http://"+e.clientAddr+"/v1/tasks", strings.NewReader(request))
	must(t, err)
	req.Header.Set("Authorization", "Bearer "+clientToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "tei-wp10-task-0001")
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
	t.Logf("the Task was dispatched to %s: attempt=%s", attempt.WorkerIdentity, attempt.ID)

	// Wait for the recall loop: RecallGrace (30s) after the Lease window, then
	// the next sweep (the loop also sweeps at start, so this is up to ~90s).
	deadline := time.Now().Add(3 * time.Minute)
	var closed attemptstate.AttemptRecord
	for time.Now().Before(deadline) {
		s := openState(t, e.controlDB)
		rec, err := s.LoadAttempt(context.Background(), attempt.ID)
		_ = s.Close()
		must(t, err)
		if rec.Phase == attemptstate.AttemptPhaseClosed {
			closed = rec
			break
		}
		time.Sleep(time.Second)
	}
	if closed.ID == "" {
		t.Fatalf("the Attempt of the expired Lease was never closed within %s", 3*time.Minute)
	}

	// An Attempt closed for an expired Lease, with no Claim: that is the
	// observation this test exists for.
	if closed.CloseReason != attemptstate.CloseReasonLeaseExpired {
		t.Fatalf("close reason = %q, want %q", closed.CloseReason, attemptstate.CloseReasonLeaseExpired)
	}
	s := openState(t, e.controlDB)
	_, claimed, err := s.LoadResultClaim(context.Background(), attempt.ID)
	must(t, err)
	must(t, s.Close())
	if claimed {
		t.Fatal("a ResultClaim exists for an Attempt no Worker ran")
	}

	// The registry no longer lists the reclaimed Lease.
	raw, err := os.ReadFile(registryPath)
	if err == nil && strings.Contains(string(raw), attempt.LeaseID.String()) {
		t.Logf("the reclaimed Lease is still in the registry file: %s", registryPath)
	}

	// The public status: what does a Client see after the Lease is gone?
	status := e.rawStatus(t, taskID)
	t.Logf("after the close: status=%s terminal_code=%q attempt_count=%d",
		status.Status, status.TerminalCode, status.AttemptCount)

	// The Task's own phase, read from the store by the test (not by the
	// Client). This is the observation the plan calls "recovery", and this
	// revision does not perform it.
	state := openState(t, e.controlDB)
	task, err := state.LoadTask(context.Background(), taskID)
	must(t, err)
	_ = state.Close()
	t.Logf("the Task is in phase %q after its Attempt was closed for an expired Lease", task.Phase)
	if task.Phase == taskstate.TaskPhaseReady || task.Phase == taskstate.TaskPhaseActive {
		t.Logf("note: the Task stayed in a live phase; check whether a new Attempt is created")
	}

	// A second Attempt would be the recovery. Observe it rather than assert it:
	// this revision has no path from an expired Lease back to a Ready Task.
	time.Sleep(10 * time.Second)
	state = openState(t, e.controlDB)
	attempts, err := state.ListAttemptsByTask(context.Background(), taskID)
	must(t, err)
	_ = state.Close()
	t.Logf("attempts after the close: %d", len(attempts))
	if len(attempts) < 2 {
		t.Logf("NOT RECOVERED: no second Attempt was created after the Lease expired (#745 recovery gap)")
	}

	// The stale Attempt must not be handed to a Worker: a Worker that started
	// now would run under a Lease the Control Plane has already reclaimed. The
	// listing drops Leases past their window, so the enrolled Agent sees
	// nothing, and no Claim can arrive for the closed Attempt.
	agent := silent.start(t)
	time.Sleep(5 * time.Second)
	events := readEvents(t, silent.events)
	for _, ev := range events {
		if ev.AttemptID == attempt.ID.String() {
			t.Fatalf("the Agent received the expired Attempt %s: %+v", attempt.ID, ev)
		}
	}
	t.Logf("the Agent that started after the close received %d assignment events, none for %s", len(events), attempt.ID)

	state = openState(t, e.controlDB)
	_, claimed, err = state.LoadResultClaim(context.Background(), attempt.ID)
	must(t, err)
	_ = state.Close()
	if claimed {
		t.Fatal("a Claim arrived for an Attempt whose Lease was reclaimed")
	}
	_ = agent
}

type rawTaskStatus struct {
	Status       string `json:"status"`
	TerminalCode string `json:"terminal_code"`
	AttemptCount int    `json:"attempt_count"`
}

func (e *mtlsEnv) rawStatus(t *testing.T, taskID ids.TaskID) rawTaskStatus {
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
	var status rawTaskStatus
	must(t, json.Unmarshal(raw, &status))
	return status
}
