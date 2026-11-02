package operatorapi

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
)

// ErrOperatorCertificate is returned when the request's certificate does not name
// an Operator of this deployment.
var ErrOperatorCertificate = errors.New("operatorapi: not an operator certificate")

// MTLSAuthenticator verifies the client certificate of a request against the
// Operator CA and reads the Operator it names (ADR-0038).
//
// The chain is verified by the standard library first: this function only answers
// the second question, whether the certificate names a role this process is
// willing to accept. Checking one is not a substitute for the other, which is why
// the roots are passed in rather than parsed from the peer.
func MTLSAuthenticator(roots *x509.CertPool, trustDomain string, now func() time.Time) Authenticator {
	if now == nil {
		now = time.Now
	}
	return func(r *http.Request) (operatorcert.Identity, error) {
		if r == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			return operatorcert.Identity{}, fmt.Errorf("%w: no client certificate", ErrOperatorCertificate)
		}
		leaf := r.TLS.PeerCertificates[0]
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots:       roots,
			KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			CurrentTime: now(),
		}); err != nil {
			return operatorcert.Identity{}, fmt.Errorf("%w: %v", ErrOperatorCertificate, err)
		}
		if leaf.IsCA {
			return operatorcert.Identity{}, fmt.Errorf("%w: a CA certificate is not an operator", ErrOperatorCertificate)
		}
		identity, err := operatorcert.Parse(leaf.URIs)
		if err != nil {
			return operatorcert.Identity{}, fmt.Errorf("%w: %v", ErrOperatorCertificate, err)
		}
		if trustDomain != "" && identity.TrustDomain != trustDomain {
			return operatorcert.Identity{}, fmt.Errorf("%w: trust domain %q", ErrOperatorCertificate, identity.TrustDomain)
		}
		return identity, nil
	}
}
