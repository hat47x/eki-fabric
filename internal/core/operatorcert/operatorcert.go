// Package operatorcert reads the identity and role an Operator certificate
// carries (ADR-0038).
//
// The role lives in the URI SANs rather than in a custom extension because the
// Worker certificates already use URI names, so one parser style covers both. Two
// names are required: one says who, one says what they may do.
package operatorcert

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Scheme is the URI scheme of an EKI name.
const Scheme = "eki"

// Role is what the certificate allows (ADR-0038 §2).
type Role string

const (
	// RoleOperator may approve Trust Bundle versions.
	RoleOperator Role = "operator"
	// RoleAdministrator may manage Workers and Principals.
	RoleAdministrator Role = "administrator"
)

// Valid reports whether the role is one this package knows.
func (r Role) Valid() bool { return r == RoleOperator || r == RoleAdministrator }

// Identity is who the certificate names and what it may do.
type Identity struct {
	TrustDomain string
	Name        string
	Role        Role
}

// ErrInvalidName means the certificate's URI SANs are not an EKI operator name.
var ErrInvalidName = errors.New("operatorcert: invalid name")

// ErrWrongRoles means the certificate carries zero roles or more than one.
var ErrWrongRoles = errors.New("operatorcert: a certificate must carry exactly one role")

// Parse reads the identity and the role from a certificate's URI SANs. A
// certificate naming both roles is refused: ADR-0038 keeps the two duties apart
// so that one compromised certificate cannot both approve trust and manage the
// identities that trust is granted to.
func Parse(uris []*url.URL) (Identity, error) {
	var (
		identity  *Identity
		roleNames []Role
	)
	for _, u := range uris {
		domain, segment, name, err := split(u)
		if err != nil {
			return Identity{}, err
		}
		switch segment {
		case "operator", "administrator":
			if identity != nil {
				return Identity{}, fmt.Errorf("%w: more than one identity name", ErrInvalidName)
			}
			identity = &Identity{TrustDomain: domain, Name: name, Role: Role(segment)}
		case "role":
			if !Role(name).Valid() {
				return Identity{}, fmt.Errorf("%w: unknown role %q", ErrInvalidName, name)
			}
			roleNames = append(roleNames, Role(name))
		default:
			return Identity{}, fmt.Errorf("%w: unknown segment %q", ErrInvalidName, segment)
		}
	}
	if identity == nil {
		return Identity{}, fmt.Errorf("%w: no identity name", ErrInvalidName)
	}
	if len(roleNames) != 1 {
		return Identity{}, fmt.Errorf("%w: got %d", ErrWrongRoles, len(roleNames))
	}
	if roleNames[0] != identity.Role {
		return Identity{}, fmt.Errorf("%w: name says %q, role says %q", ErrInvalidName, identity.Role, roleNames[0])
	}
	return *identity, nil
}

// split reads one `eki://<domain>/<segment>/<name>` URI. Every part is fixed: no
// user info, no query, no port, no escaped characters in the name.
func split(u *url.URL) (domain, segment, name string, err error) {
	if u == nil || u.Scheme != Scheme || u.Opaque != "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" || u.RawFragment != "" || u.ForceQuery || u.Host == "" || u.Port() != "" {
		return "", "", "", fmt.Errorf("%w: not an %s://<trust-domain>/<segment>/<name> name", ErrInvalidName, Scheme)
	}
	path := u.EscapedPath()
	rest, ok := strings.CutPrefix(path, "/")
	if !ok {
		return "", "", "", fmt.Errorf("%w: empty path", ErrInvalidName)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("%w: path must be <segment>/<name>", ErrInvalidName)
	}
	if strings.ContainsAny(rest, "%") {
		return "", "", "", fmt.Errorf("%w: escaped characters are not allowed", ErrInvalidName)
	}
	return u.Host, parts[0], parts[1], nil
}
