package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
	"github.com/hat47x/eki-fabric/internal/control/service"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workercert"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

// The subcommands in this file are operator-local administrative actions
// (ADR-0027 section 2.2 item 4). They open the State Store file named in the
// configuration directly, as the operator of the host, and are not reachable
// from the Client API or the Worker listener. Whoever can run them can already
// read the database, so they are as trusted as the host account.
//
// The admin identity they record is a label for the audit trail
// ("admin:<label>"), not an authentication.

const defaultAdminLabel = "operator"

var errUsage = errors.New("usage")

func adminCommand(name string, args []string, stdout, stderr io.Writer) (int, bool) {
	var run func([]string, io.Writer, io.Writer) error
	switch name {
	case "gen-worker-ca":
		run = genWorkerCA
	case "enroll-token":
		run = enrollToken
	case "worker-state":
		run = workerState
	default:
		return 0, false
	}
	if err := run(args, stdout, stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, true
		}
		fmt.Fprintf(stderr, "eki-control %s: %v\n", name, err)
		if errors.Is(err, errUsage) {
			return 2, true
		}
		return 1, true
	}
	return 0, true
}

// genWorkerCA writes a new Worker CA: a key and a self-signed certificate that
// can sign client certificates for one trust domain only.
func genWorkerCA(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("gen-worker-ca", flag.ContinueOnError)
	fs.SetOutput(stderr)
	trustDomain := fs.String("trust-domain", "", "trust domain the CA may sign for (required)")
	certOut := fs.String("cert-out", "", "path to write the CA certificate (required)")
	keyOut := fs.String("key-out", "", "path to write the CA private key, mode 0600 (required)")
	days := fs.Int("validity-days", int(workerca.DefaultCAValidity/(24*time.Hour)), "lifetime of the CA certificate in days")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *trustDomain == "" || *certOut == "" || *keyOut == "" || *days <= 0 || fs.NArg() != 0 {
		return fmt.Errorf("%w: --trust-domain, --cert-out and --key-out are required", errUsage)
	}
	certPEM, keyPEM, err := workerca.GenerateCA(*trustDomain, time.Duration(*days)*24*time.Hour, time.Now())
	if err != nil {
		return err
	}
	// The key first, and never over an existing file: an existing CA key is
	// not replaced by accident.
	if err := writeNew(*keyOut, keyPEM, 0o600); err != nil {
		return err
	}
	if err := writeNew(*certOut, certPEM, 0o644); err != nil {
		_ = os.Remove(*keyOut)
		return err
	}
	fmt.Fprintf(stdout, "worker CA for trust domain %s written\n", *trustDomain)
	fmt.Fprintf(stdout, "certificate: %s\nkey:         %s\n", *certOut, *keyOut)
	fmt.Fprintln(stderr, "Keep the CA key off shared storage and separate from the Lease and WorkOrder signing keys. Anyone who holds it can create Workers of this trust domain.")
	return nil
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// registryFromConfig opens the State Store of a Control Plane configured with
// Worker auth "mtls".
func registryFromConfig(ctx context.Context, path string) (service.Config, *sqlite.Store, *workerenrollment.Service, error) {
	cfg, err := service.LoadConfig(path)
	if err != nil {
		return service.Config{}, nil, nil, err
	}
	if cfg.Worker == nil || cfg.Worker.Auth != service.WorkerAuthMTLS || cfg.Worker.MTLS == nil {
		return service.Config{}, nil, nil, errors.New("the configuration has no Worker listener with auth \"mtls\"")
	}
	store, err := sqlite.Open(ctx, cfg.Database)
	if err != nil {
		return service.Config{}, nil, nil, fmt.Errorf("open state store: %w", err)
	}
	svc, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: wallClock{}})
	if err != nil {
		_ = store.Close()
		return service.Config{}, nil, nil, err
	}
	return cfg, store, svc, nil
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

// enrollToken issues one enrollment credential and prints it once, with the
// fingerprint the Worker pins the Control Plane with.
func enrollToken(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("enroll-token", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to the Control Plane configuration file (required)")
	trustDomain := fs.String("trust-domain", "", "trust domain to enroll into; defaults to the configured one and must match it")
	ttl := fs.Duration("ttl", workerregistry.DefaultCredentialTTL, fmt.Sprintf("lifetime of the credential, at most %v", workerregistry.MaxCredentialTTL))
	label := fs.String("admin", defaultAdminLabel, "label recorded as the issuer, admin:<label>")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || fs.NArg() != 0 {
		return fmt.Errorf("%w: --config is required", errUsage)
	}
	ctx := context.Background()
	cfg, store, svc, err := registryFromConfig(ctx, *configPath)
	if err != nil {
		return err
	}
	defer store.Close()
	domain := cfg.Worker.MTLS.TrustDomain
	if *trustDomain != "" && *trustDomain != domain {
		return fmt.Errorf("%w: --trust-domain %q differs from the configured %q; the listener would refuse the credential", errUsage, *trustDomain, domain)
	}
	if cfg.Worker.TLS == nil {
		return errors.New("the Worker listener has no TLS certificate to pin")
	}
	fingerprint, err := anchorFingerprint(cfg.Worker.TLS.CertFile)
	if err != nil {
		return err
	}
	admin, err := workerenrollment.NewAdmin(*label)
	if err != nil {
		return err
	}
	issued, err := svc.IssueEnrollmentCredential(ctx, admin, workerenrollment.IssueRequest{TrustDomain: domain, TTL: *ttl})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "credential_id:    %s\n", issued.ID)
	fmt.Fprintf(stdout, "trust_domain:     %s\n", issued.TrustDomain)
	fmt.Fprintf(stdout, "expires_at:       %s\n", issued.ExpiresAt.Format(time.RFC3339))
	fmt.Fprintf(stdout, "ca_fingerprint:   %s\n", fingerprint)
	fmt.Fprintf(stdout, "enrollment_token: %s\n", issued.Secret.Reveal())
	fmt.Fprintln(stderr, "This is the only time the token is shown; it cannot be recovered. It works once and expires at the time above.")
	fmt.Fprintln(stderr, "Give the Worker the token (in a file with mode 0600, not on a command line) and the ca_fingerprint, over a channel you trust.")
	return nil
}

// anchorFingerprint is the fingerprint of the last certificate in the Worker
// listener's TLS certificate file: the top of the chain the listener sends.
// For a file that holds only a self-signed certificate it is that certificate.
// The Agent pins whichever certificate of the presented chain has this
// fingerprint and verifies the chain up to it.
func anchorFingerprint(certFile string) (string, error) {
	raw, err := os.ReadFile(certFile)
	if err != nil {
		return "", fmt.Errorf("read the Worker listener certificate: %w", err)
	}
	var last []byte
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			last = block.Bytes
		}
	}
	if last == nil {
		return "", errors.New("the Worker listener certificate file holds no certificate")
	}
	if _, err := x509.ParseCertificate(last); err != nil {
		return "", fmt.Errorf("the Worker listener certificate: %w", err)
	}
	return workercert.Fingerprint(last), nil
}

// workerState disables, enables or revokes a Worker. The registry state is the
// source of revocation (ADR-0027 section 2.4 item 16): the Control Plane
// refuses the Worker's next request, on an existing connection too.
func workerState(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("worker-state", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to the Control Plane configuration file (required)")
	worker := fs.String("worker", "", "Worker identity, wrk_... (required)")
	state := fs.String("state", "", "registered (enable), disabled or revoked; revoked is final (required)")
	label := fs.String("admin", defaultAdminLabel, "label recorded as the actor, admin:<label>")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *worker == "" || *state == "" || fs.NArg() != 0 {
		return fmt.Errorf("%w: --config, --worker and --state are required", errUsage)
	}
	identity, err := ids.ParseWorkerIdentity(*worker)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	target := workerregistry.State(*state)
	switch target {
	case workerregistry.StateRegistered, workerregistry.StateDisabled, workerregistry.StateRevoked:
	default:
		return fmt.Errorf("%w: --state must be registered, disabled or revoked", errUsage)
	}
	ctx := context.Background()
	_, store, svc, err := registryFromConfig(ctx, *configPath)
	if err != nil {
		return err
	}
	defer store.Close()
	admin, err := workerenrollment.NewAdmin(*label)
	if err != nil {
		return err
	}
	w, err := svc.SetWorkerState(ctx, admin, identity, target)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "worker %s is %s (revision %d)\n", w.Identity, w.State, w.Revision)
	return nil
}
