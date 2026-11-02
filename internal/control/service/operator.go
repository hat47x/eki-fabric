package service

import (
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/control/operatorapi"
)

// ErrInvalidOperatorConfig means the Operator section of the configuration is
// incomplete. ADR-0038 makes the Operator route an explicit choice, so a section
// that is half filled is a mistake rather than a default.
var ErrInvalidOperatorConfig = errors.New("invalid operator configuration")

// OperatorConfig is the Operator listener (ADR-0038). Leaving it out means the
// process has no Operator routes at all.
type OperatorConfig struct {
	// Listen is the address the Operator listener binds, for example
	// "127.0.0.1:8444".
	Listen string `json:"listen,omitempty"`
	// CAFile is the Operator CA certificate that signs Operator client
	// certificates.
	CAFile string `json:"ca_file,omitempty"`
	// CertFile and KeyFile are the listener's own certificate: without them the
	// process cannot serve TLS at all.
	CertFile string `json:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
	// TrustDomain is the domain every Operator name must belong to. Empty accepts
	// whatever domain the CA signed.
	TrustDomain string `json:"trust_domain,omitempty"`
}

// ServerConfig turns the section into what operatorapi.Build needs, refusing a
// section that is half filled.
func (c *OperatorConfig) ServerConfig() (operatorapi.ServerConfig, error) {
	if c == nil {
		return operatorapi.ServerConfig{}, ErrInvalidOperatorConfig
	}
	switch {
	case c.Listen == "":
		return operatorapi.ServerConfig{}, fmt.Errorf("%w: listen is required", ErrInvalidOperatorConfig)
	case c.CAFile == "":
		return operatorapi.ServerConfig{}, fmt.Errorf("%w: ca_file is required: an Operator route without a CA cannot prove who is calling", ErrInvalidOperatorConfig)
	case c.CertFile == "" || c.KeyFile == "":
		return operatorapi.ServerConfig{}, fmt.Errorf("%w: cert_file and key_file are required", ErrInvalidOperatorConfig)
	}
	return operatorapi.ServerConfig{
		CAPath: c.CAFile, CertFile: c.CertFile, KeyFile: c.KeyFile, TrustDomain: c.TrustDomain,
	}, nil
}
