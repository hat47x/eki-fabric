// Package workerca is the small certificate authority that signs Worker client
// certificates (ADR-0027 sections 2.1, 2.3 and 2.7).
//
// It signs one kind of certificate: a client certificate whose only name is
// eki://<trust-domain>/worker/<worker-identity>, with the identity taken from
// the registry and never from the requester. Its own certificate carries a
// name constraint, so even a stolen CA key cannot produce a certificate for
// another trust domain that a verifier of this CA would accept.
//
// The CA key is a separate key from the Lease and WorkOrder signing keys
// (ADR-0027 item 25). It is read from a file. A KMS or HSM is not supported
// here; that is an open question of the ADR.
package workerca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/federation"
	"github.com/hat47x/eki-fabric/internal/core/workercert"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
)

var (
	ErrInvalidCA  = errors.New("invalid worker CA")
	ErrInvalidCSR = errors.New("invalid certificate signing request")
)

// DefaultCAValidity is the lifetime GenerateCA gives a new CA certificate. A
// provisional default; CA rotation is an open question (ADR-0027 section 5).
const DefaultCAValidity = 5 * 365 * 24 * time.Hour

// CA signs Worker client certificates for one trust domain.
type CA struct {
	cert        *x509.Certificate
	key         *ecdsa.PrivateKey
	trustDomain string
	validity    time.Duration
}

var _ workerenrollment.CertificateIssuer = (*CA)(nil)

// New builds a CA from PEM text of its certificate and its private key. It
// refuses a certificate that is not a path-length-0 CA for this trust domain
// only, or whose key is not ECDSA P-256 or does not match.
func New(certPEM, keyPEM []byte, trustDomain string, validity time.Duration) (*CA, error) {
	if _, err := federation.ParseTrustDomain(trustDomain); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCA, err)
	}
	if validity == 0 {
		validity = workercert.DefaultValidity
	}
	if validity < workercert.MinValidity || validity > workercert.MaxValidity {
		return nil, fmt.Errorf("%w: certificate validity %v is outside [%v, %v]", ErrInvalidCA, validity, workercert.MinValidity, workercert.MaxValidity)
	}
	cert, err := parseCertPEM(certPEM)
	if err != nil {
		return nil, err
	}
	key, err := parseKeyPEM(keyPEM)
	if err != nil {
		return nil, err
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: the CA certificate key is not ECDSA P-256", ErrInvalidCA)
	}
	if !pub.Equal(&key.PublicKey) {
		return nil, fmt.Errorf("%w: the CA key does not match the CA certificate", ErrInvalidCA)
	}
	switch {
	case !cert.IsCA || !cert.BasicConstraintsValid:
		return nil, fmt.Errorf("%w: the certificate is not a CA", ErrInvalidCA)
	case cert.KeyUsage&x509.KeyUsageCertSign == 0:
		return nil, fmt.Errorf("%w: the CA certificate cannot sign certificates", ErrInvalidCA)
	case !cert.MaxPathLenZero:
		return nil, fmt.Errorf("%w: the CA must be limited to path length 0", ErrInvalidCA)
	case len(cert.PermittedURIDomains) != 1 || cert.PermittedURIDomains[0] != trustDomain || !cert.PermittedDNSDomainsCritical:
		return nil, fmt.Errorf("%w: the CA must carry a critical name constraint permitting only the URI domain %q", ErrInvalidCA, trustDomain)
	case len(cert.PermittedDNSDomains) != 0 || len(cert.PermittedIPRanges) != 0 || len(cert.PermittedEmailAddresses) != 0:
		return nil, fmt.Errorf("%w: the CA permits names other than its trust domain's URIs", ErrInvalidCA)
	}
	return &CA{cert: cert, key: key, trustDomain: trustDomain, validity: validity}, nil
}

// LoadFiles reads the CA certificate and key from PEM files.
func LoadFiles(certPath, keyPath, trustDomain string, validity time.Duration) (*CA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read worker CA certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read worker CA key: %w", err)
	}
	return New(certPEM, keyPEM, trustDomain, validity)
}

// Certificate returns the CA certificate that verifiers trust.
func (c *CA) Certificate() *x509.Certificate { return c.cert }

// TrustDomain is the only domain this CA signs for.
func (c *CA) TrustDomain() string { return c.trustDomain }

// Validity is the lifetime of the certificates this CA signs.
func (c *CA) Validity() time.Duration { return c.validity }

// IssueCertificate signs a client certificate for req.Identity over
// req.PublicKey. Nothing from the requester but the public key is used.
func (c *CA) IssueCertificate(req workerenrollment.CertificateRequest) (workerenrollment.IssuedCertificate, error) {
	if req.TrustDomain != c.trustDomain {
		return workerenrollment.IssuedCertificate{}, fmt.Errorf("%w: the CA signs only for %q", ErrInvalidCA, c.trustDomain)
	}
	uri, err := workercert.URI(req.TrustDomain, req.Identity)
	if err != nil {
		return workerenrollment.IssuedCertificate{}, err
	}
	pub, ok := req.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub == nil || pub.Curve != elliptic.P256() {
		return workerenrollment.IssuedCertificate{}, fmt.Errorf("%w: the key is not ECDSA P-256", ErrInvalidCSR)
	}
	now := req.Now.UTC().Truncate(time.Second)
	if now.Before(c.cert.NotBefore) || !now.Before(c.cert.NotAfter) {
		return workerenrollment.IssuedCertificate{}, fmt.Errorf("%w: the CA certificate is not valid now", ErrInvalidCA)
	}
	notAfter := now.Add(c.validity)
	if notAfter.After(c.cert.NotAfter) {
		notAfter = c.cert.NotAfter
	}
	serial, err := newSerial()
	if err != nil {
		return workerenrollment.IssuedCertificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		// The subject is a label for humans reading a certificate. It is the
		// registry identity, and nothing in verification reads it.
		Subject:               pkix.Name{CommonName: string(req.Identity)},
		NotBefore:             now,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		URIs:                  []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, pub, c.key)
	if err != nil {
		return workerenrollment.IssuedCertificate{}, fmt.Errorf("sign worker certificate: %w", err)
	}
	return workerenrollment.IssuedCertificate{
		DER:       der,
		Serial:    fmt.Sprintf("%x", serial),
		NotBefore: now,
		NotAfter:  notAfter,
	}, nil
}

// newSerial draws a 127-bit positive serial number. Unique with overwhelming
// probability; the registry records each one.
func newSerial() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("draw certificate serial: %w", err)
	}
	return n.Add(n, big.NewInt(1)), nil
}

// GenerateCA creates a new CA key and a self-signed CA certificate for
// trustDomain, as PEM. The certificate is a path-length-0 CA that may sign
// only certificates whose URI names lie in trustDomain.
func GenerateCA(trustDomain string, validity time.Duration, now time.Time) (certPEM, keyPEM []byte, err error) {
	if _, err := federation.ParseTrustDomain(trustDomain); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidCA, err)
	}
	if validity <= 0 {
		validity = DefaultCAValidity
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	start := now.UTC().Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: "EKI Worker CA " + trustDomain},
		NotBefore:                   start,
		NotAfter:                    start.Add(validity),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLen:                  0,
		MaxPathLenZero:              true,
		PermittedURIDomains:         []string{trustDomain},
		PermittedDNSDomainsCritical: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("self-sign CA certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("encode CA key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func parseCertPEM(raw []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: the CA certificate is not PEM", ErrInvalidCA)
	}
	if next, _ := pem.Decode(rest); next != nil {
		return nil, fmt.Errorf("%w: the CA certificate file holds more than one block", ErrInvalidCA)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCA, err)
	}
	return cert, nil
}

func parseKeyPEM(raw []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%w: the CA key is not PEM", ErrInvalidCA)
	}
	switch block.Type {
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: the CA key cannot be parsed", ErrInvalidCA)
		}
		ec, ok := k.(*ecdsa.PrivateKey)
		if !ok || ec.Curve != elliptic.P256() {
			return nil, fmt.Errorf("%w: the CA key is not ECDSA P-256", ErrInvalidCA)
		}
		return ec, nil
	case "EC PRIVATE KEY":
		ec, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil || ec.Curve != elliptic.P256() {
			return nil, fmt.Errorf("%w: the CA key is not ECDSA P-256", ErrInvalidCA)
		}
		return ec, nil
	}
	return nil, fmt.Errorf("%w: unsupported CA key block %q", ErrInvalidCA, block.Type)
}
