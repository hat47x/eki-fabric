package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

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
