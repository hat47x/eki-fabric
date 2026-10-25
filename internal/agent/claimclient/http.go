package claimclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

// HTTPClient is a workerplane.ClaimClient over HTTP(S) for the process-level
// TEST PROFILE. It is not the Worker Protocol (#198) and its bearer token is
// not an enrollment decision. See testprofile for the body format.
type HTTPClient struct {
	endpoint string
	token    string
	http     *http.Client
}

var _ workerplane.ClaimClient = (*HTTPClient)(nil)

// HTTPConfig configures NewHTTP.
type HTTPConfig struct {
	// ControlURL is the base URL of the Control Plane's Worker listener, for
	// example "https://127.0.0.1:9443". https is required unless the host is a
	// loopback address.
	ControlURL string
	// Token is the bearer token whose SHA-256 the Control Plane's session
	// table lists.
	Token string
	// RootCAs are the CAs that may vouch for the Control Plane. Nil means the
	// system pool.
	RootCAs *x509.CertPool
}

const maxResponseBytes = 64 * 1024

// NewHTTP validates the configuration. It opens no connection.
func NewHTTP(cfg HTTPConfig) (*HTTPClient, error) {
	u, err := url.Parse(cfg.ControlURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("%w: control URL must be scheme://host:port", ErrInvalidConfig)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopbackHost(u.Hostname()):
	default:
		return nil, fmt.Errorf("%w: the control URL must use https unless it is a loopback address", ErrInvalidConfig)
	}
	if len(cfg.Token) < 20 {
		return nil, fmt.Errorf("%w: token is missing or too short", ErrInvalidConfig)
	}
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: cfg.RootCAs},
		MaxIdleConnsPerHost: 1,
		DisableCompression:  true,
	}
	return &HTTPClient{
		endpoint: u.Scheme + "://" + u.Host + testprofile.Path,
		token:    cfg.Token,
		http: &http.Client{
			Transport: tr,
			// A redirect would carry the token to another place.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LoadRootCAs reads a PEM file of CA certificates.
func LoadRootCAs(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%w: CA file holds no certificate", ErrInvalidConfig)
	}
	return pool, nil
}

// CloseIdleConnections releases pooled connections.
func (c *HTTPClient) CloseIdleConnections() { c.http.CloseIdleConnections() }

// SubmitClaim posts one claim. The principal is not sent: the Control Plane
// derives it from the token, and a claim that names another Worker is
// refused there. Every non-200 answer is an error, so claimqueue keeps the
// claim.
func (c *HTTPClient) SubmitClaim(ctx context.Context, _ workerplane.Principal, claim resultclaim.Claim) (workerplane.ClaimAck, error) {
	body, err := testprofile.EncodeClaim(claim)
	if err != nil {
		return workerplane.ClaimAck{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return workerplane.ClaimAck{}, err
	}
	req.Header.Set("Content-Type", testprofile.ContentType)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return workerplane.ClaimAck{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return workerplane.ClaimAck{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return workerplane.ClaimAck{}, fmt.Errorf("%w: control plane answered HTTP %d", ErrNotAcknowledged, resp.StatusCode)
	}
	if len(raw) > maxResponseBytes {
		return workerplane.ClaimAck{}, fmt.Errorf("%w: response is too large", ErrNotAcknowledged)
	}
	ack, err := testprofile.DecodeAck(raw)
	if err != nil {
		return workerplane.ClaimAck{}, fmt.Errorf("%w: %v", ErrNotAcknowledged, err)
	}
	return ack, nil
}

// ErrNotAcknowledged means the Control Plane did not return a usable
// acknowledgement. The claim is not known to be delivered.
var ErrNotAcknowledged = errors.New("claim not acknowledged")
