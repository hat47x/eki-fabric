package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/enroll"
)

// enrollCommand registers this device as a Worker (ADR-0027 section 2.2).
//
// The private key is generated here and stays here. The enrollment secret is
// read from a file, sent once, and only after the Control Plane has presented
// the certificate whose fingerprint came with the secret.
func enrollCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eki-agent enroll", flag.ContinueOnError)
	fs.SetOutput(stderr)
	controlURL := fs.String("control-url", "", "base URL of the Control Plane Worker listener, https://host:port (required)")
	fingerprint := fs.String("ca-fingerprint", "", "SHA-256 fingerprint of the Control Plane's pinned certificate, from the administrator (required)")
	tokenFile := fs.String("token-file", "", "file holding the enrollment token, mode 0600 (required)")
	stateDir := fs.String("state-dir", "", "directory that receives the key and certificate (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *controlURL == "" || *fingerprint == "" || *tokenFile == "" || *stateDir == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "eki-agent enroll: --control-url, --ca-fingerprint, --token-file and --state-dir are required")
		return 2
	}
	raw, err := os.ReadFile(*tokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "eki-agent enroll: read the token file: %v\n", err)
		return 2
	}
	secret := strings.TrimSpace(string(raw))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	id, err := enroll.Enroll(ctx, enroll.Params{
		ControlURL: *controlURL, Fingerprint: *fingerprint, Secret: secret, StateDir: *stateDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "eki-agent enroll: %v\n", err)
		switch {
		case errors.Is(err, enroll.ErrInvalidParams), errors.Is(err, enroll.ErrIdentityExists):
			return 2
		}
		return 1
	}
	fmt.Fprintf(stdout, "enrolled as %s in trust domain %s\n", id.WorkerIdentity, id.TrustDomain)
	fmt.Fprintf(stdout, "certificate valid until %s; the agent renews it when a third of its lifetime remains\n", id.Leaf.NotAfter.Format(time.RFC3339))
	fmt.Fprintf(stdout, "identity written to %s; the token is spent and can be deleted\n", *stateDir)
	return 0
}
