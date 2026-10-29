package enroll

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/workercert"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

var (
	ErrInvalidParams = errors.New("invalid enrollment parameters")
	// ErrNotPinned is returned when the Control Plane does not present the
	// certificate whose fingerprint was supplied. Nothing, in particular not
	// the enrollment secret, has been sent when it is returned.
	ErrNotPinned = errors.New("the Control Plane does not present the pinned certificate")
	// ErrRefused means the Control Plane did not accept the enrollment
	// credential. It does not say why, and neither does the Control Plane.
	ErrRefused = errors.New("the Control Plane refused the enrollment credential")
)

const requestTimeout = 30 * time.Second

// Params are the inputs of Enroll.
type Params struct {
	// ControlURL is https://host:port of the Control Plane's Worker listener.
	ControlURL string
	// Fingerprint is the SHA-256 fingerprint that came with the secret: of the
	// top certificate of the chain the Worker listener sends.
	Fingerprint string
	// Secret is the enrollment secret. It is sent in the Authorization header
	// of one request, after the Control Plane has proved the pinned identity.
	Secret string
	// StateDir receives the identity. It is created with mode 0700.
	StateDir string
	// Clock is the time against which the Control Plane's certificate and the
	// issued certificate are checked. Nil means the system clock.
	Clock Clock
}

// Enroll generates a key on this device, enrolls with the Control Plane, and
// saves the identity. It refuses to replace an identity that exists.
func Enroll(ctx context.Context, p Params) (Identity, error) {
	clock := p.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	base, host, err := parseControlURL(p.ControlURL)
	if err != nil {
		return Identity{}, err
	}
	pin, err := workercert.ParseFingerprint(p.Fingerprint)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalidParams, err)
	}
	// The format check keeps an obviously wrong file (a path, a password) from
	// being sent anywhere. The error does not include the text.
	if _, err := workerregistry.HashPresentedSecret(p.Secret); err != nil {
		return Identity{}, fmt.Errorf("%w: the enrollment secret does not have the expected form", ErrInvalidParams)
	}
	if p.StateDir == "" {
		return Identity{}, fmt.Errorf("%w: a state directory is required", ErrInvalidParams)
	}
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		return Identity{}, fmt.Errorf("create the state directory: %w", err)
	}
	if _, err := os.Stat(identityPath(p.StateDir)); err == nil {
		return Identity{}, ErrIdentityExists
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate the private key: %w", err)
	}
	csr, err := csrPEM(key)
	if err != nil {
		return Identity{}, err
	}

	pinned := &pinnedVerifier{pin: pin, host: host, now: clock.Now}
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS13,
				// The chain is verified by VerifyConnection against the pinned
				// certificate, because the system roots are not the authority
				// here (ADR-0027 item 6).
				InsecureSkipVerify: true, //nolint:gosec // replaced by pinnedVerifier below
				VerifyConnection:   pinned.verify,
			},
			DisableKeepAlives:  true,
			DisableCompression: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()

	// The secret is in this request only. The request is written after the TLS
	// handshake, and the handshake fails unless the pinned certificate is
	// presented, so a wrong fingerprint ends here with nothing sent.
	resp, err := post(ctx, client, base+enrollwire.EnrollPath, csr, "Bearer "+p.Secret)
	if err != nil {
		if pinned.failed {
			return Identity{}, ErrNotPinned
		}
		return Identity{}, err
	}
	if resp.status == http.StatusUnauthorized {
		return Identity{}, ErrRefused
	}
	id, caPEM, err := parseResponse(resp, key, clock.Now())
	if err != nil {
		return Identity{}, err
	}
	if err := saveAll(p.StateDir, id, pinned.anchor, caPEM); err != nil {
		return Identity{}, err
	}
	return id, nil
}

func identityPath(dir string) string { return filepath.Join(dir, IdentityFile) }

func saveAll(dir string, id Identity, anchor *x509.Certificate, caPEM string) error {
	// The anchor first: an identity without a pinned Control Plane is useless,
	// and a pinned Control Plane without an identity is harmless.
	if err := writeAtomic(filepath.Join(dir, ControlAnchorFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: anchor.Raw})); err != nil {
		return err
	}
	if caPEM != "" {
		if err := writeAtomic(filepath.Join(dir, WorkerCAFile), []byte(caPEM)); err != nil {
			return err
		}
	}
	return Save(dir, id)
}

// Renew asks the Control Plane for a new certificate over mTLS with the
// current one, using a new key, and saves the result. The Control Plane
// refuses an expired certificate and a Worker that is disabled or revoked.
func Renew(ctx context.Context, controlURL, stateDir string, current Identity, opts ...Option) (Identity, error) {
	clock := applyOptions(opts).clock
	base, _, err := parseControlURL(controlURL)
	if err != nil {
		return Identity{}, err
	}
	anchor, err := LoadAnchor(stateDir)
	if err != nil {
		return Identity{}, err
	}
	if current.Expired(clock.Now()) {
		return Identity{}, ErrExpired
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate the private key: %w", err)
	}
	csr, err := csrPEM(key)
	if err != nil {
		return Identity{}, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(anchor)
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS13, RootCAs: roots, Time: clock.Now,
				GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
					return current.TLSCertificate(), nil
				},
			},
			DisableKeepAlives:  true,
			DisableCompression: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	resp, err := post(ctx, client, base+enrollwire.RenewPath, csr, "")
	if err != nil {
		return Identity{}, err
	}
	if resp.status != http.StatusOK {
		return Identity{}, fmt.Errorf("the Control Plane answered HTTP %d to the renewal", resp.status)
	}
	id, _, err := parseResponse(resp, key, clock.Now())
	if err != nil {
		return Identity{}, err
	}
	if id.WorkerIdentity != current.WorkerIdentity || id.TrustDomain != current.TrustDomain {
		return Identity{}, fmt.Errorf("%w: the renewed certificate names another Worker", ErrInvalidIdentity)
	}
	if err := Save(stateDir, id); err != nil {
		return Identity{}, err
	}
	return id, nil
}

func parseControlURL(raw string) (base, host string, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return "", "", fmt.Errorf("%w: the control URL must be https://host:port", ErrInvalidParams)
	}
	return "https://" + u.Host, u.Hostname(), nil
}

func csrPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	// The subject is empty: the Control Plane ignores it and names the Worker
	// itself.
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		return nil, fmt.Errorf("create the certificate request: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

type response struct {
	status int
	body   []byte
}

func post(ctx context.Context, c *http.Client, endpoint string, csr []byte, authorization string) (response, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(csr))
	if err != nil {
		return response{}, err
	}
	req.Header.Set("Content-Type", enrollwire.CSRContentType)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := c.Do(req)
	if err != nil {
		// The URL is not part of the error text: it holds no secret, but a
		// transport error must not echo request headers either.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return response{}, fmt.Errorf("request to the Control Plane failed: %w", uerr.Err)
		}
		return response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, enrollwire.MaxResponseBytes+1))
	if err != nil {
		return response{}, err
	}
	if len(body) > enrollwire.MaxResponseBytes {
		return response{}, errors.New("the Control Plane's response is too large")
	}
	return response{status: resp.StatusCode, body: body}, nil
}

// parseResponse checks everything the Control Plane returned against the key
// this device generated before it trusts the certificate.
func parseResponse(r response, key *ecdsa.PrivateKey, now time.Time) (Identity, string, error) {
	if r.status != http.StatusOK {
		return Identity{}, "", fmt.Errorf("the Control Plane answered HTTP %d", r.status)
	}
	out, err := enrollwire.DecodeResponse(r.body)
	if err != nil {
		return Identity{}, "", err
	}
	block, _ := pem.Decode([]byte(out.CertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return Identity{}, "", enrollwire.ErrInvalidResponse
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Identity{}, "", enrollwire.ErrInvalidResponse
	}
	id, err := NewIdentity(key, cert)
	if err != nil {
		return Identity{}, "", err
	}
	if string(id.WorkerIdentity) != out.WorkerIdentity || id.TrustDomain != out.TrustDomain {
		return Identity{}, "", fmt.Errorf("%w: the response disagrees with the certificate", ErrInvalidIdentity)
	}
	if id.Expired(now) {
		return Identity{}, "", fmt.Errorf("%w: the certificate is already expired", ErrInvalidIdentity)
	}
	if out.CAChainPEM != "" {
		ca, _ := pem.Decode([]byte(out.CAChainPEM))
		if ca == nil {
			return Identity{}, "", enrollwire.ErrInvalidResponse
		}
		caCert, err := x509.ParseCertificate(ca.Bytes)
		if err != nil || cert.CheckSignatureFrom(caCert) != nil {
			return Identity{}, "", fmt.Errorf("%w: the certificate was not signed by the CA that came with it", ErrInvalidIdentity)
		}
	}
	return id, out.CAChainPEM, nil
}

// pinnedVerifier accepts a server chain only if it contains the certificate
// with the pinned fingerprint and the chain verifies up to that certificate
// for the host that was dialed. The pinned certificate can be the leaf, an
// intermediate, or a root the server sends.
type pinnedVerifier struct {
	pin    string
	host   string
	anchor *x509.Certificate
	failed bool
	now    func() time.Time
}

func (v *pinnedVerifier) verify(cs tls.ConnectionState) error {
	certs := cs.PeerCertificates
	if len(certs) == 0 {
		v.failed = true
		return ErrNotPinned
	}
	var anchor *x509.Certificate
	for _, c := range certs {
		if workercert.Fingerprint(c.Raw) == v.pin {
			anchor = c
			break
		}
	}
	if anchor == nil {
		v.failed = true
		return ErrNotPinned
	}
	roots := x509.NewCertPool()
	roots.AddCert(anchor)
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime: v.now(),
	}); err != nil {
		v.failed = true
		return fmt.Errorf("%w: %v", ErrNotPinned, err)
	}
	if err := certs[0].VerifyHostname(v.host); err != nil {
		v.failed = true
		return fmt.Errorf("%w: %v", ErrNotPinned, err)
	}
	v.anchor = anchor
	return nil
}
