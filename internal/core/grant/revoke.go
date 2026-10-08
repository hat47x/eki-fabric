package grant

import (
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// Ledger records the revocations of grants, keyed by grant digest. It is the
// one place that answers whether a grant has been revoked, so every copy of a
// grant gets the same answer, and so does a value re-created from the same
// fields. Keying by digest is what closes the gap that a per-value flag would
// leave open: a second copy of an unrevoked grant could otherwise be revoked
// again at a different time.
//
// The first revocation of a grant stands. A second one is refused whichever
// copy it is attempted on, so a revocation can neither be moved nor reversed.
// A revocation records only a reason and an effective time. It never changes
// a grant's fields or its digest, and a query for an earlier time still
// answers whether the grant was active then.
//
// The package keeps no other state, so the caller holds the ledger and passes
// it to Admit and PlanReclaim, as it passes ActiveLeases. The package does not
// persist the ledger. The caller must store each revocation durably before it
// relies on it, and must keep the ledger current across processes. A stale
// ledger admits leases the provider meant to stop.
//
// The zero value is an empty ledger and is ready to use. Pass a *Ledger and
// never copy a Ledger value: the map is allocated on the first Revoke, so a
// copy made before that point does not share later revocations with the
// original. A Ledger is not safe for concurrent use, so the caller serializes
// access.
type Ledger struct {
	revocations map[contract.Digest]Revocation
}

// Revoke records that the provider named by g stops admitting leases under g
// at effectiveAt. Only the provider that g names may revoke it, the effective
// time must not precede issuance, and a grant can be revoked once. Revoke does
// not change g.
//
// To narrow a grant, the provider revokes it and issues a new grant with lower
// ceilings. The new grant has a different digest, so the revocation does not
// carry over to it. A grant with identical fields has the same digest, so
// issuing one again does not escape a revocation.
func (l *Ledger) Revoke(g Grant, actor Identity, reason ReasonCategory, effectiveAt time.Time) error {
	if l == nil {
		return ErrNilLedger
	}
	if !g.issued {
		return ErrNilGrant
	}
	if err := checkIdentity("revoker", actor); err != nil {
		return err
	}
	if actor != g.provider {
		return ErrRevokerMismatch
	}
	d := g.Digest()
	if _, ok := l.revocations[d]; ok {
		return ErrAlreadyRevoked
	}
	if !reason.Valid() {
		return ErrUnknownReason
	}
	if effectiveAt.IsZero() {
		return fmt.Errorf("%w: revocation effective time", ErrMissingField)
	}
	at := normalizeTime(effectiveAt)
	if at.Before(g.issuedAt) {
		return ErrRevocationBeforeIssue
	}
	if l.revocations == nil {
		l.revocations = make(map[contract.Digest]Revocation)
	}
	l.revocations[d] = Revocation{Reason: reason, EffectiveAt: at}
	return nil
}

// revocationOf returns the revocation recorded for g, and false if none is
// recorded. The caller must check that l is not nil first. A nil ledger must
// never be read as "nothing revoked".
func (l *Ledger) revocationOf(g Grant) (Revocation, bool) {
	rev, ok := l.revocations[g.Digest()]
	return rev, ok
}
