package operatorapi

import (
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// ErrOperatorCA means the Operator CA certificate could not be used to build a
// trust pool.
var ErrOperatorCA = errors.New("operatorapi: unusable operator CA")

// LoadCA reads the Operator CA certificate and returns the pool a TLS listener
// verifies client certificates with (ADR-0038 §2).
//
// The file is read once at startup: an Operator CA that changes while the process
// runs would silently change who may approve trust, which is exactly the change a
// restart and an approval are supposed to make visible.
func LoadCA(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: no path", ErrOperatorCA)
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOperatorCA, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%w: no certificate in %s", ErrOperatorCA, path)
	}
	return pool, nil
}
