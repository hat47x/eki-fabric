// Package service composes the Control Plane process from its parts: the
// durable State Store, Task Admission, and the Client API. It owns wiring,
// configuration and lifecycle, and nothing else.
package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/core/admission"
)

const maxConfigBytes = 1 << 20

var ErrInvalidConfig = errors.New("invalid control plane configuration")

// Config is the Control Plane configuration file. Unknown fields are an error:
// a misspelled security setting must not silently fall back to a default.
type Config struct {
	// Listen is the address of the Client API, for example "127.0.0.1:8443".
	Listen string `json:"listen"`
	// Database is the path of the SQLite state file. It must be on a local
	// filesystem.
	Database string `json:"database"`

	// TLS enables HTTPS. Without it the API is plaintext, which is accepted
	// only on a loopback address unless AllowInsecureListen is set.
	TLS *TLSConfig `json:"tls,omitempty"`
	// AllowInsecureListen permits plaintext HTTP on a non-loopback address,
	// for deployments where a trusted proxy terminates TLS in front of the
	// process. It is a deliberate, visible opt-out.
	AllowInsecureListen bool `json:"allow_insecure_listen,omitempty"`

	// MaxBodyBytes bounds a request body. Zero means the default.
	MaxBodyBytes int64 `json:"max_body_bytes,omitempty"`

	// Tokens are the static credentials. Only SHA-256 hashes are stored.
	Tokens []TokenConfig `json:"tokens"`

	Policy PolicyConfig `json:"policy"`
}

type TLSConfig struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

type TokenConfig struct {
	Principal   string `json:"principal"`
	Role        string `json:"role"`
	TokenSHA256 string `json:"token_sha256"`
}

type PolicyConfig struct {
	ApprovedExecutors      []string      `json:"approved_executors"`
	PerTask                PerTaskConfig `json:"per_task"`
	MaxAttempts            uint32        `json:"max_attempts"`
	MaxOutstandingTasks    uint32        `json:"max_outstanding_tasks"`
	MaxDeadlineHorizonSecs int64         `json:"max_deadline_horizon_seconds,omitempty"`
}

type PerTaskConfig struct {
	CPUMillis          int64 `json:"cpu_millis"`
	MemoryBytes        int64 `json:"memory_bytes"`
	WallTimeSeconds    int64 `json:"wall_time_seconds"`
	OutputBytesMax     int64 `json:"output_bytes_max"`
	NetworkBytesMax    int64 `json:"network_bytes_max"`
	TemporaryDiskBytes int64 `json:"temporary_disk_bytes"`
}

// LoadConfig reads and validates a configuration file.
func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	if len(raw) > maxConfigBytes {
		return Config{}, fmt.Errorf("%w: file exceeds %d bytes", ErrInvalidConfig, maxConfigBytes)
	}
	return ParseConfig(raw)
}

// ParseConfig decodes and validates configuration bytes.
func ParseConfig(raw []byte) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%w: trailing data after the configuration object", ErrInvalidConfig)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks everything that can be checked without opening a socket or a
// database.
func (c Config) Validate() error {
	if c.Listen == "" {
		return fmt.Errorf("%w: listen is required", ErrInvalidConfig)
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("%w: listen %q is not host:port: %v", ErrInvalidConfig, c.Listen, err)
	}
	if c.Database == "" {
		return fmt.Errorf("%w: database is required", ErrInvalidConfig)
	}
	if c.TLS != nil && (c.TLS.CertFile == "" || c.TLS.KeyFile == "") {
		return fmt.Errorf("%w: tls requires both cert_file and key_file", ErrInvalidConfig)
	}
	if c.TLS == nil && !isLoopback(host) && !c.AllowInsecureListen {
		return fmt.Errorf("%w: plaintext HTTP on %q is refused; configure tls, listen on a loopback address, or set allow_insecure_listen when a trusted proxy terminates TLS", ErrInvalidConfig, c.Listen)
	}
	if c.MaxBodyBytes < 0 {
		return fmt.Errorf("%w: negative max_body_bytes", ErrInvalidConfig)
	}
	if _, err := c.authenticator(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if _, err := c.admissionPolicy(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c Config) authenticator() (*clientapi.StaticTokens, error) {
	entries := make([]clientapi.TokenEntry, 0, len(c.Tokens))
	for _, t := range c.Tokens {
		entries = append(entries, clientapi.TokenEntry{
			Principal:   t.Principal,
			Role:        clientapi.Role(t.Role),
			TokenSHA256: t.TokenSHA256,
		})
	}
	return clientapi.NewStaticTokens(entries)
}

func (c Config) admissionPolicy() (admission.Policy, error) {
	p := c.Policy
	policy := admission.Policy{
		ApprovedExecutors: p.ApprovedExecutors,
		PerTask: admission.ResourceCeiling{
			CPUMillis:          p.PerTask.CPUMillis,
			MemoryBytes:        p.PerTask.MemoryBytes,
			WallTimeSeconds:    p.PerTask.WallTimeSeconds,
			OutputBytesMax:     p.PerTask.OutputBytesMax,
			NetworkBytesMax:    p.PerTask.NetworkBytesMax,
			TemporaryDiskBytes: p.PerTask.TemporaryDiskBytes,
		},
		MaxAttempts:         p.MaxAttempts,
		MaxOutstandingTasks: p.MaxOutstandingTasks,
		MaxDeadlineHorizon:  time.Duration(p.MaxDeadlineHorizonSecs) * time.Second,
	}
	if err := admission.ValidatePolicy(policy); err != nil {
		return admission.Policy{}, err
	}
	return policy, nil
}
