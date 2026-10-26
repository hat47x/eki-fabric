package workerca

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// MaxCSRBytes bounds a certificate signing request. A P-256 request is about
// 250 bytes.
const MaxCSRBytes = 8 * 1024

// ParseCSR returns the public key of a certificate signing request given as
// PEM or DER. It verifies the request's self-signature, which shows the sender
// holds the private key, and it accepts only an ECDSA P-256 key.
//
// Everything else in the request is ignored, on purpose: the subject, the
// names, the extensions and the key usages are chosen by the CA, not by the
// requester (ADR-0027 section 2.2 item 8). A request that names another
// Worker still gets the registry's identity.
func ParseCSR(raw []byte) (*ecdsa.PublicKey, error) {
	if len(raw) == 0 || len(raw) > MaxCSRBytes {
		return nil, fmt.Errorf("%w: size must be 1..%d bytes", ErrInvalidCSR, MaxCSRBytes)
	}
	der := raw
	if trimmed := bytes.TrimSpace(raw); bytes.HasPrefix(trimmed, []byte("-----BEGIN")) {
		block, rest := pem.Decode(trimmed)
		if block == nil || (block.Type != "CERTIFICATE REQUEST" && block.Type != "NEW CERTIFICATE REQUEST") ||
			len(bytes.TrimSpace(rest)) != 0 {
			return nil, fmt.Errorf("%w: not a single PEM certificate request", ErrInvalidCSR)
		}
		der = block.Bytes
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot be parsed", ErrInvalidCSR)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: the signature does not verify", ErrInvalidCSR)
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: the key must be ECDSA P-256", ErrInvalidCSR)
	}
	return pub, nil
}
