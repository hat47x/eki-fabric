package operatorapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

func TestServingNeedsAHandler(t *testing.T) {
	if _, err := Serve("127.0.0.1:0", nil, &tls.Config{}, nil); err == nil {
		t.Fatal("Serve() accepted no handler")
	}
}

func TestServingNeedsItsOwnCertificate(t *testing.T) {
	handler := New(Config{})
	if _, err := Serve("127.0.0.1:0", handler, &tls.Config{}, nil); err == nil {
		t.Fatal("Serve() accepted a listener without a certificate")
	}
}

func selfSignedCert(t *testing.T) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return &cert
}

func TestServingRefusesAListenerThatDoesNotVerifyTheClient(t *testing.T) {
	handler := New(Config{})
	cfg := &tls.Config{
		Certificates: []tls.Certificate{{}},
		ClientAuth:   tls.NoClientCert,
	}
	if _, err := Serve("127.0.0.1:0", handler, cfg, nil); err == nil {
		t.Fatal("Serve() accepted a listener that does not verify the client")
	}
}

func TestServerTimeoutsAreSet(t *testing.T) {
	handler := New(Config{})
	cert := selfSignedCert(t)
	s, err := Serve("127.0.0.1:0", handler, &tls.Config{
		Certificates: []tls.Certificate{*cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer s.Close(nil)
	if s.http.ReadHeaderTimeout == 0 || s.http.ReadTimeout == 0 || s.http.WriteTimeout == 0 || s.http.IdleTimeout == 0 || s.http.MaxHeaderBytes == 0 {
		t.Fatalf("an unbounded server setting remains: %+v", s.http)
	}
}

type testApprover struct{}

func (testApprover) Approve(trustbundle.Approval) error { return nil }
