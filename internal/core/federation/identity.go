package federation

import "fmt"

const (
	// maxSubjectBytes bounds the length of an identity subject.
	maxSubjectBytes = 256

	// identityTag separates identity signatures from bundle authority signatures.
	identityTag = "eki-federation-identity/v1"
)

// Identity is an identity presented for verification. Domain is the trust domain
// the identity claims, KeyID names the bundle key that is said to have signed it,
// and Signature is that key's Ed25519 signature over IdentitySigningInput(Domain,
// Subject). The claim alone grants nothing. Only a signature that verifies
// against the bundle bound to the claimed domain is accepted.
type Identity struct {
	Domain    TrustDomain
	Subject   string
	KeyID     string
	Signature []byte
}

// IdentitySigningInput returns the bytes that an issuer signs for an identity
// with the given subject in domain. The domain is part of the signed input, so a
// signature made for one domain does not verify for another, even when the same
// key is bound in both.
func IdentitySigningInput(domain TrustDomain, subject string) ([]byte, error) {
	if !domain.Valid() {
		return nil, fmt.Errorf("%w: trust domain is not set", ErrMalformedIdentity)
	}
	if !isASCIIToken(subject, maxSubjectBytes) {
		return nil, fmt.Errorf("%w: subject must be 1..%d printable ASCII bytes with no spaces", ErrMalformedIdentity, maxSubjectBytes)
	}
	buf := appendField(nil, []byte(identityTag))
	buf = appendField(buf, []byte(domain.String()))
	return appendField(buf, []byte(subject)), nil
}
