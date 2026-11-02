package process

// These tests run the Worker authentication of ADR-0027 across real process
// boundaries: an operator creates a Worker CA and an enrollment credential with
// eki-control, eki-agent enrolls with it and delivers a claim over mTLS, and
// the Control Plane's registry state decides whether the Worker may act.
//
// What a pass shows: the credential, the CSR, the certificate, the per-request
// registry check and the renewal work together in the two binaries. What it
// does not show: a hostile network, the Worker Protocol (#198), a CA key kept in
// an HSM, or that a Worker behind a TLS-terminating proxy can authenticate
// (that is an open question of the ADR).

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
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
	"github.com/hat47x/eki-fabric/internal/agent/enroll"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/control/service"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/workercert"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

const trustDomain = "example.org"

// mtlsEnv is an env whose Worker listener runs in mTLS mode.
type mtlsEnv struct {
	*env
	clientAddr   string
	workerCACert string
	workerCAKey  string
	serverChain  string
	artifactsDir string
	validity     int64
	clientTLS    bool
}

func newMTLSEnv(t *testing.T, validitySeconds int64) *mtlsEnv {
	t.Helper()
	e := &mtlsEnv{env: newEnv(t), validity: validitySeconds}
	e.artifactsDir = filepath.Join(e.dir, "artifacts")
	e.workerCACert = filepath.Join(e.dir, "worker-ca.pem")
	e.workerCAKey = filepath.Join(e.dir, "worker-ca.key")
	// The Worker CA comes from the operator command, as in a real deployment.
	out, err := exec.Command(binary("eki-control"), "gen-worker-ca", "--trust-domain", trustDomain,
		"--cert-out", e.workerCACert, "--key-out", e.workerCAKey).CombinedOutput()
	if err != nil {
		t.Fatalf("gen-worker-ca: %v\n%s", err, out)
	}
	// The listener sends its leaf and the CA that signed it; the fingerprint the
	// operator hands out is of the CA, the top of that chain.
	leaf, err := os.ReadFile(e.pki.certFile)
	must(t, err)
	ca, err := os.ReadFile(e.pki.caFile)
	must(t, err)
	e.serverChain = filepath.Join(e.dir, "server-chain.pem")
	must(t, os.WriteFile(e.serverChain, append(append([]byte{}, leaf...), ca...), 0o600))
	return e
}

func (e *mtlsEnv) writeConfig(t *testing.T) {
	t.Helper()
	e.clientAddr = freeAddr(t)
	cfg := service.Config{
		Listen: e.clientAddr, Database: e.controlDB,
		Tokens: []service.TokenConfig{{Principal: "alice", Role: "client", TokenSHA256: clientapi.HashToken(clientToken)}},
		Policy: service.PolicyConfig{
			ApprovedExecutors: []string{"dev.example.transform"},
			PerTask:           service.PerTaskConfig{CPUMillis: 1000, MemoryBytes: 1 << 28, WallTimeSeconds: 60, OutputBytesMax: 1 << 20, NetworkBytesMax: 1 << 20, TemporaryDiskBytes: 1 << 20},
			MaxAttempts:       3, MaxOutstandingTasks: 4,
		},
		Worker: &service.WorkerConfig{
			Listen: strings.TrimPrefix(e.workerURL, "https://"), Auth: service.WorkerAuthMTLS,
			TLS: &service.TLSConfig{CertFile: e.serverChain, KeyFile: e.pki.keyFile},
			ArtifactsDir: e.artifactsDir,
			MTLS: &service.WorkerMTLSConfig{
				TrustDomain: trustDomain, CACertFile: e.workerCACert, CAKeyFile: e.workerCAKey, CertValiditySeconds: e.validity,
			},
		},
	}
	if e.clientTLS {
		cfg.TLS = &service.TLSConfig{CertFile: e.pki.certFile, KeyFile: e.pki.keyFile}
	}
	raw, err := json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(e.cfgPath, raw, 0o600))
}

// issueToken runs the operator command and returns the one-time secret and the
// fingerprint it prints.
func (e *mtlsEnv) issueToken(t *testing.T) (secret, fingerprint, stderr string) {
	t.Helper()
	cmd := exec.Command(binary("eki-control"), "enroll-token", "--config", e.cfgPath, "--ttl", "1h")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("enroll-token: %v\n%s", err, errOut.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		k, v, _ := strings.Cut(line, ":")
		switch k {
		case "enrollment_token":
			secret = strings.TrimSpace(v)
		case "ca_fingerprint":
			fingerprint = strings.TrimSpace(v)
		}
	}
	if secret == "" || fingerprint == "" {
		t.Fatalf("enroll-token printed:\n%s", out.String())
	}
	return secret, fingerprint, errOut.String()
}

func (e *mtlsEnv) tokenFile(t *testing.T, name, secret string) string {
	t.Helper()
	path := filepath.Join(e.dir, name+".enroll-token")
	must(t, os.WriteFile(path, []byte(secret+"\n"), 0o600))
	return path
}

// agentEnroll runs "eki-agent enroll" and returns its combined output.
func (e *mtlsEnv) agentEnroll(t *testing.T, tokenFile, fingerprint, stateDir string) (string, error) {
	t.Helper()
	out, err := exec.Command(binary("eki-agent"), "enroll", "--control-url", e.workerURL,
		"--ca-fingerprint", fingerprint, "--token-file", tokenFile, "--state-dir", stateDir).CombinedOutput()
	return string(out), err
}

// enrollAgent issues a credential and enrolls an agent state directory. It
// returns the directory and the registry identity.
func (e *mtlsEnv) enrollAgent(t *testing.T, name string) (stateDir, worker string) {
	t.Helper()
	secret, fp, _ := e.issueToken(t)
	stateDir = filepath.Join(e.dir, name+"-state")
	if out, err := e.agentEnroll(t, e.tokenFile(t, name, secret), fp, stateDir); err != nil {
		t.Fatalf("eki-agent enroll: %v\n%s", err, out)
	}
	id, err := enroll.Load(stateDir)
	must(t, err)
	return stateDir, string(id.WorkerIdentity)
}

func (e *mtlsEnv) startMTLSAgent(t *testing.T, queueDB, stateDir string) *proc {
	t.Helper()
	return startProc(t, "eki-agent", "--control-url", e.workerURL, "--queue-db", queueDB, "--state-dir", stateDir,
		"--interval", "100ms", "--retry-base", "100ms", "--retry-max", "500ms")
}

// rawClient talks to the Worker listener directly, trusting the test CA, with
// an optional client certificate.
func (e *mtlsEnv) rawClient(t *testing.T, cert *tls.Certificate) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	ca, err := os.ReadFile(e.pki.caFile)
	must(t, err)
	pool.AppendCertsFromPEM(ca)
	cfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	tr := &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 20 * time.Second}
}

func (e *mtlsEnv) post(t *testing.T, c *http.Client, base, path, contentType, auth string, body []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(body))
	must(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	return k
}

func csrPEMFor(t *testing.T, key *ecdsa.PrivateKey, tmpl *x509.CertificateRequest) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	must(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

type rawWorker struct {
	identity string
	cert     tls.Certificate
	leaf     *x509.Certificate
}

// rawEnroll enrolls without the agent, so a test controls the CSR.
func (e *mtlsEnv) rawEnroll(t *testing.T, tmpl *x509.CertificateRequest) rawWorker {
	t.Helper()
	secret, _, _ := e.issueToken(t)
	key := newKey(t)
	status, body := e.post(t, e.rawClient(t, nil), e.workerURL, enrollwire.EnrollPath, enrollwire.CSRContentType, "Bearer "+secret, csrPEMFor(t, key, tmpl))
	if status != http.StatusOK {
		t.Fatalf("raw enroll = %d %s", status, body)
	}
	out, err := enrollwire.DecodeResponse([]byte(body))
	must(t, err)
	block, _ := pem.Decode([]byte(out.CertificatePEM))
	leaf, err := x509.ParseCertificate(block.Bytes)
	must(t, err)
	return rawWorker{identity: out.WorkerIdentity, leaf: leaf, cert: tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key}}
}

func (e *mtlsEnv) claimBody(t *testing.T, claim resultclaim.Claim) []byte {
	t.Helper()
	raw, err := testprofile.EncodeClaim(claim)
	must(t, err)
	return raw
}

func waitDelivered(t *testing.T, queueDB string, claim resultclaim.Claim) {
	t.Helper()
	waitFor(t, "claim delivered", func() (bool, string) {
		entry, found := queueEntry(t, queueDB, claim)
		return !found, entry.LastError
	})
}

func (e *mtlsEnv) registryWorker(t *testing.T, worker string) workerregistry.Worker {
	t.Helper()
	s := openState(t, e.controlDB)
	defer s.Close()
	w, err := s.LoadWorker(context.Background(), ids.WorkerIdentity(worker))
	must(t, err)
	return w
}

func (e *mtlsEnv) workerState(t *testing.T, worker, state string) {
	t.Helper()
	out, err := exec.Command(binary("eki-control"), "worker-state", "--config", e.cfgPath, "--worker", worker, "--state", state).CombinedOutput()
	if err != nil {
		t.Fatalf("worker-state: %v\n%s", err, out)
	}
}

func (e *mtlsEnv) seedPolicyNow(t *testing.T) {
	t.Helper()
	s := openState(t, e.controlDB)
	defer s.Close()
	seedPolicy(t, s, time.Now().UTC())
}

func (e *mtlsEnv) seedFor(t *testing.T, n int, worker string) resultclaim.Claim {
	t.Helper()
	s := openState(t, e.controlDB)
	defer s.Close()
	policy, err := s.LoadCurrentPolicy(context.Background())
	must(t, err)
	return seedClaimFor(t, s, policy, n, time.Now().UTC(), worker)
}

// ---- tests ------------------------------------------------------------------

func TestMTLSEnrollWithCLICredentialThenDeliverClaim(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	control := e.startControl(t)

	secret, fingerprint, errText := e.issueToken(t)
	if strings.Contains(errText, secret) {
		t.Fatal("the credential was printed on stderr")
	}
	tokenFile := e.tokenFile(t, "a", secret)
	stateDir := filepath.Join(e.dir, "a-state")
	out, err := e.agentEnroll(t, tokenFile, fingerprint, stateDir)
	if err != nil {
		t.Fatalf("eki-agent enroll: %v\n%s", err, out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("the agent printed the credential:\n%s", out)
	}
	id, err := enroll.Load(stateDir)
	must(t, err)
	if id.TrustDomain != trustDomain || !strings.HasPrefix(string(id.WorkerIdentity), "wrk_") {
		t.Fatalf("enrolled identity %+v", id)
	}
	worker := string(id.WorkerIdentity)
	if w := e.registryWorker(t, worker); !w.Usable() || w.Certificate.Serial != id.Leaf.SerialNumber.Text(16) {
		t.Fatalf("registry entry %+v does not match the issued certificate", w)
	}

	// A claim goes through mTLS and is accepted for the registry identity.
	claim := e.seedFor(t, 1, worker)
	queueDB := filepath.Join(e.dir, "a.db")
	agent := e.startMTLSAgent(t, queueDB, stateDir)
	enqueue(t, queueDB, claim)
	waitDelivered(t, queueDB, claim)
	if v := viewOf(t, e.controlDB, claim); v.phase != attemptstate.AttemptPhaseVerifying || !v.claimed {
		t.Fatalf("control Attempt after the mTLS delivery = %+v", v)
	}

	// The second use of the credential is refused, and the refusal is the same
	// as for a secret that never existed.
	if out, err := e.agentEnroll(t, tokenFile, fingerprint, filepath.Join(e.dir, "again-state")); err == nil {
		t.Fatalf("the credential enrolled twice:\n%s", out)
	}
	csr := csrPEMFor(t, newKey(t), &x509.CertificateRequest{})
	c := e.rawClient(t, nil)
	reusedStatus, reusedBody := e.post(t, c, e.workerURL, enrollwire.EnrollPath, enrollwire.CSRContentType, "Bearer "+secret, csr)
	unknown, _ := workerregistry.NewSecret()
	unknownStatus, unknownBody := e.post(t, c, e.workerURL, enrollwire.EnrollPath, enrollwire.CSRContentType, "Bearer "+unknown.Reveal(), csr)
	if reusedStatus != http.StatusUnauthorized || reusedStatus != unknownStatus || reusedBody != unknownBody {
		t.Fatalf("reused: %d %q, unknown: %d %q; want identical refusals", reusedStatus, reusedBody, unknownStatus, unknownBody)
	}
	// A secret in the query string is rejected outright.
	if status, _ := e.post(t, c, e.workerURL, enrollwire.EnrollPath+"?token="+unknown.Reveal(), enrollwire.CSRContentType, "Bearer "+unknown.Reveal(), csr); status != http.StatusBadRequest {
		t.Fatalf("a secret in the query string = %d, want 400", status)
	}

	if !agent.running() {
		t.Fatalf("agent exited:\n%s", agent.out.String())
	}
	for _, p := range []*proc{control, agent} {
		if strings.Contains(p.out.String(), secret) || strings.Contains(p.out.String(), unknown.Reveal()) {
			t.Fatalf("a process log contains an enrollment secret:\n%s", p.out.String())
		}
	}
	if !strings.Contains(control.out.String(), "reason=consumed") {
		t.Fatalf("the control log lacks the audit reason for the reused credential:\n%s", control.out.String())
	}
}

func TestMTLSWrongFingerprintAbortsEnrollmentBeforeTheSecretIsSent(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.writeConfig(t)
	control := e.startControl(t)
	secret, fingerprint, _ := e.issueToken(t)
	tokenFile := e.tokenFile(t, "w", secret)

	wrong := workercert.Fingerprint([]byte("not the control plane"))
	stateDir := filepath.Join(e.dir, "w-state")
	out, err := e.agentEnroll(t, tokenFile, wrong, stateDir)
	if err == nil {
		t.Fatalf("enrollment with the wrong fingerprint succeeded:\n%s", out)
	}
	if !strings.Contains(out, "pinned") {
		t.Fatalf("the agent did not say the pin failed:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, enroll.IdentityFile)); statErr == nil {
		t.Fatal("an identity was written")
	}
	// The Control Plane never saw an enrollment request: no refusal, no
	// enrollment. The secret is still good.
	if strings.Contains(control.out.String(), "enrollment refused") || strings.Contains(control.out.String(), "worker enrolled") {
		t.Fatalf("the Control Plane received an enrollment request:\n%s", control.out.String())
	}
	if out, err := e.agentEnroll(t, tokenFile, fingerprint, stateDir); err != nil {
		t.Fatalf("the secret was spent by the aborted enrollment: %v\n%s", err, out)
	}
}

func TestMTLSRevokedWorkerIsRefusedOnItsNextRequest(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)
	stateDir, worker := e.enrollAgent(t, "r")
	first, second, third := e.seedFor(t, 1, worker), e.seedFor(t, 2, worker), e.seedFor(t, 3, worker)

	queueDB := filepath.Join(e.dir, "r.db")
	e.startMTLSAgent(t, queueDB, stateDir)
	enqueue(t, queueDB, first)
	waitDelivered(t, queueDB, first)

	// The agent holds a keep-alive connection from the first delivery. The
	// administrator revokes the Worker; the next request on it is refused.
	e.workerState(t, worker, "revoked")
	enqueue(t, queueDB, second)
	waitFor(t, "the revoked Worker's claim refused", func() (bool, string) {
		entry, found := queueEntry(t, queueDB, second)
		if !found {
			t.Fatal("a revoked Worker's claim was accepted")
		}
		return strings.Contains(entry.LastError, "HTTP 401"), entry.LastError
	})
	if v := viewOf(t, e.controlDB, second); v.phase != attemptstate.AttemptPhaseDispatched || v.claimed {
		t.Fatalf("control changed for a revoked Worker: %+v", v)
	}
	// Revocation is final: enabling it again fails, and the claim stays refused.
	if out, err := exec.Command(binary("eki-control"), "worker-state", "--config", e.cfgPath, "--worker", worker, "--state", "registered").CombinedOutput(); err == nil {
		t.Fatalf("a revoked Worker was enabled again:\n%s", out)
	}
	enqueue(t, queueDB, third)
	waitFor(t, "the third claim refused too", func() (bool, string) {
		entry, _ := queueEntry(t, queueDB, third)
		return strings.Contains(entry.LastError, "HTTP 401"), entry.LastError
	})
	// A revoked Worker cannot renew either.
	id, err := enroll.Load(stateDir)
	must(t, err)
	if _, err := enroll.Renew(context.Background(), e.workerURL, stateDir, id); err == nil {
		t.Fatal("a revoked Worker renewed its certificate")
	}
}

func TestMTLSAgentRenewsItsCertificateAndExpiredCertificatesAreRefused(t *testing.T) {
	// A short lifetime, so renewal and expiry can be observed. The margin
	// between renewal (two thirds in) and expiry has to survive a slow or
	// stepped clock on a busy test machine, so it is not the 10 s minimum.
	const validity = 30 // seconds
	e := newMTLSEnv(t, validity)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)
	stateDir, worker := e.enrollAgent(t, "n")
	before := e.registryWorker(t, worker).Certificate.Serial

	// A second Worker that never renews: its certificate expires.
	stale := e.rawEnroll(t, &x509.CertificateRequest{})

	queueDB := filepath.Join(e.dir, "n.db")
	agent := e.startMTLSAgent(t, queueDB, stateDir)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("agent output:\n%s", agent.out.String())
		}
	})
	waitFor(t, "the agent to renew at one third of the lifetime", func() (bool, string) {
		now := e.registryWorker(t, worker).Certificate.Serial
		return now != before, now
	})
	waitFor(t, "the agent to log the renewal", func() (bool, string) {
		return strings.Contains(agent.out.String(), "worker certificate renewed"), agent.out.String()
	})
	// The renewed identity is what is on disk, and it is for the same Worker.
	id, err := enroll.Load(stateDir)
	must(t, err)
	if string(id.WorkerIdentity) != worker {
		t.Fatalf("renewal changed the identity: %s -> %s", worker, id.WorkerIdentity)
	}

	// Once the first certificates have expired, the renewed one still works.
	// Wait by the wall clock, which is what certificate validity is checked
	// against, not by a sleep that a stepped clock could shorten.
	for time.Now().Before(stale.leaf.NotAfter.Add(time.Second)) {
		time.Sleep(100 * time.Millisecond)
	}
	claim := e.seedFor(t, 1, worker)
	enqueue(t, queueDB, claim)
	waitDelivered(t, queueDB, claim)

	// The stale Worker's expired certificate is refused, for claims and for
	// renewal: an expired certificate is not revived by a later request.
	// Polled, because a wall clock that steps backwards on a busy test machine
	// can briefly make an expired certificate look valid again.
	c := e.rawClient(t, &stale.cert)
	staleClaim := e.claimBody(t, e.seedFor(t, 2, stale.identity))
	waitFor(t, "the expired certificate to be refused", func() (bool, string) {
		status, _ := e.post(t, c, e.workerURL, testprofile.Path, testprofile.ContentType, "", staleClaim)
		return status == http.StatusUnauthorized, http.StatusText(status)
	})
	if status, _ := e.post(t, c, e.workerURL, enrollwire.RenewPath, enrollwire.CSRContentType, "", csrPEMFor(t, newKey(t), &x509.CertificateRequest{})); status != http.StatusUnauthorized {
		t.Fatalf("an expired certificate renewed: %d", status)
	}
}

func TestMTLSRefusesCertificatesOfOtherCAsAndTrustDomains(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)
	victim := e.rawEnroll(t, &x509.CertificateRequest{})
	claim := e.seedFor(t, 1, victim.identity)
	body := e.claimBody(t, claim)

	loadCA := func(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey) {
		certPEM, err := os.ReadFile(certPath)
		must(t, err)
		keyPEM, err := os.ReadFile(keyPath)
		must(t, err)
		cb, _ := pem.Decode(certPEM)
		cert, err := x509.ParseCertificate(cb.Bytes)
		must(t, err)
		kb, _ := pem.Decode(keyPEM)
		k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
		must(t, err)
		return cert, k.(*ecdsa.PrivateKey)
	}
	sign := func(parent *x509.Certificate, parentKey *ecdsa.PrivateKey, uri string) tls.Certificate {
		key := newKey(t)
		u, err := url.Parse(uri)
		must(t, err)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "x"},
			NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{u},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
		must(t, err)
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	realCA, realKey := loadCA(e.workerCACert, e.workerCAKey)
	// A CA the Control Plane does not trust, for the same trust domain.
	otherCert, otherKey := tempCA(t, e, trustDomain)
	// A CA for another trust domain.
	foreignCert, foreignKey := tempCA(t, e, "other.example")
	id := victim.identity

	cases := map[string]tls.Certificate{
		"unknown CA":                    sign(otherCert, otherKey, "eki://"+trustDomain+"/worker/"+id),
		"other trust domain, other CA":  sign(foreignCert, foreignKey, "eki://other.example/worker/"+id),
		"sub-domain, signed by the CA":  sign(realCA, realKey, "eki://sub."+trustDomain+"/worker/"+id),
		"other namespace, the CA":       sign(realCA, realKey, "eki://"+trustDomain+"/admin/"+id),
		"unregistered identity, the CA": sign(realCA, realKey, "eki://"+trustDomain+"/worker/wrk_00000000000000000000000000000000"),
	}
	for name, cert := range cases {
		cert := cert
		if status, _ := e.post(t, e.rawClient(t, &cert), e.workerURL, testprofile.Path, testprofile.ContentType, "", body); status != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, status)
		}
	}
	if v := viewOf(t, e.controlDB, claim); v.claimed {
		t.Fatalf("a refused certificate changed the control state: %+v", v)
	}
	// Control: the real Worker's certificate delivers the same claim.
	if status, resp := e.post(t, e.rawClient(t, &victim.cert), e.workerURL, testprofile.Path, testprofile.ContentType, "", body); status != http.StatusOK {
		t.Fatalf("control request = %d %s", status, resp)
	}
}

// tempCA writes a throwaway Worker-style CA for domain and returns it.
func tempCA(t *testing.T, e *mtlsEnv, domain string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	certPEM, keyPEM, err := workerca.GenerateCA(domain, 0, time.Now().Add(-time.Hour))
	must(t, err)
	cb, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(cb.Bytes)
	must(t, err)
	kb, _ := pem.Decode(keyPEM)
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	must(t, err)
	return cert, k.(*ecdsa.PrivateKey)
}

func TestMTLSForgedCSRSubjectStillGetsTheRegistryIdentity(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)
	victim := e.rawEnroll(t, &x509.CertificateRequest{})
	forged := e.rawEnroll(t, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: victim.identity},
		DNSNames: []string{"eki-control"},
		URIs:     []*url.URL{{Scheme: "eki", Host: trustDomain, Path: "/worker/" + victim.identity}},
	})
	if forged.identity == victim.identity {
		t.Fatal("a forged CSR obtained another Worker's identity")
	}
	if len(forged.leaf.URIs) != 1 || forged.leaf.URIs[0].Path != "/worker/"+forged.identity || len(forged.leaf.DNSNames) != 0 {
		t.Fatalf("the certificate carries requester-chosen names: %v %v", forged.leaf.URIs, forged.leaf.DNSNames)
	}
	// With the forged Worker's certificate, the victim's claim is refused by
	// the unchanged claim ownership check.
	claim := e.seedFor(t, 1, victim.identity)
	status, body := e.post(t, e.rawClient(t, &forged.cert), e.workerURL, testprofile.Path, testprofile.ContentType, "", e.claimBody(t, claim))
	if status != http.StatusForbidden || !strings.Contains(body, "not_claim_owner") {
		t.Fatalf("victim's claim sent as the forged Worker = %d %s, want 403 not_claim_owner", status, body)
	}
	if v := viewOf(t, e.controlDB, claim); v.claimed {
		t.Fatalf("the victim's Attempt changed: %+v", v)
	}
}

// The Worker listener and the Client API are separate listeners with separate
// authenticators: neither credential is accepted by the other.
func TestMTLSWorkerAndClientCredentialsDoNotCross(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.clientTLS = true
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)
	worker := e.rawEnroll(t, &x509.CertificateRequest{})
	claimBody := e.claimBody(t, e.seedFor(t, 1, worker.identity))

	// A Client API token on the Worker listener.
	if status, _ := e.post(t, e.rawClient(t, nil), e.workerURL, testprofile.Path, testprofile.ContentType, "Bearer "+clientToken, claimBody); status != http.StatusUnauthorized {
		t.Fatalf("a Client token on the Worker listener = %d, want 401", status)
	}
	// ... with an enrollment-shaped request too: the token is not a secret.
	if status, _ := e.post(t, e.rawClient(t, nil), e.workerURL, enrollwire.EnrollPath, enrollwire.CSRContentType, "Bearer "+clientToken, csrPEMFor(t, newKey(t), &x509.CertificateRequest{})); status != http.StatusUnauthorized {
		t.Fatalf("a Client token as an enrollment secret = %d, want 401", status)
	}

	// The Worker certificate on the Client API: the certificate is not a
	// credential there. The Client API listener is TLS here.
	clientBase := "https://" + e.clientAddr
	waitFor(t, "the Client API", func() (bool, string) {
		resp, err := e.rawClient(t, nil).Get(clientBase + "/v1/tasks/tsk_00000000000000000000000000000000")
		if err != nil {
			return false, err.Error()
		}
		resp.Body.Close()
		return true, ""
	})
	for name, auth := range map[string]string{"certificate only": "", "certificate and a made-up token": "Bearer worker-cert-is-not-a-token-0123456789"} {
		status, _ := e.post(t, e.rawClient(t, &worker.cert), clientBase, "/v1/tasks", "application/json", auth, []byte(`{}`))
		if status != http.StatusUnauthorized {
			t.Errorf("%s on the Client API = %d, want 401", name, status)
		}
	}
	// Control: the Client token does work on the Client API (it is not
	// refused for authentication; the empty task is refused for its content).
	if status, _ := e.post(t, e.rawClient(t, nil), clientBase, "/v1/tasks", "application/json", "Bearer "+clientToken, []byte(`{}`)); status == http.StatusUnauthorized || status == http.StatusForbidden {
		t.Fatalf("the Client token was refused by the Client API: %d", status)
	}
}

func TestMTLSConfigurationFailsClosed(t *testing.T) {
	for name, mutate := range map[string]func(*mtlsEnv, map[string]any){
		"static table with mtls": func(e *mtlsEnv, w map[string]any) {
			w["sessions"] = []map[string]string{{"worker_identity": "worker-a", "session_token_sha256": clientapi.HashToken(workerAToken)}}
		},
		"missing CA key": func(e *mtlsEnv, w map[string]any) {
			w["mtls"].(map[string]any)["ca_key_file"] = filepath.Join(e.dir, "absent.key")
		},
		"CA for another trust domain": func(e *mtlsEnv, w map[string]any) { w["mtls"].(map[string]any)["trust_domain"] = "other.example" },
		"no tls":                      func(e *mtlsEnv, w map[string]any) { delete(w, "tls") },
	} {
		t.Run(name, func(t *testing.T) {
			e := newMTLSEnv(t, 0)
			e.writeConfig(t)
			raw, err := os.ReadFile(e.cfgPath)
			must(t, err)
			var cfg map[string]any
			must(t, json.Unmarshal(raw, &cfg))
			mutate(e, cfg["worker"].(map[string]any))
			raw, err = json.Marshal(cfg)
			must(t, err)
			must(t, os.WriteFile(e.cfgPath, raw, 0o600))
			p := startProc(t, "eki-control", "--config", e.cfgPath)
			select {
			case <-p.exited:
			case <-time.After(30 * time.Second):
				t.Fatal("eki-control kept running with an inconsistent Worker configuration")
			}
			var exit *exec.ExitError
			if !errors.As(p.err, &exit) || exit.ExitCode() == 0 {
				t.Fatalf("exit = %v, want a failure\n%s", p.err, p.out.String())
			}
		})
	}
}
