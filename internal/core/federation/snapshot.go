package federation

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// errInvalidSnapshot reports a structural problem in a Snapshot that no more
// specific sentinel error covers.
var errInvalidSnapshot = errors.New("federation: invalid registry snapshot")

// Snapshot is a plain copy of the state of a Registry, for storage. It holds
// no reference to the Registry. Entries are sorted by domain name, so equal
// states give equal snapshots.
//
// A Snapshot is stored data, not trusted input. RestoreRegistry checks every
// field again, so a snapshot that was edited, truncated, or rolled back is
// refused.
type Snapshot struct {
	Entries []DomainSnapshot
}

// DomainSnapshot is the stored state of one trust domain: its link, its
// installed bundle, and its revocation.
type DomainSnapshot struct {
	// Domain is the canonical trust domain name.
	Domain string
	// AuthorityKey is the pinned Ed25519 bundle authority key.
	AuthorityKey []byte
	// Purposes lists the purposes that the link allows.
	Purposes []Purpose
	// HighestSequence is the highest bundle sequence ever accepted for the
	// domain. It is the rollback guard of ADR-0012, section 1. A Registry in
	// memory always binds the highest accepted sequence, so Snapshot sets this
	// field to the installed sequence. The field is separate from Bundle so that
	// a stored copy of it can be checked against the installed bundle.
	HighestSequence uint64
	// Bundle is the installed bundle.
	Bundle BundleRecord
	// Revoked reports whether the domain has been revoked.
	Revoked bool
	// RevokedAt is the effective time of the revocation. It is zero exactly when
	// Revoked is false.
	RevokedAt time.Time
}

// BundleRecord is the stored form of an installed bundle. Its digest is not
// stored. RestoreRegistry recomputes the digest from the sequence and the keys,
// so the signature check covers the stored content.
type BundleRecord struct {
	// Domain must match the Domain of the enclosing DomainSnapshot.
	Domain string
	// Sequence is the bundle sequence.
	Sequence uint64
	// ValidatedAt is the time at which the caller accepted the bundle. The
	// authority does not sign it, so RestoreRegistry cannot check it.
	ValidatedAt time.Time
	// Keys are the key identities of the bundle, in any order.
	Keys []BundleKey
	// AuthoritySignature is the signature of the pinned authority key over
	// BundleSigningInput of the bundle.
	AuthoritySignature []byte
}

// Snapshot returns a copy of the state of the Registry. The copy shares no memory
// with the Registry, so changing it does not change the Registry. Snapshot fails
// only for a nil or uninitialized Registry.
func (r *Registry) Snapshot() (Snapshot, error) {
	if r == nil || r.entries == nil {
		return Snapshot{}, ErrNilRegistry
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	domains := make([]TrustDomain, 0, len(r.entries))
	for d := range r.entries {
		domains = append(domains, d)
	}
	slices.SortFunc(domains, func(x, y TrustDomain) int {
		return strings.Compare(x.name, y.name)
	})

	out := Snapshot{Entries: make([]DomainSnapshot, 0, len(domains))}
	for _, d := range domains {
		e := r.entries[d]
		out.Entries = append(out.Entries, DomainSnapshot{
			Domain:          d.name,
			AuthorityKey:    slices.Clone(e.link.authorityKey),
			Purposes:        slices.Clone(e.link.purposes),
			HighestSequence: e.bundle.sequence,
			Bundle: BundleRecord{
				Domain:             d.name,
				Sequence:           e.bundle.sequence,
				ValidatedAt:        e.bundle.validatedAt,
				Keys:               e.bundle.Keys(),
				AuthoritySignature: slices.Clone(e.signature),
			},
			Revoked:   e.revoked,
			RevokedAt: e.revokedAt,
		})
	}
	return out, nil
}

// RestoreRegistry returns a Registry in the state that snap describes. The caller
// supplies maxBundleAge. It is a policy value, so it is not part of the Snapshot.
//
// snap is not trusted. Every link, bundle, and revocation is checked again with
// the rules that the mutating methods apply. The first violation returns an error
// and no Registry. The checks are:
//   - trust domains are strictly ascending by name, with no duplicates, and each
//     name is canonical;
//   - each link passes NewLink, and no two domains pin the same authority key;
//   - each bundle passes NewBundle, belongs to its domain, and carries an
//     authority signature that verifies under the pinned key;
//   - the installed sequence equals HighestSequence. A lower installed sequence
//     is a rollback and returns ErrRollback;
//   - a revocation time is present exactly when the domain is revoked.
//
// A revoked domain stays revoked. Federate still refuses it, and InstallBundle
// refuses bundles for it, as before the snapshot was taken.
func RestoreRegistry(maxBundleAge time.Duration, snap Snapshot) (*Registry, error) {
	r, err := NewRegistry(maxBundleAge)
	if err != nil {
		return nil, err
	}
	owners := make(map[string]string, len(snap.Entries))
	for i, d := range snap.Entries {
		if i > 0 && d.Domain <= snap.Entries[i-1].Domain {
			return nil, fmt.Errorf("%w: domain %q must come after %q, entries must be strictly ascending", errInvalidSnapshot, d.Domain, snap.Entries[i-1].Domain)
		}
		e, err := restoreEntry(d)
		if err != nil {
			return nil, err
		}
		key := string(e.link.authorityKey)
		if other, reused := owners[key]; reused {
			return nil, fmt.Errorf("%w: offered for %s and %s", ErrAuthorityKeyReused, other, d.Domain)
		}
		owners[key] = d.Domain
		r.entries[e.link.domain] = e
	}
	return r, nil
}

// restoreEntry checks one stored domain and rebuilds its entry.
func restoreEntry(d DomainSnapshot) (*entry, error) {
	domain, err := ParseTrustDomain(d.Domain)
	if err != nil {
		return nil, err
	}
	link, err := NewLink(domain, d.AuthorityKey, d.Purposes...)
	if err != nil {
		return nil, err
	}
	rec := d.Bundle
	if rec.Domain != d.Domain {
		return nil, fmt.Errorf("%w: bundle is for %q, link is for %q", ErrDomainMismatch, rec.Domain, d.Domain)
	}
	bundle, err := NewBundle(domain, rec.Sequence, rec.ValidatedAt, rec.Keys)
	if err != nil {
		return nil, err
	}
	if err := verifyBundleSignature(link.authorityKey, bundle, rec.AuthoritySignature); err != nil {
		return nil, err
	}
	switch {
	case bundle.sequence < d.HighestSequence:
		return nil, fmt.Errorf("%w: installed %d, highest accepted %d for %s", ErrRollback, bundle.sequence, d.HighestSequence, domain)
	case bundle.sequence > d.HighestSequence:
		return nil, fmt.Errorf("%w: installed sequence %d of %s is above the highest accepted %d", errInvalidSnapshot, bundle.sequence, domain, d.HighestSequence)
	}
	if d.Revoked && d.RevokedAt.IsZero() {
		return nil, fmt.Errorf("%w: %s is revoked without a revocation time", errInvalidSnapshot, domain)
	}
	if !d.Revoked && !d.RevokedAt.IsZero() {
		return nil, fmt.Errorf("%w: %s has a revocation time but is not revoked", errInvalidSnapshot, domain)
	}
	e := &entry{
		link:      link,
		bundle:    bundle,
		signature: slices.Clone(rec.AuthoritySignature),
		revoked:   d.Revoked,
	}
	if d.Revoked {
		e.revokedAt = d.RevokedAt.Round(0).UTC()
	}
	return e, nil
}
