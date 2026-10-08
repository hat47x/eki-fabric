package federation

import "time"

// Binding is a read-only view of the binding for one trust domain. It maps the
// domain to one bundle, which fixes one sequence and one digest, and it keeps the
// revocation time after a domain is revoked. A Binding is a copy, so changing the
// Registry later does not change a Binding that was already returned.
type Binding struct {
	link      Link
	bundle    Bundle
	revoked   bool
	revokedAt time.Time
}

// Domain returns the trust domain that the binding is for.
func (b Binding) Domain() TrustDomain {
	return b.link.domain
}

// Bundle returns the bundle currently bound to the domain.
func (b Binding) Bundle() Bundle {
	return b.bundle
}

// Sequence returns the sequence number of the bound bundle.
func (b Binding) Sequence() uint64 {
	return b.bundle.sequence
}

// Digest returns the digest of the bound bundle.
func (b Binding) Digest() Digest {
	return b.bundle.digest
}

// ValidatedAt returns the validated-at time of the bound bundle.
func (b Binding) ValidatedAt() time.Time {
	return b.bundle.validatedAt
}

// Purposes returns the purposes that the domain's link allows.
func (b Binding) Purposes() []Purpose {
	return b.link.Purposes()
}

// Allows reports whether the domain's link allows p.
func (b Binding) Allows(p Purpose) bool {
	return b.link.Allows(p)
}

// Revoked reports whether the domain has been revoked, including a revocation
// whose effective time has not yet arrived.
func (b Binding) Revoked() bool {
	return b.revoked
}

// RevokedAt returns the effective time of the revocation. The value is zero if
// the domain has not been revoked.
func (b Binding) RevokedAt() time.Time {
	return b.revokedAt
}
