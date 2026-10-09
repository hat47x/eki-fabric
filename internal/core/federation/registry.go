package federation

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Registry holds the federation links and the current binding of each federated
// trust domain. Every verification re-resolves the binding of the claimed domain,
// so no resolved bundle is cached across a change. Registry is safe for
// concurrent use. The zero value is not usable; create it with NewRegistry.
type Registry struct {
	mu           sync.RWMutex
	maxBundleAge time.Duration
	entries      map[TrustDomain]*entry
}

// entry is the federation state of one trust domain. A revoked entry is kept so
// that its history stays queryable and cannot be replaced.
type entry struct {
	link   Link
	bundle Bundle
	// signature is the authority signature over bundle. It is kept so that
	// Snapshot can store it and RestoreRegistry can verify it again.
	signature []byte
	revoked   bool
	revokedAt time.Time
}

// Verified is the result of a successful verification. It records which binding
// accepted the identity, so the decision can be traced later.
type Verified struct {
	Domain         TrustDomain
	Subject        string
	KeyID          string
	Purpose        Purpose
	BundleSequence uint64
	BundleDigest   Digest
}

// NewRegistry returns an empty Registry in which a bundle is stale once it was
// validated more than maxBundleAge before the verification time. maxBundleAge
// must be positive.
func NewRegistry(maxBundleAge time.Duration) (*Registry, error) {
	if maxBundleAge <= 0 {
		return nil, fmt.Errorf("%w: got %s", ErrInvalidMaxBundleAge, maxBundleAge)
	}
	return &Registry{
		maxBundleAge: maxBundleAge,
		entries:      make(map[TrustDomain]*entry),
	}, nil
}

// Federate establishes the link for a domain together with its first bundle.
// authoritySignature must be the Ed25519 signature, made with link's pinned
// authority key, over BundleSigningInput(bundle). A domain can be federated
// once per Registry, and no two domains can pin the same authority key.
func (r *Registry) Federate(link Link, bundle Bundle, authoritySignature []byte) error {
	if r == nil || r.entries == nil {
		return ErrNilRegistry
	}
	if err := link.validate(); err != nil {
		return err
	}
	if err := bundle.validate(); err != nil {
		return err
	}
	if bundle.domain != link.domain {
		return fmt.Errorf("%w: bundle is for %s, link is for %s", ErrDomainMismatch, bundle.domain, link.domain)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.entries[link.domain]; ok {
		return fmt.Errorf("%w: %s", ErrAlreadyFederated, link.domain)
	}
	for _, other := range r.entries {
		if bytes.Equal(other.link.authorityKey, link.authorityKey) {
			return fmt.Errorf("%w: offered for %s", ErrAuthorityKeyReused, link.domain)
		}
	}
	if err := verifyBundleSignature(link.authorityKey, bundle, authoritySignature); err != nil {
		return err
	}
	r.entries[link.domain] = &entry{link: link, bundle: bundle, signature: slices.Clone(authoritySignature)}
	return nil
}

// InstallBundle offers a bundle for its own domain, which must already be
// federated. authoritySignature must be the signature of the domain's pinned
// authority key over BundleSigningInput(bundle). The offer is then decided as
// follows:
//   - a sequence lower than the bound one is a rollback and is rejected;
//   - the bound sequence with a different digest is equivocation and is rejected;
//   - the bound sequence with the same digest is accepted, and validatedAt moves
//     forward only, so a bundle can be re-validated without changing its content;
//   - a higher sequence replaces the binding.
//
// Nothing can be installed for a revoked domain.
func (r *Registry) InstallBundle(bundle Bundle, authoritySignature []byte) error {
	if r == nil || r.entries == nil {
		return ErrNilRegistry
	}
	if err := bundle.validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.entries[bundle.domain]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFederated, bundle.domain)
	}
	if e.revoked {
		return fmt.Errorf("%w: %s", ErrDomainRevoked, bundle.domain)
	}
	if err := verifyBundleSignature(e.link.authorityKey, bundle, authoritySignature); err != nil {
		return err
	}
	switch {
	case bundle.sequence < e.bundle.sequence:
		return fmt.Errorf("%w: bound %d, offered %d", ErrRollback, e.bundle.sequence, bundle.sequence)
	case bundle.sequence > e.bundle.sequence:
		e.bundle = bundle
		e.signature = slices.Clone(authoritySignature)
	case bundle.digest != e.bundle.digest:
		return fmt.Errorf("%w: sequence %d", ErrEquivocation, bundle.sequence)
	case bundle.validatedAt.After(e.bundle.validatedAt):
		e.bundle.validatedAt = bundle.validatedAt
	}
	return nil
}

// Revoke removes a federated domain from federation from effectiveAt onward. The
// revocation is a local decision, so it needs no authority signature. The domain
// stays in the Registry with its binding and revocation time, and it cannot be
// federated or given a bundle again. The domain is revoked for any verification
// at or after effectiveAt, and it is still accepted before effectiveAt if its
// bundle is fresh.
func (r *Registry) Revoke(domain TrustDomain, effectiveAt time.Time) error {
	if r == nil || r.entries == nil {
		return ErrNilRegistry
	}
	if !domain.Valid() {
		return fmt.Errorf("%w: trust domain is not set", ErrInvalidTrustDomain)
	}
	if effectiveAt.IsZero() {
		return ErrInvalidTime
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.entries[domain]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFederated, domain)
	}
	if e.revoked {
		return fmt.Errorf("%w: %s", ErrAlreadyRevoked, domain)
	}
	e.revoked = true
	e.revokedAt = effectiveAt.Round(0).UTC()
	return nil
}

// Binding returns the current binding of one domain. A revoked domain is still
// returned, together with its revocation time. The Registry never offers a view
// across domains.
func (r *Registry) Binding(domain TrustDomain) (Binding, error) {
	if r == nil || r.entries == nil {
		return Binding{}, ErrNilRegistry
	}
	if !domain.Valid() {
		return Binding{}, fmt.Errorf("%w: trust domain is not set", ErrInvalidTrustDomain)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.entries[domain]
	if !ok {
		return Binding{}, fmt.Errorf("%w: %s", ErrNotFederated, domain)
	}
	return Binding{
		link:      e.link,
		bundle:    e.bundle,
		revoked:   e.revoked,
		revokedAt: e.revokedAt,
	}, nil
}

// Verify checks that id was signed by a key that is in the bundle bound to the
// domain id claims, and that the binding is fresh, not revoked, and linked to
// purpose at now. The signature covers the claimed domain, so a key bound to
// several domains cannot vouch for an identity of another one. Every failure is
// returned as a *Rejection with a stable Reason, and a nil error means the
// identity is accepted for purpose.
func (r *Registry) Verify(id Identity, purpose Purpose, now time.Time) (Verified, error) {
	if r == nil || r.entries == nil {
		return Verified{}, reject(ReasonMalformedInput, ErrNilRegistry)
	}
	if now.IsZero() {
		return Verified{}, reject(ReasonMalformedInput, ErrInvalidTime)
	}
	if !purpose.Valid() {
		return Verified{}, reject(ReasonUnknownPurpose, ErrUnknownPurpose)
	}
	if !id.Domain.Valid() || !isASCIIToken(id.Subject, maxSubjectBytes) ||
		!isASCIIToken(id.KeyID, maxKeyIDBytes) || len(id.Signature) != ed25519.SignatureSize {
		return Verified{}, reject(ReasonMalformedInput, ErrMalformedIdentity)
	}
	now = now.Round(0).UTC()

	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.entries[id.Domain]
	if !ok {
		return Verified{}, reject(ReasonNotFederated, ErrNotFederated)
	}
	if !e.link.Allows(purpose) {
		return Verified{}, reject(ReasonPurposeNotAllowed, ErrPurposeNotAllowed)
	}
	if e.revoked && !now.Before(e.revokedAt) {
		return Verified{}, reject(ReasonDomainRevoked, ErrDomainRevoked)
	}
	if e.bundle.validatedAt.After(now) {
		return Verified{}, reject(ReasonTrustBundleInvalid, ErrTrustBundleInvalid)
	}
	if now.After(e.bundle.validatedAt.Add(r.maxBundleAge)) {
		return Verified{}, reject(ReasonTrustBundleStale, ErrTrustBundleStale)
	}
	key, ok := e.bundle.lookupKey(id.KeyID)
	if !ok {
		return Verified{}, reject(ReasonUnknownKey, ErrUnknownKey)
	}
	if len(key.PublicKey) != ed25519.PublicKeySize {
		return Verified{}, reject(ReasonTrustBundleInvalid, ErrTrustBundleInvalid)
	}
	input, err := IdentitySigningInput(id.Domain, id.Subject)
	if err != nil {
		return Verified{}, reject(ReasonMalformedInput, err)
	}
	if !ed25519.Verify(key.PublicKey, input, id.Signature) {
		return Verified{}, reject(ReasonSignatureInvalid, ErrSignatureInvalid)
	}
	return Verified{
		Domain:         id.Domain,
		Subject:        id.Subject,
		KeyID:          id.KeyID,
		Purpose:        purpose,
		BundleSequence: e.bundle.sequence,
		BundleDigest:   e.bundle.digest,
	}, nil
}

// verifyBundleSignature checks the authority signature over a bundle. It returns
// ErrBundleSignature for every failure, including a malformed signature.
func verifyBundleSignature(authorityKey ed25519.PublicKey, bundle Bundle, signature []byte) error {
	input, err := BundleSigningInput(bundle)
	if err != nil {
		return err
	}
	if !ed25519.Verify(authorityKey, input, signature) {
		return fmt.Errorf("%w: sequence %d of %s", ErrBundleSignature, bundle.sequence, bundle.domain)
	}
	return nil
}
