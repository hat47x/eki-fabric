package trustbundle

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

func testCA(t *testing.T, name string, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             base,
		NotAfter:              notAfter,
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
	return cert
}

func bundleWith(t *testing.T, ca *x509.Certificate) Bundle {
	t.Helper()
	return Bundle{
		Version:  1,
		Issuers:  []Issuer{{Fingerprint: IssuerFingerprint(ca), NotBefore: base, NotAfter: base.Add(365 * 24 * time.Hour)}},
		NotAfter: base.Add(90 * 24 * time.Hour),
	}
}

func TestAPeerSignedByATrustedRootPasses(t *testing.T) {
	ca := testCA(t, "ca", base.Add(365*24*time.Hour))
	b := bundleWith(t, ca)
	if err := CheckPeerChains(b, [][]*x509.Certificate{{ca}}, base.Add(time.Hour)); err != nil {
		t.Fatalf("CheckPeerChains() error = %v", err)
	}
}

func TestAPeerSignedByAnotherRootIsRefused(t *testing.T) {
	trusted := testCA(t, "trusted", base.Add(365*24*time.Hour))
	other := testCA(t, "other", base.Add(365*24*time.Hour))
	b := bundleWith(t, trusted)
	err := CheckPeerChains(b, [][]*x509.Certificate{{other}}, base.Add(time.Hour))
	if !errors.Is(err, ErrIssuerNotTrusted) {
		t.Fatalf("error = %v, want %v", err, ErrIssuerNotTrusted)
	}
}

func TestAPeerWithNoChainIsRefused(t *testing.T) {
	ca := testCA(t, "ca", base.Add(365*24*time.Hour))
	b := bundleWith(t, ca)
	if err := CheckPeerChains(b, nil, base.Add(time.Hour)); !errors.Is(err, ErrIssuerNotTrusted) {
		t.Fatalf("error = %v, want %v", err, ErrIssuerNotTrusted)
	}
}

func TestAnExpiredVersionRefusesEvenATrustedRoot(t *testing.T) {
	ca := testCA(t, "ca", base.Add(365*24*time.Hour))
	b := bundleWith(t, ca)
	b.NotAfter = base.Add(time.Hour)
	if err := CheckPeerChains(b, [][]*x509.Certificate{{ca}}, base.Add(2*time.Hour)); !errors.Is(err, ErrNotCurrent) {
		t.Fatalf("error = %v, want %v", err, ErrNotCurrent)
	}
}
