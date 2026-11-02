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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

func writeCAAndServer(t *testing.T, dir string) (caPath, certPath, keyPath string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "operator-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPath = filepath.Join(dir, "operator-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "operator-api"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, "server.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath = filepath.Join(dir, "server.key")
	return caPath, certPath, keyPathAfterWrite(t, keyPath, keyDER)
}

func keyPathAfterWrite(t *testing.T, path string, der []byte) string {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWithoutACAPathTheRouteIsDisabled(t *testing.T) {
	handler, tlsConfig, err := Build(ServerConfig{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if handler.Enabled() {
		t.Fatal("the handler is enabled without a CA")
	}
	if tlsConfig.ClientAuth == tls.RequireAndVerifyClientCert {
		t.Fatal("the listener asks for a certificate it cannot check")
	}
}

func TestWithACATheListenerRequiresAClientCertificate(t *testing.T) {
	caPath, certPath, keyPath := writeCAAndServer(t, t.TempDir())
	handler, tlsConfig, err := Build(ServerConfig{
		CAPath: caPath, CertFile: certPath, KeyFile: keyPath,
		TrustDomain: "example.org", Approver: trustBundleApprover{},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !handler.Enabled() {
		t.Fatal("the handler is disabled with a CA")
	}
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v, want RequireAndVerifyClientCert", tlsConfig.ClientAuth)
	}
	if tlsConfig.ClientCAs == nil || len(tlsConfig.ClientCAs.Subjects()) != 1 {
		t.Fatal("the listener has no Operator CA")
	}
}

func TestAnOperatorCAWithoutAnApproverIsRefused(t *testing.T) {
	caPath, certPath, keyPath := writeCAAndServer(t, t.TempDir())
	if _, _, err := Build(ServerConfig{CAPath: caPath, CertFile: certPath, KeyFile: keyPath}); err == nil {
		t.Fatal("Build() accepted a CA with nowhere to record approvals")
	}
}

func TestAnOperatorCAWithoutAServerCertificateIsRefused(t *testing.T) {
	caPath, _, _ := writeCAAndServer(t, t.TempDir())
	if _, _, err := Build(ServerConfig{CAPath: caPath, Approver: trustBundleApprover{}}); err == nil {
		t.Fatal("Build() accepted a CA without a server certificate")
	}
}

type trustBundleApprover struct{}

func (trustBundleApprover) Approve(trustbundle.Approval) error { return nil }
