package federation

import (
	"fmt"
	"strings"
)

// maxTrustDomainBytes is the length limit for a canonical trust domain name.
const maxTrustDomainBytes = 253

// TrustDomain is the canonical name of an administrative boundary whose
// identities can be federated. The zero value is not a valid trust domain.
type TrustDomain struct {
	name string
}

// ParseTrustDomain returns the trust domain called name. The name must already
// be canonical: 1 to 253 bytes, each a lowercase ASCII letter, a digit, a dot,
// or a hyphen; no label between dots may be empty; and no label may begin or
// end with a hyphen. Names are compared exactly and never normalized, so
// "Example.org" is an error and not an alias of "example.org".
func ParseTrustDomain(name string) (TrustDomain, error) {
	if err := checkTrustDomainName(name); err != nil {
		return TrustDomain{}, err
	}
	return TrustDomain{name: name}, nil
}

// String returns the canonical name. The zero value returns an empty string.
func (d TrustDomain) String() string {
	return d.name
}

// Valid reports whether d names a trust domain. The zero value is not valid.
func (d TrustDomain) Valid() bool {
	return checkTrustDomainName(d.name) == nil
}

func checkTrustDomainName(name string) error {
	if len(name) == 0 || len(name) > maxTrustDomainBytes {
		return fmt.Errorf("%w: length %d is outside 1..%d bytes", ErrInvalidTrustDomain, len(name), maxTrustDomainBytes)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if !isLower && !isDigit && c != '-' && c != '.' {
			return fmt.Errorf("%w: byte 0x%02x at offset %d is not allowed", ErrInvalidTrustDomain, c, i)
		}
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return fmt.Errorf("%w: empty label", ErrInvalidTrustDomain)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%w: label %q begins or ends with a hyphen", ErrInvalidTrustDomain, label)
		}
	}
	return nil
}
