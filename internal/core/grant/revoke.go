package grant

import (
	"fmt"
	"time"
)

// Revoke returns a copy of g that stops admitting leases at effectiveAt. Only
// the provider the grant names may revoke it, and the effective time must not
// precede issuance. Revoke does not change g, its issued fields, or its digest,
// so earlier queries still answer what was true at those earlier times.
//
// A grant can be revoked once. The provider may still issue a new, narrower
// grant to the same beneficiary.
func (g Grant) Revoke(actor Identity, reason ReasonCategory, effectiveAt time.Time) (Grant, error) {
	if !g.issued {
		return Grant{}, ErrNilGrant
	}
	if err := checkIdentity("revoker", actor); err != nil {
		return Grant{}, err
	}
	if actor != g.provider {
		return Grant{}, ErrRevokerMismatch
	}
	if g.revoked {
		return Grant{}, ErrAlreadyRevoked
	}
	if !reason.Valid() {
		return Grant{}, ErrUnknownReason
	}
	if effectiveAt.IsZero() {
		return Grant{}, fmt.Errorf("%w: revocation effective time", ErrMissingField)
	}
	at := normalizeTime(effectiveAt)
	if at.Before(g.issuedAt) {
		return Grant{}, ErrRevocationBeforeIssue
	}
	out := g
	out.revoked = true
	out.revocation = Revocation{Reason: reason, EffectiveAt: at}
	return out, nil
}
