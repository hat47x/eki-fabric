package service

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
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/clientapi"
)

const (
	aliceToken = "alice-token-0123456789abcdef"
	taskBody   = `{
  "schema": "eki.task-request/v1",
  "executor": {"id": "dev.example.transform", "version_constraint": "^1"},
  "inputs": [],
  "parameters": {"mode": "normalize"},
  "resources": {"cpu_millis": 1000, "memory_bytes": 268435456, "wall_time_seconds": 300, "output_bytes_max": 16777216, "network_bytes_max": 134217728},
  "reliability": {"retryable": true, "preemptible": true, "checkpointable": false, "deterministic": true, "max_attempts": 3, "effect_mode": "none"}
}`
)

func testConfig(t testing.TB, dir string) Config {
	t.Helper()
	return Config{
		Listen:   "127.0.0.1:0",
		Database: filepath.Join(dir, "state.db"),
		Tokens: []TokenConfig{
			{Principal: "alice", Role: "client", TokenSHA256: clientapi.HashToken(aliceToken)},
		},
		Policy: PolicyConfig{
			ApprovedExecutors: []string{"dev.example.transform"},
			PerTask: PerTaskConfig{
				CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeSeconds: 3600,
				OutputBytesMax: 1 << 30, NetworkBytesMax: 1 << 30, TemporaryDiskBytes: 1 << 30,
			},
			MaxAttempts:         5,
			MaxOutstandingTasks: 2,
		},
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func start(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := Start(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return s
}

func stop(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func call(t *testing.T, client *http.Client, method, url, token, key, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s error = %v", method, url, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s %s: response is not JSON: %v\n%s", method, url, err, data)
	}
	return res.StatusCode, out
}

func TestTasksSurviveARestartOfTheControlPlane(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, dir)
	client := &http.Client{Timeout: 5 * time.Second}

	first := start(t, cfg)
	base := "http://" + first.Addr()
	status, accepted := call(t, client, http.MethodPost, base+"/v1/tasks", aliceToken, "key-restart-1", taskBody)
	if status != http.StatusAccepted {
		t.Fatalf("submit status = %d: %v", status, accepted)
	}
	id := accepted["task_id"].(string)
	stop(t, first)

	// A new process on the same state file knows the task, and a client that
	// retries because it never saw the reply gets the same task, not a second.
	second := start(t, cfg)
	defer stop(t, second)
	base = "http://" + second.Addr()

	status, got := call(t, client, http.MethodGet, base+"/v1/tasks/"+id, aliceToken, "", "")
	if status != http.StatusOK || got["task_id"] != id || got["status"] != "queued" {
		t.Fatalf("status after restart = %d %v", status, got)
	}
	status, retry := call(t, client, http.MethodPost, base+"/v1/tasks", aliceToken, "key-restart-1", taskBody)
	if status != http.StatusAccepted || retry["task_id"] != id {
		t.Fatalf("retry after restart = %d %v, want the original task %s", status, retry, id)
	}

	// The quota counted the first process's work: one more fits, a second does not.
	if status, _ := call(t, client, http.MethodPost, base+"/v1/tasks", aliceToken, "key-restart-2", taskBody); status != http.StatusAccepted {
		t.Fatalf("second task status = %d", status)
	}
	status, over := call(t, client, http.MethodPost, base+"/v1/tasks", aliceToken, "key-restart-3", taskBody)
	if status != http.StatusTooManyRequests {
		t.Fatalf("third task status = %d %v, want 429 (quota is 2 and must survive a restart)", status, over)
	}

	// A cancellation also survives.
	if status, _ := call(t, client, http.MethodPost, base+"/v1/tasks/"+id+"/cancel", aliceToken, "key-cancel-1", ""); status != http.StatusAccepted {
		t.Fatalf("cancel status = %d", status)
	}
}

func TestStateFileIsNotCreatedWorldReadable(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	cfg := testConfig(t, t.TempDir())
	s := start(t, cfg)
	defer stop(t, s)
	info, err := os.Stat(cfg.Database)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state file mode = %o", info.Mode().Perm())
	}
}

func TestStartFailsClosed(t *testing.T) {
	t.Run("invalid configuration opens nothing", func(t *testing.T) {
		dir := t.TempDir()
		cfg := testConfig(t, dir)
		cfg.Tokens = nil
		if _, err := Start(context.Background(), cfg, quietLogger()); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("Start() error = %v, want ErrInvalidConfig", err)
		}
		if _, err := os.Stat(cfg.Database); err == nil {
			t.Fatal("the state file was created for an invalid configuration")
		}
	})
	t.Run("address in use releases the store", func(t *testing.T) {
		cfg := testConfig(t, t.TempDir())
		first := start(t, cfg)
		defer stop(t, first)

		clash := testConfig(t, t.TempDir())
		clash.Listen = first.Addr()
		if _, err := Start(context.Background(), clash, quietLogger()); err == nil {
			t.Fatal("Start() on an address in use succeeded")
		}
	})
	t.Run("missing tls files", func(t *testing.T) {
		cfg := testConfig(t, t.TempDir())
		cfg.TLS = &TLSConfig{CertFile: filepath.Join(t.TempDir(), "no.crt"), KeyFile: filepath.Join(t.TempDir(), "no.key")}
		if _, err := Start(context.Background(), cfg, quietLogger()); err == nil {
			t.Fatal("Start() with unreadable TLS files succeeded")
		}
	})
}

func TestShutdownIsIdempotent(t *testing.T) {
	s := start(t, testConfig(t, t.TempDir()))
	stop(t, s)
	stop(t, s)
	select {
	case <-s.Done():
	default:
		t.Fatal("Done() is not closed after Shutdown")
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v after a clean shutdown", err)
	}
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- Run(ctx, testConfig(t, t.TempDir()), quietLogger()) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not return after its context was cancelled")
	}
}

// --- configuration ---

func validConfigJSON() string {
	return fmt.Sprintf(`{
  "listen": "127.0.0.1:8443",
  "database": "/var/lib/eki/state.db",
  "tokens": [{"principal": "alice", "role": "client", "token_sha256": %q}],
  "policy": {
    "approved_executors": ["dev.example.transform"],
    "per_task": {"cpu_millis": 4000, "memory_bytes": 1073741824, "wall_time_seconds": 3600, "output_bytes_max": 1073741824, "network_bytes_max": 1073741824, "temporary_disk_bytes": 1073741824},
    "max_attempts": 5,
    "max_outstanding_tasks": 10
  }
}`, clientapi.HashToken(aliceToken))
}

func TestParseConfig(t *testing.T) {
	if _, err := ParseConfig([]byte(validConfigJSON())); err != nil {
		t.Fatalf("a valid configuration was rejected: %v", err)
	}

	valid := validConfigJSON()
	replace := func(old, new string) string {
		t.Helper()
		if !strings.Contains(valid, old) {
			t.Fatalf("test bug: %q not in the base configuration", old)
		}
		return strings.Replace(valid, old, new, 1)
	}
	cases := map[string]string{
		"unknown field (a misspelled setting)": replace(`"database"`, `"databse"`),
		"trailing data":                        valid + `{}`,
		"not json":                             `listen: 127.0.0.1:8443`,
		"no listen":                            replace(`"listen": "127.0.0.1:8443",`, ``),
		"listen without a port":                replace(`127.0.0.1:8443`, `127.0.0.1`),
		"no database":                          replace(`"database": "/var/lib/eki/state.db",`, ``),
		"plaintext on all interfaces":          replace(`127.0.0.1:8443`, `0.0.0.0:8443`),
		"plaintext on a public address":        replace(`127.0.0.1:8443`, `203.0.113.5:8443`),
		"plaintext on a hostname":              replace(`127.0.0.1:8443`, `eki.example.com:8443`),
		"tls with only a certificate":          replace(`"database"`, `"tls": {"cert_file": "a.crt"}, "database"`),
		"no tokens":                            replace(`"tokens": [{"principal": "alice", "role": "client", "token_sha256": "`+clientapi.HashToken(aliceToken)+`"}],`, `"tokens": [],`),
		"bad token hash":                       replace(clientapi.HashToken(aliceToken), "abc"),
		"unknown role":                         replace(`"role": "client"`, `"role": "superuser"`),
		"no executors":                         replace(`["dev.example.transform"]`, `[]`),
		"no outstanding cap":                   replace(`"max_outstanding_tasks": 10`, `"max_outstanding_tasks": 0`),
		"zero ceiling":                         replace(`"cpu_millis": 4000`, `"cpu_millis": 0`),
		"negative body limit":                  replace(`"database"`, `"max_body_bytes": -1, "database"`),
	}
	for name, raw := range cases {
		if _, err := ParseConfig([]byte(raw)); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: ParseConfig() error = %v, want ErrInvalidConfig", name, err)
		}
	}
}

func TestPlaintextIsAllowedOnLoopbackAndByExplicitOptOut(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:0", "[::1]:0", "localhost:0"} {
		cfg := testConfig(t, t.TempDir())
		cfg.Listen = listen
		if err := cfg.Validate(); err != nil {
			t.Errorf("loopback %s was refused: %v", listen, err)
		}
	}
	cfg := testConfig(t, t.TempDir())
	cfg.Listen = "0.0.0.0:0"
	if err := cfg.Validate(); err == nil {
		t.Fatal("plaintext on all interfaces was accepted by default")
	}
	cfg.AllowInsecureListen = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the explicit opt-out was refused: %v", err)
	}
}

func TestLoadConfigRejectsOversizedAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("a missing file was accepted")
	}
	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, bytes.Repeat([]byte(" "), maxConfigBytes+10), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadConfig(big); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("LoadConfig() error = %v, want ErrInvalidConfig", err)
	}
	ok := filepath.Join(dir, "ok.json")
	if err := os.WriteFile(ok, []byte(validConfigJSON()), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadConfig(ok); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
}

// --- TLS ---

func selfSignedCert(t *testing.T, dir string) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "eki test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}
	certFile = filepath.Join(dir, "server.crt")
	keyFile = filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

func TestServesHTTPSWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, pool := selfSignedCert(t, dir)
	cfg := testConfig(t, dir)
	cfg.TLS = &TLSConfig{CertFile: certFile, KeyFile: keyFile}

	s := start(t, cfg)
	defer stop(t, s)

	secure := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	status, body := call(t, secure, http.MethodPost, "https://"+s.Addr()+"/v1/tasks", aliceToken, "key-tls-0001", taskBody)
	if status != http.StatusAccepted {
		t.Fatalf("https submit status = %d: %v", status, body)
	}

	// Plaintext to the TLS port must not be served.
	plain := &http.Client{Timeout: 3 * time.Second}
	if res, err := plain.Get("http://" + s.Addr() + "/v1/tasks/x"); err == nil {
		res.Body.Close()
		if res.StatusCode < 400 {
			t.Fatalf("plaintext HTTP was served on a TLS port: %d", res.StatusCode)
		}
	}

	old := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MaxVersion: tls.VersionTLS11}}}
	if res, err := old.Get("https://" + s.Addr() + "/v1/tasks/x"); err == nil {
		res.Body.Close()
		t.Fatal("a TLS 1.1 handshake succeeded")
	}
}

func TestSlowClientsAreCutOff(t *testing.T) {
	s := start(t, testConfig(t, t.TempDir()))
	defer stop(t, s)

	conn, err := net.DialTimeout("tcp", s.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	// Send an incomplete request line and nothing else. ReadHeaderTimeout must
	// close the connection instead of holding it open forever.
	if _, err := conn.Write([]byte("POST /v1/tasks HTTP/1.1\r\nHost: x\r\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	buf := make([]byte, 1024)
	_, err = conn.Read(buf)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("the server kept a stalled connection open past its header timeout")
	}
}
