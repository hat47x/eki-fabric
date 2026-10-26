// Package process runs eki-control and eki-agent as separate OS processes and
// delivers a ResultClaim from the Agent's durable SQLite queue to the Control
// Plane over TLS.
//
// This is the TEST PROFILE of the Worker Plane, not the Worker Protocol: the
// session-token table, the JSON body and the HTTP framing are test fixtures
// until Worker authentication/enrollment and the wire contract (#198) are
// decided. A pass here shows that the two binaries, the TLS listener, the two
// SQLite files and the process lifecycle work together. It does not show
// worker.proto compatibility, an enrollment scheme, or that a Worker ran a
// Task.
package process

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	queuesqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/sqlite"
	statesqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/control/service"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

const (
	workerAToken = "test-profile-token-worker-a-0123456789"
	workerBToken = "test-profile-token-worker-b-0123456789"
	clientToken  = "test-profile-client-token-0123456789"
	pollDeadline = 45 * time.Second
)

var binDir string

func TestMain(m *testing.M) { os.Exit(testMain(m)) }

func testMain(m *testing.M) int {
	dir, err := os.MkdirTemp("", "eki-process-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	binDir = dir
	for _, name := range []string{"eki-control", "eki-agent"} {
		cmd := exec.Command("go", "build", "-o", binary(name), "./cmd/"+name)
		cmd.Dir = filepath.Join("..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "go build %s: %v\n%s", name, err, out)
			return 1
		}
	}
	return m.Run()
}

func binary(name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(binDir, name)
}

// ---- fixtures -------------------------------------------------------------

type pki struct{ caFile, certFile, keyFile string }

// newPKI writes a throwaway CA and a server certificate for 127.0.0.1.
func newPKI(t *testing.T, dir string) pki {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "eki process test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	must(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	must(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "eki-control"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	must(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	must(t, err)

	p := pki{filepath.Join(dir, "ca.pem"), filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key")}
	writePEM(t, p.caFile, "CERTIFICATE", caDER)
	writePEM(t, p.certFile, "CERTIFICATE", leafDER)
	writePEM(t, p.keyFile, "PRIVATE KEY", keyDER)
	return p
}

func writePEM(t *testing.T, path, kind string, der []byte) {
	t.Helper()
	must(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600))
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	defer l.Close()
	return l.Addr().String()
}

// waitFor polls cond until it reports true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(pollDeadline)
	var last string
	for {
		ok, detail := cond()
		if ok {
			return
		}
		last = detail
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (last: %s)", what, last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ---- processes ------------------------------------------------------------

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *safeBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type proc struct {
	cmd    *exec.Cmd
	out    *safeBuffer
	exited chan struct{}
	err    error
}

func startProc(t *testing.T, name string, args ...string) *proc {
	t.Helper()
	p := &proc{cmd: exec.Command(binary(name), args...), out: &safeBuffer{}, exited: make(chan struct{})}
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	must(t, p.cmd.Start())
	go func() { p.err = p.cmd.Wait(); close(p.exited) }()
	t.Cleanup(func() {
		p.kill()
		if t.Failed() {
			t.Logf("%s output:\n%s", name, p.out.String())
		}
	})
	return p
}

func (p *proc) kill() {
	_ = p.cmd.Process.Kill()
	<-p.exited
}

func (p *proc) running() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// stop sends SIGTERM-equivalent shutdown where supported and waits.
func (p *proc) interrupt(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		p.kill()
		return
	}
	must(t, p.cmd.Process.Signal(os.Interrupt))
	select {
	case <-p.exited:
	case <-time.After(20 * time.Second):
		t.Fatalf("process did not exit after interrupt:\n%s", p.out.String())
	}
}

type env struct {
	dir       string
	pki       pki
	controlDB string
	cfgPath   string
	workerURL string
	workerBin string
}

func (e *env) writeControlConfig(t *testing.T) (clientAddr string) {
	t.Helper()
	workerAddr := strings.TrimPrefix(e.workerURL, "https://")
	clientAddr = freeAddr(t)
	cfg := service.Config{
		Listen: clientAddr, Database: e.controlDB,
		Tokens: []service.TokenConfig{{Principal: "alice", Role: "client", TokenSHA256: clientapi.HashToken(clientToken)}},
		Policy: service.PolicyConfig{
			ApprovedExecutors: []string{"dev.example.transform"},
			PerTask:           service.PerTaskConfig{CPUMillis: 1000, MemoryBytes: 1 << 28, WallTimeSeconds: 60, OutputBytesMax: 1 << 20, NetworkBytesMax: 1 << 20, TemporaryDiskBytes: 1 << 20},
			MaxAttempts:       3, MaxOutstandingTasks: 4,
		},
		Worker: &service.WorkerConfig{
			Listen: workerAddr,
			TLS:    &service.TLSConfig{CertFile: e.pki.certFile, KeyFile: e.pki.keyFile},
			Sessions: []service.WorkerSessionConfig{
				{WorkerIdentity: "worker-a", TrustDomain: "example.org", SessionTokenSHA256: clientapi.HashToken(workerAToken)},
				{WorkerIdentity: "worker-b", TrustDomain: "example.org", SessionTokenSHA256: clientapi.HashToken(workerBToken)},
			},
		},
	}
	raw, err := json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(e.cfgPath, raw, 0o600))
	return clientAddr
}

// startControl starts eki-control and waits until the Worker listener accepts
// a TLS handshake that the test CA vouches for.
func (e *env) startControl(t *testing.T) *proc {
	t.Helper()
	p := startProc(t, "eki-control", "--config", e.cfgPath)
	pool := x509.NewCertPool()
	ca, err := os.ReadFile(e.pki.caFile)
	must(t, err)
	pool.AppendCertsFromPEM(ca)
	addr := strings.TrimPrefix(e.workerURL, "https://")
	waitFor(t, "eki-control Worker listener", func() (bool, string) {
		if !p.running() {
			t.Fatalf("eki-control exited early: %v\n%s", p.err, p.out.String())
		}
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, &tls.Config{RootCAs: pool})
		if err != nil {
			return false, err.Error()
		}
		_ = conn.Close()
		return true, ""
	})
	return p
}

func (e *env) startAgent(t *testing.T, name, queueDB, worker, token string) *proc {
	t.Helper()
	tokenFile := filepath.Join(e.dir, name+".token")
	must(t, os.WriteFile(tokenFile, []byte(token+"\n"), 0o600))
	return startProc(t, "eki-agent",
		"--control-url", e.workerURL, "--queue-db", queueDB, "--ca", e.pki.caFile,
		"--token-file", tokenFile, "--worker-identity", worker, "--trust-domain", "example.org",
		"--interval", "100ms", "--retry-base", "100ms", "--retry-max", "500ms")
}

// ---- seeding and observing state -------------------------------------------

func openState(t *testing.T, path string) *statesqlite.Store {
	t.Helper()
	s, err := statesqlite.Open(context.Background(), path)
	must(t, err)
	return s
}

// seedPolicy activates the policy the Leases are reserved under.
func seedPolicy(t *testing.T, s *statesqlite.Store, now time.Time) policystate.State {
	t.Helper()
	policy, err := policystate.NewActive(1, "policy-1", now.Add(-time.Hour))
	must(t, err)
	must(t, s.CompareAndSwapPolicy(context.Background(), 0, policy))
	return policy
}

// seedClaim drives a Task/Attempt/Lease through the store APIs to a
// dispatched Attempt held by worker-a and returns the completed ResultClaim
// that Worker would send. The Placement inputs are synthetic.
func seedClaim(t *testing.T, s *statesqlite.Store, policy policystate.State, n int, now time.Time) resultclaim.Claim {
	t.Helper()
	return seedClaimFor(t, s, policy, n, now, "worker-a")
}

// seedClaimFor is seedClaim for a Worker with the given identity. The mTLS
// tests use the identity the registry issued at enrollment.
func seedClaimFor(t *testing.T, s *statesqlite.Store, policy policystate.State, n int, now time.Time, worker string) resultclaim.Claim {
	t.Helper()
	ctx := context.Background()
	task, err := taskstate.NewTask(ids.TaskID(fmt.Sprintf("tsk_%032x", n)), contract.SHA256([]byte(fmt.Sprintf("taskir-%d", n))), now.Add(-time.Minute))
	must(t, err)
	task.Phase = taskstate.TaskPhaseReady
	must(t, s.CreateTask(ctx, task))
	attempt, err := attemptstate.New(ids.AttemptID(fmt.Sprintf("atm_%032x", n)), task.ID, task.TaskIRDigest, 1, task.CreatedAt)
	must(t, err)
	must(t, s.CreateAttempt(ctx, attempt))
	placing := attempt
	placing.Phase, placing.WorkerIdentity = attemptstate.AttemptPhasePlacing, worker
	placing.Revision++
	must(t, s.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, placing))
	reservation, err := s.ReserveLease(ctx, statestore.LeaseReservation{
		LeaseID: ids.LeaseID(fmt.Sprintf("les_%032x", n)), TaskID: task.ID, TaskIRDigest: task.TaskIRDigest,
		ExpectedTaskRevision: task.Revision, AttemptID: attempt.ID, ExpectedAttemptRevision: placing.Revision,
		WorkerIdentity: worker, PlacementTrustDomain: "example.org", PlacementAuthorityPrincipal: "placement-1",
		PlacementDecisionID: fmt.Sprintf("decision-%d", n), PlacementPayloadDigest: contract.SHA256([]byte("placement")),
		PlacementExpiresAt: now.Add(2 * time.Hour), PolicyRevision: policy.Revision, ExpectedPolicyEpoch: policy.Epoch,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Hour),
	})
	must(t, err)
	assigned, err := s.LoadAttempt(ctx, attempt.ID)
	must(t, err)
	dispatched := assigned
	dispatched.Phase, dispatched.UpdatedAt = attemptstate.AttemptPhaseDispatched, now
	dispatched.Revision++
	must(t, s.CompareAndSwapAttempt(ctx, attempt.ID, assigned.Revision, dispatched))

	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: task.ID, TaskIRDigest: task.TaskIRDigest, AttemptID: attempt.ID, WorkerIdentity: worker,
		LeaseID: reservation.Lease.ID, FenceEpoch: reservation.Lease.FenceEpoch, WorkOrderDecision: "workorder-1",
		ExecutorDigest: contract.SHA256([]byte("executor")), Nonce: []byte("0123456789abcdef"),
		Status: resultclaim.StatusCompleted, OutputBytes: 42, OutputDigest: contract.SHA256([]byte("output")),
		StartedAt: now.Add(-2 * time.Second), EndedAt: now.Add(-time.Second),
	})
	must(t, err)
	return claim
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

type nopSender struct{}

func (nopSender) Send(context.Context, resultclaim.Claim) error { return errors.New("not used") }

// enqueue puts a claim into an Agent's queue file. The test only enqueues; the
// agent process delivers. The file is closed again so the agent is the only
// long-lived user.
func enqueue(t *testing.T, queueDB string, claim resultclaim.Claim) {
	t.Helper()
	store, err := queuesqlite.Open(context.Background(), queueDB)
	must(t, err)
	defer store.Close()
	q, err := claimqueue.New(claimqueue.Config{Store: store, Sender: nopSender{}, Clock: wallClock{}, BaseDelay: time.Second, MaxDelay: time.Minute})
	must(t, err)
	dup, err := q.Enqueue(context.Background(), claim)
	must(t, err)
	if dup {
		t.Fatal("claim was already queued")
	}
}

func queueEntry(t *testing.T, queueDB string, claim resultclaim.Claim) (claimqueue.Entry, bool) {
	t.Helper()
	store, err := queuesqlite.Open(context.Background(), queueDB)
	must(t, err)
	defer store.Close()
	entry, found, err := store.Load(context.Background(), claim.AttemptID())
	must(t, err)
	return entry, found
}

// controlView is what the Control Plane durably holds for one Attempt.
type controlView struct {
	phase   attemptstate.AttemptPhase
	rev     uint64
	claimed bool
}

func viewOf(t *testing.T, statePath string, claim resultclaim.Claim) controlView {
	t.Helper()
	s := openState(t, statePath)
	defer s.Close()
	a, err := s.LoadAttempt(context.Background(), claim.AttemptID())
	must(t, err)
	_, found, err := s.LoadResultClaim(context.Background(), claim.AttemptID())
	must(t, err)
	return controlView{a.Phase, a.Revision, found}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	return &env{
		dir: dir, pki: newPKI(t, dir), controlDB: filepath.Join(dir, "control.db"),
		cfgPath: filepath.Join(dir, "control.json"), workerURL: "https://" + freeAddr(t),
	}
}

// ---- tests ------------------------------------------------------------------

func TestAgentDeliversClaimToControlAcrossControlRestart(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC()
	state := openState(t, e.controlDB)
	policy := seedPolicy(t, state, now)
	first, second := seedClaim(t, state, policy, 1, now), seedClaim(t, state, policy, 2, now)
	must(t, state.Close())
	e.writeControlConfig(t)

	control := e.startControl(t)
	queueDB := filepath.Join(e.dir, "agent.db")
	agent := e.startAgent(t, "worker-a", queueDB, "worker-a", workerAToken)

	enqueue(t, queueDB, first)
	waitFor(t, "first claim delivered", func() (bool, string) {
		_, found := queueEntry(t, queueDB, first)
		return !found, "still queued"
	})
	got := viewOf(t, e.controlDB, first)
	if got.phase != attemptstate.AttemptPhaseVerifying || !got.claimed {
		t.Fatalf("control Attempt after delivery = %+v, want verifying with the claim recorded", got)
	}

	// Kill the Control Plane without a clean shutdown. The next claim must be
	// kept by the agent, with a recorded failed attempt.
	control.kill()
	enqueue(t, queueDB, second)
	waitFor(t, "second claim retried and retained", func() (bool, string) {
		entry, found := queueEntry(t, queueDB, second)
		if !found {
			t.Fatal("claim left the queue while the control plane was down")
		}
		return entry.Attempts >= 2 && entry.LastError != "", fmt.Sprintf("attempts=%d", entry.Attempts)
	})
	if !agent.running() {
		t.Fatalf("agent exited while control was down:\n%s", agent.out.String())
	}
	if v := viewOf(t, e.controlDB, second); v.phase != attemptstate.AttemptPhaseDispatched || v.claimed {
		t.Fatalf("control changed without a delivery: %+v", v)
	}

	// Restart control over the same state file: the retained claim arrives.
	e.startControl(t)
	waitFor(t, "second claim delivered after control restart", func() (bool, string) {
		_, found := queueEntry(t, queueDB, second)
		return !found, "still queued"
	})
	if v := viewOf(t, e.controlDB, second); v.phase != attemptstate.AttemptPhaseVerifying || !v.claimed {
		t.Fatalf("control Attempt after restart delivery = %+v", v)
	}
	// The first claim's state survived the kill.
	if v := viewOf(t, e.controlDB, first); v.phase != attemptstate.AttemptPhaseVerifying {
		t.Fatalf("first Attempt after control restart = %+v", v)
	}

	// Clean agent shutdown on interrupt.
	agent.interrupt(t)
	if agent.err != nil {
		t.Fatalf("agent exit after interrupt: %v\n%s", agent.err, agent.out.String())
	}
}

func TestControlRejectsUnknownTokenAndWrongPrincipalWithoutChange(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC()
	state := openState(t, e.controlDB)
	policy := seedPolicy(t, state, now)
	claim := seedClaim(t, state, policy, 1, now)
	must(t, state.Close())
	e.writeControlConfig(t)
	control := e.startControl(t)

	cases := []struct {
		name, worker, token, wantStatus string
	}{
		// A token the table does not list.
		{"unknown token", "worker-a", "test-profile-token-nobody-0123456789", "HTTP 401"},
		// A valid session of worker-b carrying a claim that names worker-a.
		{"wrong principal", "worker-a", workerBToken, "HTTP 403"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			queueDB := filepath.Join(e.dir, strings.ReplaceAll(tc.name, " ", "-")+".db")
			agent := e.startAgent(t, strings.ReplaceAll(tc.name, " ", "-"), queueDB, tc.worker, tc.token)
			enqueue(t, queueDB, claim)
			waitFor(t, "rejection recorded by the agent", func() (bool, string) {
				entry, found := queueEntry(t, queueDB, claim)
				if !found {
					t.Fatal("a rejected claim left the agent queue")
				}
				return entry.Attempts >= 1 && strings.Contains(entry.LastError, tc.wantStatus), entry.LastError
			})
			agent.kill()
			if v := viewOf(t, e.controlDB, claim); v.phase != attemptstate.AttemptPhaseDispatched || v.claimed {
				t.Fatalf("control changed after a rejected delivery: %+v", v)
			}
			if out := control.out.String(); strings.Contains(out, tc.token) {
				t.Fatalf("control log leaks the token:\n%s", out)
			}
		})
	}
}

// offLoopbackConfig writes an eki-control configuration whose Worker listener
// is on 0.0.0.0 with the given extra worker fields (raw JSON, may be empty).
func (e *env) offLoopbackConfig(t *testing.T, workerPort, extraWorker string) {
	t.Helper()
	cfg := fmt.Sprintf(`{"listen":"127.0.0.1:0","database":%q,"tokens":[{"principal":"alice","role":"client","token_sha256":%q}],
"policy":{"approved_executors":["dev.example.transform"],"per_task":{"cpu_millis":1000,"memory_bytes":1,"wall_time_seconds":60,"output_bytes_max":1,"network_bytes_max":1,"temporary_disk_bytes":1},"max_attempts":3,"max_outstanding_tasks":4},
"worker":{"listen":"0.0.0.0:%s",%s"sessions":[{"worker_identity":"worker-a","session_token_sha256":%q}]}}`,
		e.controlDB, clientapi.HashToken(clientToken), workerPort, extraWorker, clientapi.HashToken(workerAToken))
	must(t, os.WriteFile(e.cfgPath, []byte(cfg), 0o600))
}

func TestControlRefusesTestProfileOffLoopbackByDefault(t *testing.T) {
	e := newEnv(t)
	tlsField := fmt.Sprintf(`"tls":{"cert_file":%q,"key_file":%q},`, e.pki.certFile, e.pki.keyFile)
	e.offLoopbackConfig(t, "0", tlsField) // even with TLS
	p := startProc(t, "eki-control", "--config", e.cfgPath)
	select {
	case <-p.exited:
	case <-time.After(30 * time.Second):
		t.Fatal("eki-control kept running with a test-profile Worker listener off loopback")
	}
	var exit *exec.ExitError
	if !errors.As(p.err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("exit = %v, want a failure\n%s", p.err, p.out.String())
	}
	if !strings.Contains(p.out.String(), "test profile") || !strings.Contains(p.out.String(), "ADR-0027") {
		t.Fatalf("unexpected refusal text:\n%s", p.out.String())
	}
}

func TestControlAllowsTestProfileOffLoopbackOnlyWhenExplicitAndWarns(t *testing.T) {
	e := newEnv(t)
	_, port, err := net.SplitHostPort(freeAddr(t))
	must(t, err)
	tlsField := fmt.Sprintf(`"tls":{"cert_file":%q,"key_file":%q},"allow_test_profile_off_loopback":true,`, e.pki.certFile, e.pki.keyFile)
	e.offLoopbackConfig(t, port, tlsField)
	p := startProc(t, "eki-control", "--config", e.cfgPath)
	waitFor(t, "eki-control warning for the off-loopback test profile", func() (bool, string) {
		if !p.running() {
			t.Fatalf("eki-control exited early: %v\n%s", p.err, p.out.String())
		}
		return strings.Contains(p.out.String(), "TEST PROFILE"), p.out.String()
	})
}

func TestControlRefusesPlaintextWorkerListenerOffLoopback(t *testing.T) {
	e := newEnv(t)
	// The explicit opt-in does not lift the TLS requirement.
	e.offLoopbackConfig(t, "0", `"allow_test_profile_off_loopback":true,`)
	p := startProc(t, "eki-control", "--config", e.cfgPath)
	select {
	case <-p.exited:
	case <-time.After(30 * time.Second):
		t.Fatal("eki-control kept running with a plaintext Worker listener off loopback")
	}
	var exit *exec.ExitError
	if !errors.As(p.err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("exit = %v, want a failure\n%s", p.err, p.out.String())
	}
	if !strings.Contains(p.out.String(), "needs tls") {
		t.Fatalf("unexpected refusal text:\n%s", p.out.String())
	}
}
