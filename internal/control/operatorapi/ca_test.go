package operatorapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCA(t *testing.T, dir string) string {
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
	path := filepath.Join(dir, "operator-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestACertificateFileBecomesAPool(t *testing.T) {
	path := writeCA(t, t.TempDir())
	pool, err := LoadCA(path)
	if err != nil {
		t.Fatalf("LoadCA() error = %v", err)
	}
	if len(pool.Subjects()) != 1 {
		t.Fatalf("subjects = %d, want 1", len(pool.Subjects()))
	}
}

func TestAMissingPathIsRefused(t *testing.T) {
	if _, err := LoadCA(""); !errors.Is(err, ErrOperatorCA) {
		t.Fatalf("error = %v, want %v", err, ErrOperatorCA)
	}
}

func TestAMissingFileIsRefused(t *testing.T) {
	if _, err := LoadCA(filepath.Join(t.TempDir(), "absent.pem")); !errors.Is(err, ErrOperatorCA) {
		t.Fatalf("error = %v, want %v", err, ErrOperatorCA)
	}
}

func TestAFileWithoutACertificateIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCA(path); !errors.Is(err, ErrOperatorCA) {
		t.Fatalf("error = %v, want %v", err, ErrOperatorCA)
	}
}
