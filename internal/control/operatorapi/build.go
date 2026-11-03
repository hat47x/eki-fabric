package operatorapi

import (
	"crypto/tls"
	"fmt"
	"log/slog"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

// ServerConfig is what a process needs to serve the Operator routes (ADR-0038).
type ServerConfig struct {
	// CAPath is the Operator CA certificate. Empty means this process has no
	// Operator routes: the handler answers 404 and the returned TLS config asks
	// for no client certificate.
	CAPath string
	// CertFile and KeyFile are the server's own certificate.
	CertFile string
	KeyFile  string
	// TrustDomain is the domain every Operator name must belong to. Empty accepts
	// any domain the CA signed.
	TrustDomain string
	// Approver records approvals. Required with CAPath.
	Approver Approver
	// Book is where those approvals land, so an Operator can read them back (#545).
	Book *trustbundle.ApprovalBook
	// Admitter admits a signed version on behalf of an Administrator (#571).
	Admitter Admitter
	// Audit receives one entry per request. Nil logs instead.
	Audit func(Access)
	// Logger receives one line per request.
	Logger *slog.Logger
}

// Build assembles the Operator handler and the TLS configuration it must be served
// with. With no CA the handler is disabled and the TLS configuration is plain:
// a process without an Operator CA must not ask for a certificate it cannot
// check, and must not expose the route at all.
func Build(cfg ServerConfig) (*Handler, *tls.Config, error) {
	if cfg.CAPath == "" {
		return New(Config{Logger: cfg.Logger}), &tls.Config{MinVersion: tls.VersionTLS13}, nil
	}
	if cfg.Approver == nil {
		return nil, nil, fmt.Errorf("%w: an operator CA needs somewhere to record approvals", ErrOperatorCA)
	}
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, nil, fmt.Errorf("%w: the operator listener needs its own certificate", ErrOperatorCA)
	}
	pool, err := LoadCA(cfg.CAPath)
	if err != nil {
		return nil, nil, err
	}
	serverCert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrOperatorCA, err)
	}
	handler := New(Config{
		Authenticate: MTLSAuthenticator(pool, cfg.TrustDomain, nil),
		Approver:     cfg.Approver,
		Book:         cfg.Book,
		Admitter:     cfg.Admitter,
		Audit:        cfg.Audit,
		Logger:       cfg.Logger,
	})
	// RequireAndVerifyClientCert is the point: a request that reaches the handler
	// has already been proved to hold a certificate this CA signed.
	return handler, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}, nil
}
