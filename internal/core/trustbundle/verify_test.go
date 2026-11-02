package trustbundle

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestATrustedIssuerInsideItsWindowIsAccepted(t *testing.T) {
	b := validBundle()
	if err := CheckIssuer(b, b.Issuers[0].Fingerprint, base.Add(time.Hour)); err != nil {
		t.Fatalf("CheckIssuer() error = %v", err)
	}
}

func TestAnIssuerTheBundleDoesNotListIsNotTrusted(t *testing.T) {
	b := validBundle()
	err := CheckIssuer(b, contract.SHA256([]byte("other")), base.Add(time.Hour))
	if !errors.Is(err, ErrIssuerNotTrusted) {
		t.Fatalf("error = %v, want %v", err, ErrIssuerNotTrusted)
	}
}

func TestAnIssuerOutsideItsWindowIsRejected(t *testing.T) {
	b := validBundle()
	b.Issuers[0].NotAfter = base.Add(24 * time.Hour)
	b.NotAfter = base.Add(90 * 24 * time.Hour)
	err := CheckIssuer(b, b.Issuers[0].Fingerprint, base.Add(48*time.Hour))
	if !errors.Is(err, ErrIssuerExpired) {
		t.Fatalf("error = %v, want %v", err, ErrIssuerExpired)
	}
}

func TestAnExpiredBundleIsRejectedBeforeTheIssuerIsConsidered(t *testing.T) {
	b := validBundle()
	b.NotAfter = base.Add(time.Hour)
	err := CheckIssuer(b, b.Issuers[0].Fingerprint, base.Add(2*time.Hour))
	if !errors.Is(err, ErrNotCurrent) {
		t.Fatalf("error = %v, want %v", err, ErrNotCurrent)
	}
}

func TestRemovingAnIssuerMakesItsCertificatesFail(t *testing.T) {
	previous := validBundle()
	next := validBundle()
	next.Version = 2
	next.NotAfter = previous.NotAfter.Add(24 * time.Hour)
	next.Issuers[0] = issuer(2, base, base.Add(365*24*time.Hour))
	if err := CheckIssuer(previous, previous.Issuers[0].Fingerprint, base.Add(time.Hour)); err != nil {
		t.Fatalf("before removal error = %v", err)
	}
	err := CheckIssuer(next, previous.Issuers[0].Fingerprint, base.Add(time.Hour))
	if !errors.Is(err, ErrIssuerNotTrusted) {
		t.Fatalf("after removal error = %v, want %v", err, ErrIssuerNotTrusted)
	}
}
