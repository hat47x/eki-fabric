package trustbundle

import (
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// IssuerFingerprint is the value a bundle lists for the authority that signed a
// certificate: the SHA-256 digest of the root certificate's DER form, the same
// value `openssl x509 -fingerprint -sha256` prints.
func IssuerFingerprint(cert *x509.Certificate) contract.Digest {
	if cert == nil {
		return contract.Digest{}
	}
	// The digest of the DER form, not the digest of the digest: this is the value
	// `openssl x509 -fingerprint -sha256` prints and the one a bundle lists (#466).
	return contract.FromSHA256(sha256.Sum256(cert.Raw))
}

// CheckPeerChains applies the bundle to the chains a TLS handshake verified. The
// chain's last certificate is the root that must be listed by the bundle, so a
// version that removed the root stops the next connection (ADR-0036 §2.3).
//
// It does not replace the TLS verification: the caller has already established
// that the chain is cryptographically valid. This check answers a different
// question — is this root one this version trusts.
func CheckPeerChains(b Bundle, chains [][]*x509.Certificate, now time.Time) error {
	if err := b.Current(now); err != nil {
		return err
	}
	if len(chains) == 0 {
		return fmt.Errorf("%w: the peer presented no chain", ErrIssuerNotTrusted)
	}
	var lastErr error
	for _, chain := range chains {
		if len(chain) == 0 {
			continue
		}
		if err := CheckIssuer(b, IssuerFingerprint(chain[len(chain)-1]), now); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: every chain was empty", ErrIssuerNotTrusted)
	}
	return lastErr
}
