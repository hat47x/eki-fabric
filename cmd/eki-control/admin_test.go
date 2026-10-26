package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/control/service"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

type adminEnv struct {
	dir, cfg, db, serverPEM string
}

func newAdminEnv(t *testing.T) adminEnv {
	t.Helper()
	dir := t.TempDir()
	e := adminEnv{dir: dir, cfg: filepath.Join(dir, "control.json"), db: filepath.Join(dir, "control.db")}
	var out, errOut bytes.Buffer
	args := []string{"gen-worker-ca", "--trust-domain", "example.org", "--cert-out", filepath.Join(dir, "worker-ca.pem"), "--key-out", filepath.Join(dir, "worker-ca.key")}
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("gen-worker-ca = %d %s", code, errOut.String())
	}
	// The listener certificate is not read for anything but its fingerprint
	// here; a CA certificate stands in for it.
	e.serverPEM = filepath.Join(dir, "server.pem")
	raw, _ := os.ReadFile(filepath.Join(dir, "worker-ca.pem"))
	if err := os.WriteFile(e.serverPEM, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := service.Config{
		Listen: "127.0.0.1:0", Database: e.db,
		Tokens: []service.TokenConfig{{Principal: "alice", Role: "client", TokenSHA256: clientapi.HashToken("client-token-0123456789")}},
		Policy: service.PolicyConfig{
			ApprovedExecutors: []string{"dev.example.transform"},
			PerTask:           service.PerTaskConfig{CPUMillis: 1, MemoryBytes: 1, WallTimeSeconds: 1, OutputBytesMax: 1, NetworkBytesMax: 1, TemporaryDiskBytes: 1},
			MaxAttempts:       1, MaxOutstandingTasks: 1,
		},
		Worker: &service.WorkerConfig{
			Listen: "127.0.0.1:0", Auth: service.WorkerAuthMTLS,
			TLS:  &service.TLSConfig{CertFile: e.serverPEM, KeyFile: filepath.Join(dir, "server.key")},
			MTLS: &service.WorkerMTLSConfig{TrustDomain: "example.org", CACertFile: filepath.Join(dir, "worker-ca.pem"), CAKeyFile: filepath.Join(dir, "worker-ca.key")},
		},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.cfg, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

func field(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && k == name {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func TestGenWorkerCARefusesToOverwriteAKey(t *testing.T) {
	e := newAdminEnv(t)
	var out, errOut bytes.Buffer
	args := []string{"gen-worker-ca", "--trust-domain", "example.org", "--cert-out", filepath.Join(e.dir, "again.pem"), "--key-out", filepath.Join(e.dir, "worker-ca.key")}
	if code := run(args, &out, &errOut); code == 0 {
		t.Fatal("an existing CA key was overwritten")
	}
	if _, err := os.Stat(filepath.Join(e.dir, "again.pem")); err == nil {
		t.Fatal("a certificate was written for a key that was not")
	}
	if code := run([]string{"gen-worker-ca", "--trust-domain", "Bad Domain", "--cert-out", "x", "--key-out", "y"}, &out, &errOut); code == 0 {
		t.Fatal("an invalid trust domain was accepted")
	}
	if code := run([]string{"gen-worker-ca"}, &out, &errOut); code != 2 {
		t.Fatalf("missing flags = %d, want 2", code)
	}
}

func TestEnrollTokenPrintsTheSecretOnceAndStoresOnlyItsHash(t *testing.T) {
	e := newAdminEnv(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"enroll-token", "--config", e.cfg, "--ttl", "2h"}, &out, &errOut); code != 0 {
		t.Fatalf("enroll-token = %d %s", code, errOut.String())
	}
	secret := field(out.String(), "enrollment_token")
	if !strings.HasPrefix(secret, workerregistry.SecretPrefix) {
		t.Fatalf("token %q lacks the prefix; output:\n%s", secret, out.String())
	}
	if field(out.String(), "trust_domain") != "example.org" || len(field(out.String(), "ca_fingerprint")) != 64 {
		t.Fatalf("output:\n%s", out.String())
	}
	if strings.Contains(errOut.String(), secret) {
		t.Fatal("the token is on stderr")
	}

	// The token is usable once by the registry, and the database holds no copy.
	store, err := sqlite.Open(context.Background(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := workerenrollment.New(workerenrollment.Config{Store: store, Clock: wallClock{}})
	if _, err := svc.ConsumeEnrollmentCredential(context.Background(), secret, "example.org"); err != nil {
		t.Fatalf("issued credential: %v", err)
	}
	if _, err := svc.ConsumeEnrollmentCredential(context.Background(), secret, "example.org"); err == nil {
		t.Fatal("a credential was used twice")
	}
	store.Close()
	raw, _ := os.ReadFile(e.db)
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("the database file contains the plaintext token")
	}
}

func TestEnrollTokenRefusesWrongDomainBadTTLAndAStaticProfile(t *testing.T) {
	e := newAdminEnv(t)
	for name, args := range map[string][]string{
		"other trust domain": {"enroll-token", "--config", e.cfg, "--trust-domain", "other.example"},
		"ttl over the limit": {"enroll-token", "--config", e.cfg, "--ttl", "200h"},
		"no config":          {"enroll-token"},
	} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code == 0 {
			t.Errorf("%s: succeeded", name)
		}
		if strings.Contains(out.String(), workerregistry.SecretPrefix) {
			t.Errorf("%s: a token was printed", name)
		}
	}
	// A configuration without the mTLS listener has nothing to enroll into.
	raw, _ := os.ReadFile(e.cfg)
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	delete(cfg, "worker")
	raw, _ = json.Marshal(cfg)
	noWorker := filepath.Join(e.dir, "noworker.json")
	_ = os.WriteFile(noWorker, raw, 0o600)
	var out, errOut bytes.Buffer
	if code := run([]string{"enroll-token", "--config", noWorker}, &out, &errOut); code == 0 {
		t.Fatal("a credential was issued without an mTLS Worker listener")
	}
}

func TestWorkerStateRevokesAndRevocationIsFinal(t *testing.T) {
	e := newAdminEnv(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"enroll-token", "--config", e.cfg}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	store, err := sqlite.Open(context.Background(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := workerenrollment.New(workerenrollment.Config{Store: store, Clock: wallClock{}})
	w, err := svc.ConsumeEnrollmentCredential(context.Background(), field(out.String(), "enrollment_token"), "example.org")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	for _, step := range []struct {
		state string
		ok    bool
	}{{"disabled", true}, {"registered", true}, {"revoked", true}, {"registered", false}, {"unknown", false}} {
		out.Reset()
		errOut.Reset()
		code := run([]string{"worker-state", "--config", e.cfg, "--worker", string(w.Identity), "--state", step.state}, &out, &errOut)
		if (code == 0) != step.ok {
			t.Fatalf("worker-state %s = %d (%s %s)", step.state, code, out.String(), errOut.String())
		}
	}
	if code := run([]string{"worker-state", "--config", e.cfg, "--worker", "worker-a", "--state", "revoked"}, &out, &errOut); code != 2 {
		t.Fatalf("a non-registry identity = %d, want a usage error", code)
	}
}
