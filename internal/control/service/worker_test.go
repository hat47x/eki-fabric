package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
)

func workerConfig(t *testing.T, mutate func(*Config)) Config {
	t.Helper()
	cfg := testConfig(t, t.TempDir())
	cfg.Worker = &WorkerConfig{
		Listen:   "127.0.0.1:0",
		Sessions: []WorkerSessionConfig{{WorkerIdentity: "worker-a", SessionTokenSHA256: clientapi.HashToken("worker-token-0123456789abcdef")}},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return cfg
}

func TestWorkerListenerConfigFailsClosed(t *testing.T) {
	cases := map[string]func(*Config){
		"plaintext off loopback": func(c *Config) { c.Worker.Listen = "0.0.0.0:9443" },
		"hostname off loopback":  func(c *Config) { c.Worker.Listen = "control.example.org:9443" },
		"no sessions":            func(c *Config) { c.Worker.Sessions = nil },
		"bad hash":               func(c *Config) { c.Worker.Sessions[0].SessionTokenSHA256 = "nothex" },
		"no identity":            func(c *Config) { c.Worker.Sessions[0].WorkerIdentity = "" },
		"half tls":               func(c *Config) { c.Worker.TLS = &TLSConfig{CertFile: "only-cert.pem"} },
		"no address":             func(c *Config) { c.Worker.Listen = "" },
		"negative body limit":    func(c *Config) { c.Worker.MaxBodyBytes = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := workerConfig(t, mutate)
			if err := cfg.Validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Validate = %v, want ErrInvalidConfig", err)
			}
			if _, err := Start(context.Background(), cfg, quietLogger()); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Start = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestWorkerListenerLoopbackDecision(t *testing.T) {
	tls := &TLSConfig{CertFile: "c.pem", KeyFile: "k.pem"}
	cases := []struct {
		listen     string
		off        bool // non-loopback
		wantNoFlag bool // valid without the flag
	}{
		{"127.0.0.1:9443", false, true},
		{"127.0.0.2:9443", false, true},
		{"[::1]:9443", false, true},
		{"localhost:9443", false, true},
		{"0.0.0.0:9443", true, false},
		{"[::]:9443", true, false},
		{":9443", true, false},
		{"192.168.1.10:9443", true, false},
		{"10.0.0.5:9443", true, false},
		{"control.example.org:9443", true, false},
		{"[fe80::1%lo]:9443", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.listen, func(t *testing.T) {
			base := func(allow bool, withTLS bool) WorkerConfig {
				w := WorkerConfig{
					Listen: tc.listen, AllowTestProfileOffLoopback: allow,
					Sessions: []WorkerSessionConfig{{WorkerIdentity: "worker-a", SessionTokenSHA256: clientapi.HashToken("worker-token-0123456789abcdef")}},
				}
				if withTLS {
					w.TLS = tls
				}
				return w
			}
			// Without the flag: only loopback is accepted, with or without TLS.
			for _, withTLS := range []bool{false, true} {
				err := base(false, withTLS).validate()
				if tc.wantNoFlag {
					if err != nil {
						t.Fatalf("loopback tls=%v: %v", withTLS, err)
					}
					continue
				}
				if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "test profile") || !strings.Contains(err.Error(), "ADR-0027") {
					t.Fatalf("off loopback without flag tls=%v: %v", withTLS, err)
				}
			}
			// With the flag: off loopback needs TLS; loopback stays valid.
			err := base(true, true).validate()
			if err != nil {
				t.Fatalf("flag with tls: %v", err)
			}
			err = base(true, false).validate()
			if tc.off {
				if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "needs tls") {
					t.Fatalf("flag without tls off loopback: %v", err)
				}
			} else if err != nil {
				t.Fatalf("flag on loopback: %v", err)
			}
		})
	}
}

func TestWorkerListenerOnLoopbackStartsAndStops(t *testing.T) {
	cfg := workerConfig(t, nil)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	s, err := Start(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if s.WorkerAddr() == "" || s.WorkerAddr() == s.Addr() {
		t.Fatalf("worker address %q, client address %q", s.WorkerAddr(), s.Addr())
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStartFailsWhenTheWorkerCertificateIsMissing(t *testing.T) {
	dir := t.TempDir()
	cfg := workerConfig(t, func(c *Config) {
		c.Worker.TLS = &TLSConfig{CertFile: filepath.Join(dir, "missing.pem"), KeyFile: filepath.Join(dir, "missing.key")}
	})
	if _, err := Start(context.Background(), cfg, quietLogger()); err == nil {
		t.Fatal("started without a Worker certificate")
	}
}

func TestNoWorkerListenerWithoutConfiguration(t *testing.T) {
	s, err := Start(context.Background(), testConfig(t, t.TempDir()), quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	if s.WorkerAddr() != "" {
		t.Fatalf("worker listener without configuration: %q", s.WorkerAddr())
	}
}

func mtlsWorkerConfig(t *testing.T, mutate func(*Config)) Config {
	t.Helper()
	cfg := testConfig(t, t.TempDir())
	cfg.Worker = &WorkerConfig{
		Listen: "0.0.0.0:9443",
		Auth:   WorkerAuthMTLS,
		TLS:    &TLSConfig{CertFile: "server.pem", KeyFile: "server.key"},
		ArtifactsDir: t.TempDir(),
		MTLS:   &WorkerMTLSConfig{TrustDomain: "example.org", CACertFile: "worker-ca.pem", CAKeyFile: "worker-ca.key"},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return cfg
}

// The mTLS mode is the one that may listen off loopback. Every inconsistent
// combination with the test profile fails closed.
func TestWorkerMTLSConfig(t *testing.T) {
	if err := mtlsWorkerConfig(t, nil).Validate(); err != nil {
		t.Fatalf("a well-formed mTLS configuration off loopback is refused: %v", err)
	}
	cases := map[string]func(*Config){
		"unknown auth":            func(c *Config) { c.Worker.Auth = "oauth" },
		"mtls without tls":        func(c *Config) { c.Worker.TLS = nil },
		"mtls without tls, local": func(c *Config) { c.Worker.TLS = nil; c.Worker.Listen = "127.0.0.1:9443" },
		"mtls without settings":   func(c *Config) { c.Worker.MTLS = nil },
		"mtls with static table": func(c *Config) {
			c.Worker.Sessions = []WorkerSessionConfig{{WorkerIdentity: "w", SessionTokenSHA256: clientapi.HashToken("worker-token-0123456789abcdef")}}
		},
		"mtls with test override": func(c *Config) { c.Worker.AllowTestProfileOffLoopback = true },
		"no CA files":             func(c *Config) { c.Worker.MTLS.CACertFile = "" },
		"no CA key":               func(c *Config) { c.Worker.MTLS.CAKeyFile = "" },
		"CA shares the TLS cert":  func(c *Config) { c.Worker.MTLS.CACertFile = c.Worker.TLS.CertFile },
		"CA shares the TLS key":   func(c *Config) { c.Worker.MTLS.CAKeyFile = c.Worker.TLS.KeyFile },
		"bad trust domain":        func(c *Config) { c.Worker.MTLS.TrustDomain = "Example.ORG" },
		"no trust domain":         func(c *Config) { c.Worker.MTLS.TrustDomain = "" },
		"validity too short":      func(c *Config) { c.Worker.MTLS.CertValiditySeconds = 1 },
		"validity too long":       func(c *Config) { c.Worker.MTLS.CertValiditySeconds = 365 * 24 * 3600 },
		"negative body limit":     func(c *Config) { c.Worker.MaxBodyBytes = -1 },
		"static with mtls block":  func(c *Config) { c.Worker.Auth = ""; c.Worker.Listen = "127.0.0.1:9443" },
		"static_token with mtls":  func(c *Config) { c.Worker.Auth = WorkerAuthStaticToken; c.Worker.Listen = "127.0.0.1:9443" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := mtlsWorkerConfig(t, mutate)
			if err := cfg.Validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Validate = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestWorkerMTLSStartFailsClosedWithoutTheCAFiles(t *testing.T) {
	cfg := mtlsWorkerConfig(t, func(c *Config) { c.Worker.Listen = "127.0.0.1:0" })
	if _, err := Start(context.Background(), cfg, quietLogger()); err == nil {
		t.Fatal("Start succeeded without a Worker CA")
	}
}

// writeWorkerCA writes a Worker CA generated at at, valid for validity.
func writeWorkerCA(t *testing.T, dir string, validity time.Duration, at time.Time) (certPath, keyPath string) {
	t.Helper()
	certPEM, keyPEM, err := workerca.GenerateCA("example.org", validity, at)
	if err != nil {
		t.Fatalf("GenerateCA() error = %v", err)
	}
	certPath = filepath.Join(dir, "worker-ca.pem")
	keyPath = filepath.Join(dir, "worker-ca.key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return certPath, keyPath
}

// mtlsConfigWithCA builds an mTLS Worker listener on loopback with a real
// server certificate and the given Worker CA files.
func mtlsConfigWithCA(t *testing.T, dir, caCert, caKey string) Config {
	t.Helper()
	certFile, keyFile, _ := selfSignedCert(t, dir)
	cfg := mtlsWorkerConfig(t, func(c *Config) {
		c.Database = filepath.Join(dir, "state.db")
		c.Worker.Listen = "127.0.0.1:0"
		c.Worker.TLS = &TLSConfig{CertFile: certFile, KeyFile: keyFile}
		c.Worker.MTLS.CACertFile = caCert
		c.Worker.MTLS.CAKeyFile = caKey
	})
	return cfg
}

// TestWorkerMTLSRefusesAnExpiredCA pins #304: an expired CA stops every
// enrollment and renewal, so it must be a startup failure rather than the first
// 401 a Worker sees.
func TestWorkerMTLSRefusesAnExpiredCA(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := writeWorkerCA(t, dir, time.Hour, time.Now().Add(-2*time.Hour))
	cfg := mtlsConfigWithCA(t, dir, caCert, caKey)
	_, err := Start(context.Background(), cfg, quietLogger())
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Start() error = %v, want %v", err, ErrInvalidConfig)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("Start() error = %v, want an expiry reason", err)
	}
}

// TestWorkerMTLSWarnsWhenTheCAExpiresSoon checks the startup warning of #304.
func TestWorkerMTLSWarnsWhenTheCAExpiresSoon(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := writeWorkerCA(t, dir, 2*time.Hour, time.Now())
	cfg := mtlsConfigWithCA(t, dir, caCert, caKey)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	s, err := Start(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer stop(t, s)
	if !strings.Contains(logs.String(), "expires soon") {
		t.Fatalf("startup did not warn about the CA expiry:\n%s", logs.String())
	}
}
