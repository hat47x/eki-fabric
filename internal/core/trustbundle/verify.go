package trustbundle

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ErrIssuerNotTrusted means the certificate's issuer is not in the bundle at
// all: either it was never trusted or a later version removed it (ADR-0036 §2.3).
var ErrIssuerNotTrusted = errors.New("trustbundle: issuer is not trusted")

// ErrIssuerExpired means the issuer is trusted but its own window does not cover
// the time asked about. A CA that has run out is not a revocation; it is the end
// of the certificates it could sign.
var ErrIssuerExpired = errors.New("trustbundle: issuer is outside its validity window")

// CheckIssuer answers whether a certificate from this issuer may be accepted at
// now, given the bundle a Worker holds. It is the check that turns a version
// change into an effect: removing an issuer makes its certificates fail here on
// the next verification.
func CheckIssuer(b Bundle, fingerprint contract.Digest, now time.Time) error {
	if err := b.Current(now); err != nil {
		return err
	}
	for _, issuer := range b.Issuers {
		if issuer.Fingerprint != fingerprint {
			continue
		}
		if now.Before(issuer.NotBefore) || now.After(issuer.NotAfter) {
			return fmt.Errorf("%w: %s", ErrIssuerExpired, fingerprint)
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrIssuerNotTrusted, fingerprint)
}
