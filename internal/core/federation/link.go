package federation

import (
	"crypto/ed25519"
	"fmt"
	"slices"
)

// Purpose is a reason for which a federated identity may be accepted. The
// vocabulary is closed. Control Plane roles, federation administration, and
// verification authority are deliberately absent, so no federation link can
// grant them.
type Purpose string

const (
	// PurposeTaskSubmission accepts a federated identity as a client that submits Tasks.
	PurposeTaskSubmission Purpose = "task-submission"
	// PurposeExecutorParticipation accepts a federated identity as an Executor that runs Tasks.
	PurposeExecutorParticipation Purpose = "executor-participation"
	// PurposeDataClassAccess accepts a federated identity for the Data Classes its link names.
	PurposeDataClassAccess Purpose = "data-class-access"
	// PurposeComputeGrantUse accepts a federated identity as a user of a Compute Grant.
	PurposeComputeGrantUse Purpose = "compute-grant-use"
)

// Valid reports whether p is in the closed purpose vocabulary.
func (p Purpose) Valid() bool {
	switch p {
	case PurposeTaskSubmission, PurposeExecutorParticipation, PurposeDataClassAccess, PurposeComputeGrantUse:
		return true
	default:
		return false
	}
}

// Link is the federation configuration for one trust domain. It holds the domain
// name, the bundle authority key pinned for that domain, and the purposes that
// identities of the domain may be accepted for. A Link cannot be changed after
// it is created.
type Link struct {
	domain       TrustDomain
	authorityKey ed25519.PublicKey
	purposes     []Purpose
}

// NewLink returns the federation link for domain. authorityKey is the Ed25519 key
// that must sign every bundle of the domain, and it is fixed here at federation
// time rather than supplied per call. At least one purpose is required. An empty
// purpose set, a purpose outside the closed vocabulary (including wildcards), and
// a duplicated purpose are all errors, so a link can never grant blanket trust.
func NewLink(domain TrustDomain, authorityKey ed25519.PublicKey, purposes ...Purpose) (Link, error) {
	link := Link{
		domain:       domain,
		authorityKey: slices.Clone(authorityKey),
		purposes:     slices.Clone(purposes),
	}
	if err := link.validate(); err != nil {
		return Link{}, err
	}
	slices.Sort(link.purposes)
	return link, nil
}

func (l Link) validate() error {
	if !l.domain.Valid() {
		return fmt.Errorf("%w: trust domain is not set", ErrInvalidLink)
	}
	if len(l.authorityKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: bundle authority key must be a %d-byte Ed25519 public key", ErrInvalidLink, ed25519.PublicKeySize)
	}
	if len(l.purposes) == 0 {
		return fmt.Errorf("%w: at least one purpose is required; blanket trust is not allowed", ErrInvalidLink)
	}
	seen := make(map[Purpose]struct{}, len(l.purposes))
	for _, p := range l.purposes {
		if !p.Valid() {
			return fmt.Errorf("%w: purpose %q is not in the closed vocabulary", ErrInvalidLink, string(p))
		}
		if _, dup := seen[p]; dup {
			return fmt.Errorf("%w: purpose %q is listed twice", ErrInvalidLink, string(p))
		}
		seen[p] = struct{}{}
	}
	return nil
}

// Domain returns the trust domain this link federates.
func (l Link) Domain() TrustDomain {
	return l.domain
}

// AuthorityKey returns a copy of the pinned bundle authority key.
func (l Link) AuthorityKey() ed25519.PublicKey {
	return slices.Clone(l.authorityKey)
}

// Purposes returns a sorted copy of the allowed purpose set.
func (l Link) Purposes() []Purpose {
	return slices.Clone(l.purposes)
}

// Allows reports whether p is in the link's allowed purpose set. The zero Link
// allows nothing.
func (l Link) Allows(p Purpose) bool {
	return slices.Contains(l.purposes, p)
}
