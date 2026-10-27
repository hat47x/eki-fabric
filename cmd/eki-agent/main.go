// Command eki-agent is the Worker-side process.
//
// Today it delivers the ResultClaims in its durable queue to a Control Plane.
// The Worker Protocol (#198) is undecided, so the body format is the
// process-level test profile. Authentication is not: "eki-agent enroll"
// registers the device with a one-time credential and a pinned Control Plane,
// and the run mode then authenticates with a client certificate it renews
// itself (ADR-0027, mTLS). The static bearer token remains for the test
// profile only. The Agent does not execute Tasks.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hat47x/eki-fabric/internal/agent/service"
)

const tokenEnv = "EKI_AGENT_TOKEN"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "enroll" {
		return enrollCommand(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("eki-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the development version and exit")
	configPath := fs.String("config", "", "path to a JSON configuration file; flags override its values")
	controlURL := fs.String("control-url", "", "base URL of the Control Plane Worker listener, https://host:port")
	queueDB := fs.String("queue-db", "", "path of the Agent's local claim queue database")
	caFile := fs.String("ca", "", "PEM file of the CA that vouches for the Control Plane (default: system roots)")
	token := fs.String("token", "", "session token (visible in the process list; prefer --token-file or "+tokenEnv+")")
	tokenFile := fs.String("token-file", "", "file holding the session token")
	worker := fs.String("worker-identity", "", "Worker identity the token authenticates as")
	stateDir := fs.String("state-dir", "", "directory written by \"eki-agent enroll\"; authenticates with its client certificate (mTLS) instead of a token")
	trustDomain := fs.String("trust-domain", "", "Trust Domain of the Worker identity")
	interval := fs.Duration("interval", 0, "how often the queue is checked (default 2s)")
	retryBase := fs.Duration("retry-base", 0, "first retry delay after a failed delivery (default 1s)")
	retryMax := fs.Duration("retry-max", 0, "longest retry delay (default 1m)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "eki-agent dev")
		return 0
	}

	var cfg service.Config
	if *configPath != "" {
		loaded, err := loadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(stderr, "eki-agent: %v\n", err)
			return 2
		}
		cfg = loaded
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	override := func(name string, apply func()) {
		if set[name] {
			apply()
		}
	}
	override("control-url", func() { cfg.ControlURL = *controlURL })
	override("queue-db", func() { cfg.QueueDB = *queueDB })
	override("ca", func() { cfg.CAFile = *caFile })
	override("token", func() { cfg.Token = *token })
	override("token-file", func() { cfg.TokenFile = *tokenFile })
	override("worker-identity", func() { cfg.WorkerIdentity = *worker })
	override("state-dir", func() { cfg.StateDir = *stateDir })
	override("trust-domain", func() { cfg.TrustDomain = *trustDomain })
	override("interval", func() { cfg.Interval = *interval })
	override("retry-base", func() { cfg.RetryBase = *retryBase })
	override("retry-max", func() { cfg.RetryMax = *retryMax })

	if cfg.ControlURL == "" && cfg.QueueDB == "" && *configPath == "" {
		fmt.Fprintln(stdout, "eki-agent: no worker session configured (pass --control-url and --queue-db, or --config)")
		return 0
	}
	var secret string
	if cfg.StateDir == "" {
		var err error
		secret, err = resolveToken(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "eki-agent: %v\n", err)
			return 2
		}
	}

	log := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := service.Run(ctx, cfg, secret, log); err != nil {
		if errors.Is(err, service.ErrInvalidConfig) {
			fmt.Fprintf(stderr, "eki-agent: %v\n", err)
			return 2
		}
		log.Error("agent stopped with an error", "error", err.Error())
		return 1
	}
	return 0
}

// resolveToken takes the token from exactly one of the flag/config value, a
// file, or the environment. It never prints the token.
func resolveToken(cfg service.Config) (string, error) {
	sources := 0
	var secret string
	if cfg.Token != "" {
		sources++
		secret = cfg.Token
	}
	if cfg.TokenFile != "" {
		sources++
		raw, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return "", fmt.Errorf("read token file: %w", err)
		}
		secret = strings.TrimSpace(string(raw))
	}
	if sources == 0 {
		if env := os.Getenv(tokenEnv); env != "" {
			sources++
			secret = env
		}
	}
	if sources != 1 {
		return "", fmt.Errorf("give exactly one of --token, --token-file or %s", tokenEnv)
	}
	return secret, nil
}

const maxConfigBytes = 1 << 20

func loadConfig(path string) (service.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return service.Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return service.Config{}, fmt.Errorf("read configuration: %w", err)
	}
	if len(raw) > maxConfigBytes {
		return service.Config{}, errors.New("configuration file is too large")
	}
	var cfg service.Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return service.Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return service.Config{}, errors.New("invalid configuration: trailing data")
	}
	return cfg, nil
}
