package grant

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// RecordedRevocation is one revocation held by a Ledger, together with the
// digest of the grant it applies to. Export returns these, and RestoreRevocation
// accepts the same fields, so a store can write a ledger out and read it back.
type RecordedRevocation struct {
	Grant      contract.Digest
	Revocation Revocation
}

// RestoreRevocation loads one revocation that an earlier run recorded, so that a
// Ledger can be rebuilt from stored state. It is the only way to fill a Ledger
// from outside Revoke.
//
// The Ledger holds digests, not grants, so the caller also passes issuedAt, the
// issuance time of the grant with that digest. The check that a revocation does
// not take effect before issuance needs that time. Without it, a stored record
// could place a revocation before the grant existed.
//
// RestoreRevocation checks that the record is well formed and agrees with
// itself. The digest must not be zero, both times must be set, the reason must
// be in the closed vocabulary, and the effective time must not be before
// issuance. Both times are normalized to UTC. The package cannot tell whether a
// record is genuine. The store must be trusted to hold only records that Revoke
// accepted.
//
// A digest that already has a revocation is refused with ErrAlreadyRevoked, so
// one digest holds at most one revocation. A refused call leaves the ledger
// unchanged. A nil ledger is refused with ErrNilLedger, so a nil ledger is never
// read as "nothing revoked".
func (l *Ledger) RestoreRevocation(d contract.Digest, issuedAt time.Time, rev Revocation) error {
	if l == nil {
		return ErrNilLedger
	}
	if d.IsZero() {
		return fmt.Errorf("%w: grant digest", ErrMissingField)
	}
	if issuedAt.IsZero() {
		return fmt.Errorf("%w: grant issuance time", ErrMissingField)
	}
	if !rev.Reason.Valid() {
		return ErrUnknownReason
	}
	if rev.EffectiveAt.IsZero() {
		return fmt.Errorf("%w: revocation effective time", ErrMissingField)
	}
	effective := normalizeTime(rev.EffectiveAt)
	if effective.Before(normalizeTime(issuedAt)) {
		return ErrRevocationBeforeIssue
	}
	if _, ok := l.revocations[d]; ok {
		return ErrAlreadyRevoked
	}
	if l.revocations == nil {
		l.revocations = make(map[contract.Digest]Revocation)
	}
	l.revocations[d] = Revocation{Reason: rev.Reason, EffectiveAt: effective}
	return nil
}

// Export returns every revocation the ledger holds, sorted by grant digest. Two
// ledgers that hold the same revocations export the same list, so a store can
// compare its ledger with the records it wrote. A nil ledger is refused with
// ErrNilLedger, because an empty list would read as "nothing revoked".
func (l *Ledger) Export() ([]RecordedRevocation, error) {
	if l == nil {
		return nil, ErrNilLedger
	}
	out := make([]RecordedRevocation, 0, len(l.revocations))
	for d, rev := range l.revocations {
		out = append(out, RecordedRevocation{Grant: d, Revocation: rev})
	}
	slices.SortFunc(out, func(a, b RecordedRevocation) int {
		return cmp.Compare(a.Grant.String(), b.Grant.String())
	})
	return out, nil
}
