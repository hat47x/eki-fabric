package process

// One side-effect-free TEI operation (tei validate) delegated through EKI's
// real processes: the Client API admits the Task with an input artifact, the
// dispatch loop issues the Lease and WorkOrder, the Agent fetches the input,
// runs the fixed wrapper, and reports a ResultClaim.
//
// This is the TEST PROFILE. It does not prove Worker Protocol (#198)
// compatibility, a production authority, or that EKI independently verified
// the result. What it can show is D1 (an Attempt exists), D2 (the digest the
// Agent reported equals the digest TEI computes locally), and that the Task
// reaches verifying.
//
// The TEI binaries are not part of this repository, so the test skips unless
// the operator supplies them:
//
//	TEI_EKI_TEI_BINARY       path to the tei executable
//	TEI_EKI_WORKSPACE        path to the workspace tei validate reads
//	TEI_EKI_EXPECTED_SHA256  sha256 of the output TEI produces locally, over
//	                         stdout and stderr concatenated (see runner.go)
//
// The expected digest is never recomputed here: a test that derived it from
// the run under test could not tell a wrong result from a right one.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"archive/tar"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	agentservice "github.com/hat47x/eki-fabric/internal/agent/service"
	"github.com/hat47x/eki-fabric/internal/agent/trustanchor"
	"github.com/hat47x/eki-fabric/internal/control/service"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

const teiExecutorID = "tei.validate"

func teiEnvironment(t *testing.T) (teiBinary, workspaceDir, expectedDigest string) {
	t.Helper()
	var missing []string
	for _, name := range []string{"TEI_EKI_TEI_BINARY", "TEI_EKI_WORKSPACE", "TEI_EKI_EXPECTED_SHA256"} {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Skipf("TEI binaries are not part of this repository; set %s to run this test", strings.Join(missing, ", "))
	}
	teiBinary = os.Getenv("TEI_EKI_TEI_BINARY")
	workspaceDir = os.Getenv("TEI_EKI_WORKSPACE")
	expectedDigest = strings.ToLower(os.Getenv("TEI_EKI_EXPECTED_SHA256"))
	for _, path := range []string{teiBinary, workspaceDir} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if raw, err := hex.DecodeString(expectedDigest); err != nil || len(raw) != sha256.Size {
		t.Fatalf("TEI_EKI_EXPECTED_SHA256 must be 64 hex characters, got %q", expectedDigest)
	}
	return teiBinary, workspaceDir, expectedDigest
}

// teiWorkspaceBundle packs the workspace deterministically, so the input digest
// does not depend on when or where the bundle was made.
func teiWorkspaceBundle(t *testing.T, dir string) ([]byte, string) {
	t.Helper()
	var paths []string
	must(t, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == dir || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}))
	if len(paths) == 0 {
		t.Fatalf("%s has no files", dir)
	}
	sort.Strings(paths)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.ModTime = time.Time{}
	tw := tar.NewWriter(gz)
	for _, rel := range paths {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		must(t, err)
		hdr := &tar.Header{
			Name: rel, Typeflag: tar.TypeReg, Mode: 0o644,
			Size: int64(len(body)), ModTime: time.Unix(0, 0).UTC(),
		}
		must(t, tw.WriteHeader(hdr))
		_, err = tw.Write(body)
		must(t, err)
	}
	must(t, tw.Close())
	must(t, gz.Close())
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// teiWrapperSource is the Agent's fixed command: it unpacks the input artifact
// the Agent fetched and runs the TEI operation on it, passing stdout and stderr
// through unchanged so the Runner's digest is over the same bytes TEI produced
// locally.
const teiWrapperSource = `package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("usage: wrapper <archive> <tei binary> <report file>")
	}
	archive, tei, report := os.Args[1], os.Args[2], os.Args[3]
	dir, err := os.MkdirTemp("", "tei-workspace-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	root := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := extract(archive, root); err != nil {
		log.Fatal(err)
	}
	cmd := exec.Command(tei, "validate", "-workspace", root, "-json")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			log.Fatal(err)
		}
	}
	if entries, err := os.ReadDir(root); err == nil {
		_ = os.WriteFile(report, []byte(fmt.Sprintf("exit=%d entries=%d\n", code, len(entries))), 0o600)
	}
	os.Exit(code)
}

func extract(archive, root string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(hdr.Name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(filepath.ToSlash(name), "../") {
			return fmt.Errorf("unsafe path %q", hdr.Name)
		}
		path := filepath.Join(root, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			out, err := os.Create(path)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		default:
			return fmt.Errorf("unsupported entry %q", hdr.Name)
		}
	}
}
`

func teiWrapper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "wrapper.go")
	must(t, os.WriteFile(src, []byte(teiWrapperSource), 0o600))
	bin := filepath.Join(dir, "tei-wrapper")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the wrapper: %v\n%s", err, out)
	}
	return bin
}

// teiInputsDir is where the Agent stores fetched inputs. The name is fixed
// before enrollment so the wrapper's argument can point at it.
func (e *mtlsEnv) teiInputsDir(name string) string {
	return filepath.Join(e.dir, name+"-inputs")
}

// teiAgent enrolls a Worker whose Agent fetches inputs and runs the wrapper.
func (e *mtlsEnv) teiAgent(t *testing.T, name string, auth *signedchain.Authorities, validatedAt time.Time, controlKeys []trustanchor.Key, execution *agentservice.ExecutionConfig) agentSide {
	t.Helper()
	inputsDir := e.teiInputsDir(name)
	must(t, os.MkdirAll(inputsDir, 0o700))
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
			Machine:         agentservice.MachineConfig{CPUMillis: 8000, MemoryBytes: 8 << 30},
			CeilingPercent:  50,
			ArtifactBaseURL: e.workerURL,
			InputsDir:       inputsDir,
			Execution:       execution,
		},
	}
	raw, err := json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(a.cfgPath, raw, 0o600))
	return a
}

// approveExecutor rewrites the written config so the deployment admits the TEI
// executor id and the resources the operation asks for. The config is decoded
// into service.Config, not a generic map, so a field that stops existing or is
// renamed fails here rather than being silently dropped from the config the
// test starts.
func (e *mtlsEnv) approveExecutor(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(e.cfgPath)
	must(t, err)
	var cfg service.Config
	must(t, json.Unmarshal(raw, &cfg))
	cfg.Policy.ApprovedExecutors = []string{teiExecutorID}
	cfg.Policy.PerTask = service.PerTaskConfig{
		CPUMillis: 2000, MemoryBytes: 1 << 30, WallTimeSeconds: 600,
		OutputBytesMax: 1 << 20, NetworkBytesMax: 0, TemporaryDiskBytes: 1 << 30,
	}
	next, err := json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(e.cfgPath, next, 0o600))
}

// uploadTEIInput puts the bundle into the deployment through the Client API and
// returns the artifact id the deployment named it by.
func (e *mtlsEnv) uploadTEIInput(t *testing.T, body []byte) string {
	t.Helper()
	ingest := fmt.Sprintf(`{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":%d,"media_type":"application/gzip","requested_data_class":"internal"}`, len(body))
	req, err := http.NewRequest(http.MethodPost, "http://"+e.clientAddr+"/v1/artifacts/ingest-intents", strings.NewReader(ingest))
	must(t, err)
	req.Header.Set("Authorization", "Bearer "+clientToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "tei-wp4-ingest-0001")
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	must(t, err)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ingest intent = %d: %s", resp.StatusCode, raw)
	}
	var intent struct {
		Upload struct {
			Href string `json:"href"`
		} `json:"upload"`
	}
	must(t, json.Unmarshal(raw, &intent))
	if intent.Upload.Href == "" {
		t.Fatalf("ingest answer has no upload href: %s", raw)
	}
	put, err := http.NewRequest(http.MethodPut, "http://"+e.clientAddr+intent.Upload.Href, bytes.NewReader(body))
	must(t, err)
	put.Header.Set("Authorization", "Bearer "+clientToken)
	put.Header.Set("Content-Type", "application/octet-stream")
	up, err := http.DefaultClient.Do(put)
	must(t, err)
	uraw, err := io.ReadAll(io.LimitReader(up.Body, 1<<20))
	up.Body.Close()
	must(t, err)
	if up.StatusCode != http.StatusAccepted {
		t.Fatalf("upload = %d: %s", up.StatusCode, uraw)
	}
	var accepted struct {
		Artifact struct {
			ID string `json:"artifact_id"`
		} `json:"artifact"`
	}
	must(t, json.Unmarshal(uraw, &accepted))
	if accepted.Artifact.ID == "" {
		t.Fatalf("upload answer has no artifact id: %s", uraw)
	}
	return accepted.Artifact.ID
}

func TestTEIValidationReachesARealAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixed command is a POSIX wrapper, and the Worker runner's tree stop is unstable on Windows (#312)")
	}
	teiBinary, workspace, expected := teiEnvironment(t)
	bundle, bundleDigest := teiWorkspaceBundle(t, workspace)
	wrapper := teiWrapper(t)

	oldLimits, oldSandbox := signedchain.Limits, signedchain.Sandbox
	signedchain.Limits = decisionpayload.ResourceLimits{
		CPUMillis: 2000, MemoryBytes: 1 << 30, WallTimeMillis: 600_000,
		OutputBytesMaximum: 1 << 20, TemporaryDiskBytes: 1 << 30,
	}
	signedchain.Sandbox = decisionpayload.SandboxPolicy{Profile: "native-fixed-spike"}
	defer func() { signedchain.Limits, signedchain.Sandbox = oldLimits, oldSandbox }()

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
	req.Header.Set("Idempotency-Key", "tei-wp4-task-0001")
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
	t.Logf("D1: task=%s attempt=%s worker=%s", taskID, attempt.ID, attempt.WorkerIdentity)

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
	if got := claim.OutputDigest().Hex(); got != expected {
		t.Fatalf("D2: the Agent reported %s, TEI computes %s locally", got, expected)
	}
	t.Logf("D2: the Agent's digest equals TEI's local expectation (%s)", expected)

	if status := e.taskStatus(t, taskID); status.AttemptCount < 1 {
		t.Fatalf("attempt_count = %d, want >= 1", status.AttemptCount)
	}
	task, err := store.LoadTask(context.Background(), taskID)
	must(t, err)
	if task.Phase != taskstate.TaskPhaseVerifying {
		t.Fatalf("task phase = %s, want %s after an accepted claim", task.Phase, taskstate.TaskPhaseVerifying)
	}
	// The wrapper reports the exit code and how many entries it unpacked, so a
	// result can be accepted for a run that failed. The exit code is checked
	// here rather than taken from the digest: a failed run can still produce
	// output bytes, and those bytes would match.
	rawReport, err := os.ReadFile(report)
	must(t, err)
	if !strings.Contains(string(rawReport), "exit=0") {
		t.Fatalf("the TEI operation failed inside the wrapper: %s", strings.TrimSpace(string(rawReport)))
	}
	t.Logf("wrapper report: %s", strings.TrimSpace(string(rawReport)))
}

// teiExecutableDigest is the digest of the TEI binary, recorded in the request
// as a Client declaration. EKI does not enforce it (see the spike's Binding
// table); the test records it so the trace can be cross-checked.
func teiExecutableDigest(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	must(t, err)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// releaseTask moves an ADMITTED Task to READY, the state the dispatch loop
// consumes. The retry covers the loop promoting the Task first.
func (e *mtlsEnv) releaseTask(t *testing.T, taskID ids.TaskID) {
	t.Helper()
	s := openState(t, e.controlDB)
	defer s.Close()
	for i := 0; i < 40; i++ {
		task, err := s.LoadTask(context.Background(), taskID)
		must(t, err)
		switch task.Phase {
		case taskstate.TaskPhaseAdmitted:
			next := task
			next.Phase, next.UpdatedAt = taskstate.TaskPhaseReady, time.Now().UTC()
			next.Revision = task.Revision + 1
			if err := s.CompareAndSwapTask(context.Background(), taskID, task.Revision, next); err == nil {
				return
			}
		case taskstate.TaskPhaseReady, taskstate.TaskPhaseActive:
			return
		default:
			t.Fatalf("task phase = %s", task.Phase)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the Task was not released to the dispatch loop")
}

// teiReceipt is declared in tei_delegation_helpers_test.go.
