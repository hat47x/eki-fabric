package workerca_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workercert"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
)

var t0 = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func newCA(t *testing.T, domain string, validity time.Duration) (*workerca.CA, []byte, []byte) {
	t.Helper()
	certPEM, keyPEM, err := workerca.GenerateCA(domain, 0, t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := workerca.New(certPEM, keyPEM, domain, validity)
	if err != nil {
		t.Fatal(err)
	}
	return ca, certPEM, keyPEM
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func issue(t *testing.T, ca *workerca.CA, id ids.WorkerIdentity, key *ecdsa.PrivateKey, now time.Time) (*x509.Certificate, workerenrollment.IssuedCertificate) {
	t.Helper()
	got, err := ca.IssueCertificate(workerenrollment.CertificateRequest{Identity: id, TrustDomain: ca.TrustDomain(), PublicKey: &key.PublicKey, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(got.DER)
	if err != nil {
		t.Fatal(err)
	}
	return cert, got
}

func TestIssuedCertificateShape(t *testing.T) {
	ca, _, _ := newCA(t, "example.org", 0)
	id, _ := ids.NewWorkerIdentity()
	cert, got := issue(t, ca, id, newKey(t), t0)

	want, _ := workercert.URI("example.org", id)
	if len(cert.URIs) != 1 || cert.URIs[0].String() != want.String() {
		t.Fatalf("SAN URIs = %v, want [%s]", cert.URIs, want)
	}
	if len(cert.DNSNames)+len(cert.IPAddresses)+len(cert.EmailAddresses) != 0 {
		t.Fatalf("certificate carries other SAN types: %v %v %v", cert.DNSNames, cert.IPAddresses, cert.EmailAddresses)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("ExtKeyUsage = %v, want client auth only", cert.ExtKeyUsage)
	}
	if cert.IsCA {
		t.Fatal("a Worker certificate is a CA")
	}
	if cert.NotAfter.Sub(cert.NotBefore) != workercert.DefaultValidity || !cert.NotBefore.Equal(t0) {
		t.Fatalf("validity %v..%v, want %v from now", cert.NotBefore, cert.NotAfter, workercert.DefaultValidity)
	}
	if got.Serial == "" || got.Serial != cert.SerialNumber.Text(16) || !got.NotAfter.Equal(cert.NotAfter) {
		t.Fatalf("issued record %+v disagrees with the certificate", got)
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate())
	opts := x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: t0.Add(time.Hour)}
	if _, err := cert.Verify(opts); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// It is not a server certificate.
	opts.KeyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	if _, err := cert.Verify(opts); err == nil {
		t.Fatal("a Worker certificate verified for server auth")
	}
}

func TestSerialsAreUnique(t *testing.T) {
	ca, _, _ := newCA(t, "example.org", 0)
	id, _ := ids.NewWorkerIdentity()
	key := newKey(t)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		_, got := issue(t, ca, id, key, t0)
		if seen[got.Serial] {
			t.Fatalf("serial %s issued twice", got.Serial)
		}
		seen[got.Serial] = true
	}
}

func TestValidityFollowsConfigurationAndIsClampedToTheCA(t *testing.T) {
	ca, _, _ := newCA(t, "example.org", time.Minute)
	id, _ := ids.NewWorkerIdentity()
	cert, _ := issue(t, ca, id, newKey(t), t0)
	if cert.NotAfter.Sub(cert.NotBefore) != time.Minute {
		t.Fatalf("validity = %v, want 1m", cert.NotAfter.Sub(cert.NotBefore))
	}
	// Near the end of the CA's life, the certificate does not outlive the CA.
	near := ca.Certificate().NotAfter.Add(-time.Second)
	cert, _ = issue(t, ca, id, newKey(t), near)
	if cert.NotAfter.After(ca.Certificate().NotAfter) {
		t.Fatalf("certificate outlives the CA: %v", cert.NotAfter)
	}
	// An expired or not-yet-valid CA signs nothing.
	for _, at := range []time.Time{ca.Certificate().NotAfter, ca.Certificate().NotBefore.Add(-time.Hour)} {
		_, err := ca.IssueCertificate(workerenrollment.CertificateRequest{Identity: id, TrustDomain: "example.org", PublicKey: &newKey(t).PublicKey, Now: at})
		if err == nil {
			t.Fatalf("CA signed at %v", at)
		}
	}
}

func TestCARefusesAnotherTrustDomainAndBadKeys(t *testing.T) {
	ca, _, _ := newCA(t, "example.org", 0)
	id, _ := ids.NewWorkerIdentity()
	if _, err := ca.IssueCertificate(workerenrollment.CertificateRequest{Identity: id, TrustDomain: "other.example", PublicKey: &newKey(t).PublicKey, Now: t0}); err == nil {
		t.Fatal("CA signed for another trust domain")
	}
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if _, err := ca.IssueCertificate(workerenrollment.CertificateRequest{Identity: id, TrustDomain: "example.org", PublicKey: &p384.PublicKey, Now: t0}); err == nil {
		t.Fatal("CA signed a P-384 key")
	}
	if _, err := ca.IssueCertificate(workerenrollment.CertificateRequest{Identity: "worker-a", TrustDomain: "example.org", PublicKey: &newKey(t).PublicKey, Now: t0}); err == nil {
		t.Fatal("CA signed an identity the registry could not have issued")
	}
}

// Go applies a URI name constraint to subdomains too, so a sub-domain of the
// trust domain is separated by the exact trust-domain comparison of the
// authenticator (workerplane), not by the constraint.
//
// The name constraint is part of the CA certificate, so a verifier rejects a
// certificate for another trust domain even if the CA key signed it.
func TestNameConstraintStopsCertificatesOutsideTheTrustDomain(t *testing.T) {
	_, certPEM, keyPEM := newCA(t, "example.org", 0)
	block, _ := pem.Decode(certPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := pem.Decode(keyPEM)
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	id, _ := ids.NewWorkerIdentity()

	sign := func(uri string) *x509.Certificate {
		u, _ := url.Parse(uri)
		tmpl := &x509.Certificate{
			SerialNumber: newSerialForTest(), Subject: pkix.Name{CommonName: "x"},
			NotBefore: t0, NotAfter: t0.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{u},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &newKey(t).PublicKey, k)
		if err != nil {
			t.Fatal(err)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	opts := x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: t0.Add(time.Minute)}
	if _, err := sign("eki://example.org/worker/" + string(id)).Verify(opts); err != nil {
		t.Fatalf("inside the trust domain: %v", err)
	}
	for _, out := range []string{"eki://other.example/worker/" + string(id), "eki://example.org.evil.example/worker/" + string(id)} {
		if _, err := sign(out).Verify(opts); err == nil {
			t.Fatalf("a certificate for %s verified", out)
		}
	}
}

func newSerialForTest() *big.Int { return big.NewInt(time.Now().UnixNano()) }

func TestNewRefusesUnsafeCAs(t *testing.T) {
	certPEM, keyPEM, err := workerca.GenerateCA("example.org", 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workerca.New(certPEM, keyPEM, "other.example", 0); err == nil {
		t.Fatal("CA for example.org accepted as other.example")
	}
	// Another key.
	_, otherKey, _ := workerca.GenerateCA("example.org", 0, t0)
	if _, err := workerca.New(certPEM, otherKey, "example.org", 0); err == nil {
		t.Fatal("mismatched key accepted")
	}
	for _, v := range []time.Duration{time.Second, 31 * 24 * time.Hour} {
		if _, err := workerca.New(certPEM, keyPEM, "example.org", v); err == nil {
			t.Fatalf("validity %v accepted", v)
		}
	}

	build := func(mod func(*x509.Certificate), key *ecdsa.PrivateKey) ([]byte, []byte) {
		tmpl := &x509.Certificate{
			SerialNumber: newSerialForTest(), Subject: pkix.Name{CommonName: "ca"},
			NotBefore: t0, NotAfter: t0.Add(time.Hour), KeyUsage: x509.KeyUsageCertSign,
			BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true,
			PermittedURIDomains: []string{"example.org"}, PermittedDNSDomainsCritical: true,
		}
		mod(tmpl)
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		kd, _ := x509.MarshalPKCS8PrivateKey(key)
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})
	}
	k := newKey(t)
	if c, kp := build(func(*x509.Certificate) {}, k); true {
		if _, err := workerca.New(c, kp, "example.org", 0); err != nil {
			t.Fatalf("control: a well-formed CA is refused: %v", err)
		}
	}
	for name, mod := range map[string]func(*x509.Certificate){
		"no name constraint":       func(c *x509.Certificate) { c.PermittedURIDomains = nil },
		"non-critical constraint":  func(c *x509.Certificate) { c.PermittedDNSDomainsCritical = false },
		"wider constraint":         func(c *x509.Certificate) { c.PermittedURIDomains = []string{".example.org"} },
		"also permits DNS":         func(c *x509.Certificate) { c.PermittedDNSDomains = []string{"example.org"} },
		"path length not limited":  func(c *x509.Certificate) { c.MaxPathLenZero = false; c.MaxPathLen = -1 },
		"cannot sign certificates": func(c *x509.Certificate) { c.KeyUsage = x509.KeyUsageDigitalSignature },
		"not a CA":                 func(c *x509.Certificate) { c.IsCA = false; c.MaxPathLenZero = false; c.MaxPathLen = 0 },
	} {
		c, kp := build(mod, k)
		if _, err := workerca.New(c, kp, "example.org", 0); err == nil {
			t.Errorf("%s: CA accepted", name)
		}
	}
	// A non-P-256 CA key is refused.
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	c, kp := build(func(*x509.Certificate) {}, p384)
	if _, err := workerca.New(c, kp, "example.org", 0); err == nil {
		t.Error("P-384 CA accepted")
	}
}

func csrFor(t *testing.T, key any, tmpl *x509.CertificateRequest) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestParseCSR(t *testing.T) {
	key := newKey(t)
	good := csrFor(t, key, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "me"}})
	for name, raw := range map[string][]byte{
		"DER": good,
		"PEM": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: good}),
	} {
		pub, err := workerca.ParseCSR(raw)
		if err != nil || !pub.Equal(&key.PublicKey) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	// A forged subject and names are accepted and then ignored.
	forged := csrFor(t, key, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "wrk_00000000000000000000000000000000"},
		URIs:    []*url.URL{{Scheme: "eki", Host: "example.org", Path: "/worker/wrk_00000000000000000000000000000000"}},
	})
	if _, err := workerca.ParseCSR(forged); err != nil {
		t.Fatalf("forged-name CSR: %v", err)
	}

	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	tampered := append([]byte(nil), good...)
	tampered[len(tampered)-3] ^= 0xff // inside the signature
	for name, raw := range map[string][]byte{
		"RSA key":          csrFor(t, rsaKey, &x509.CertificateRequest{}),
		"P-384 key":        csrFor(t, p384, &x509.CertificateRequest{}),
		"bad signature":    tampered,
		"empty":            nil,
		"garbage":          []byte("not a request"),
		"oversize":         append(append([]byte(nil), good...), make([]byte, workerca.MaxCSRBytes)...),
		"two PEM blocks":   append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: good}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: good})...),
		"wrong PEM type":   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: good}),
		"PEM then garbage": append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: good}), []byte("junk")...),
	} {
		if _, err := workerca.ParseCSR(raw); err == nil {
			t.Errorf("%s: CSR accepted", name)
		} else if !strings.Contains(err.Error(), "certificate signing request") {
			t.Errorf("%s: unexpected error class %v", name, err)
		}
	}
}
