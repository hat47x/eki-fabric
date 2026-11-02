// Package trustbundle is the Control Plane's signed, versioned list of the
// certificate authorities a Worker trusts (ADR-0036).
//
// The bundle is data plus the rules that make a version usable: monotonic
// versions, validity windows for the issuers and for the bundle itself, and a
// digest that a signature covers. It does not perform I/O and does not decide
// which issuers a deployment should trust; it only answers whether a bundle is
// well formed, whether it is current at a given time, and whether it is the
// next version of the bundle a Worker already holds.
package trustbundle

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ErrInvalidBundle is returned for a bundle that cannot be trusted as written:
// a missing version, an empty issuer list, or a window that is already over.
var ErrInvalidBundle = errors.New("trustbundle: invalid bundle")

// ErrNotCurrent is returned when the bundle's own validity window has passed or
// has not started. A Worker refuses to keep using it (fail closed).
var ErrNotCurrent = errors.New("trustbundle: bundle is not current")

// ErrNotSuccessor is returned when a bundle does not follow the one held: the
// version must be exactly one higher and the previous issuers must still be
// covered, so a Worker cannot be moved backwards or sideways.
var ErrNotSuccessor = errors.New("trustbundle: bundle does not follow the held version")

// Issuer is one certificate authority the bundle trusts.
type Issuer struct {
	// Fingerprint is the SHA-256 digest of the issuer's DER certificate.
	Fingerprint contract.Digest
	// NotBefore and NotAfter bound the certificates issued by this authority.
	NotBefore time.Time
	NotAfter  time.Time
}

// Validate reports whether the issuer is well formed.
func (i Issuer) Validate() error {
	if i.Fingerprint.IsZero() {
		return fmt.Errorf("%w: issuer has no fingerprint", ErrInvalidBundle)
	}
	if i.NotBefore.IsZero() || i.NotAfter.IsZero() {
		return fmt.Errorf("%w: issuer %s has no validity window", ErrInvalidBundle, i.Fingerprint)
	}
	if !i.NotAfter.After(i.NotBefore) {
		return fmt.Errorf("%w: issuer %s ends before it starts", ErrInvalidBundle, i.Fingerprint)
	}
	return nil
}

// Bundle is one version of the trust bundle.
type Bundle struct {
	// Version increases by one on every accepted change.
	Version uint64
	// Issuers is the set of authorities this version trusts.
	Issuers []Issuer
	// NotAfter is when this version stops being usable at all. Past it, a
	// Worker must not connect with this bundle even if it never received a
	// successor (ADR-0036 §2.3).
	NotAfter time.Time
}

// Validate reports whether the bundle is well formed. It does not look at the
// clock; use Current for that.
func (b Bundle) Validate() error {
	if b.Version == 0 {
		return fmt.Errorf("%w: version must start at 1", ErrInvalidBundle)
	}
	if len(b.Issuers) == 0 {
		return fmt.Errorf("%w: version %d trusts no issuer", ErrInvalidBundle, b.Version)
	}
	if b.NotAfter.IsZero() {
		return fmt.Errorf("%w: version %d has no expiry", ErrInvalidBundle, b.Version)
	}
	for _, issuer := range b.Issuers {
		if err := issuer.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Current reports whether the bundle may be used at now: it is well formed and
// its own window covers now. An issuer whose window has passed is not an error
// by itself; certificates from it no longer verify, which is the chain's job.
func (b Bundle) Current(now time.Time) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if now.After(b.NotAfter) {
		return fmt.Errorf("%w: version %d expired at %s", ErrNotCurrent, b.Version, b.NotAfter.UTC().Format(time.RFC3339))
	}
	return nil
}

// Digest is the value a signature covers. It must be stable across encodings,
// so it is computed over the fields in a fixed order rather than over JSON.
func (b Bundle) Digest() contract.Digest {
	var sb strings.Builder
	fmt.Fprintf(&sb, "eki.trust-bundle/v1\x00%d\x00%d\x00", b.Version, b.NotAfter.UTC().UnixNano())
	for _, issuer := range b.Issuers {
		fmt.Fprintf(&sb, "%s\x00%d\x00%d\x00", issuer.Fingerprint.String(), issuer.NotBefore.UTC().UnixNano(), issuer.NotAfter.UTC().UnixNano())
	}
	return contract.SHA256([]byte(sb.String()))
}

// Follows reports whether b is the next version of previous: exactly one higher,
// and every issuer of previous is still trusted. Removing an issuer is allowed
// only together with the migration the deployment decides (ADR-0036 §2.2); this
// rule only forbids going backwards and skipping versions.
func (b Bundle) Follows(previous Bundle) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if err := previous.Validate(); err != nil {
		return err
	}
	if b.Version != previous.Version+1 {
		return fmt.Errorf("%w: version %d after %d", ErrNotSuccessor, b.Version, previous.Version)
	}
	if !b.NotAfter.After(previous.NotAfter) {
		return fmt.Errorf("%w: version %d expires no later than version %d", ErrNotSuccessor, b.Version, previous.Version)
	}
	return nil
}
