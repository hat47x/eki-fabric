package operatorapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"testing"
	"time"
)

type operatorCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newCA(t *testing.T) operatorCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "operator-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return operatorCA{cert: cert, key: key, pool: pool}
}

func (ca operatorCA) leaf(t *testing.T, uris ...string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "operator"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		template.URIs = append(template.URIs, u)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func requestWith(cert *x509.Certificate) *http.Request {
	r := &http.Request{}
	if cert != nil {
		r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	}
	return r
}

func TestAnOperatorCertificateFromTheCAAuthenticates(t *testing.T) {
	ca := newCA(t)
	leaf := ca.leaf(t, "eki://example.org/operator/alice", "eki://example.org/role/operator")
	identity, err := MTLSAuthenticator(ca.pool, "example.org", nil)(requestWith(leaf))
	if err != nil {
		t.Fatalf("authenticator error = %v", err)
	}
	if identity.Name != "alice" || identity.Role != "operator" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestACertificateFromAnotherCAIsRefused(t *testing.T) {
	trusted := newCA(t)
	other := newCA(t)
	leaf := other.leaf(t, "eki://example.org/operator/alice", "eki://example.org/role/operator")
	_, err := MTLSAuthenticator(trusted.pool, "example.org", nil)(requestWith(leaf))
	if !errors.Is(err, ErrOperatorCertificate) {
		t.Fatalf("error = %v, want %v", err, ErrOperatorCertificate)
	}
}

func TestARequestWithoutACertificateIsRefused(t *testing.T) {
	ca := newCA(t)
	if _, err := MTLSAuthenticator(ca.pool, "example.org", nil)(requestWith(nil)); !errors.Is(err, ErrOperatorCertificate) {
		t.Fatalf("error = %v, want %v", err, ErrOperatorCertificate)
	}
}

func TestACertificateForAnotherTrustDomainIsRefused(t *testing.T) {
	ca := newCA(t)
	leaf := ca.leaf(t, "eki://other.example/operator/alice", "eki://other.example/role/operator")
	if _, err := MTLSAuthenticator(ca.pool, "example.org", nil)(requestWith(leaf)); !errors.Is(err, ErrOperatorCertificate) {
		t.Fatalf("error = %v, want %v", err, ErrOperatorCertificate)
	}
}

func TestACertificateThatNamesNoRoleIsRefused(t *testing.T) {
	ca := newCA(t)
	leaf := ca.leaf(t, "eki://example.org/operator/alice")
	if _, err := MTLSAuthenticator(ca.pool, "example.org", nil)(requestWith(leaf)); !errors.Is(err, ErrOperatorCertificate) {
		t.Fatalf("error = %v, want %v", err, ErrOperatorCertificate)
	}
}
