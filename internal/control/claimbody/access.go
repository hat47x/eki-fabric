package claimbody

// Outcome is what a retrieval request is answered with (ADR-0037 §9, #494).
//
// The three cases are kept apart for one reason each, and the reasons do not
// overlap: telling "not yours" from "does not exist" would let anyone probe for
// identifiers, while treating "expired" as "does not exist" would hide from the
// requester that their own result has been removed.
type Outcome int

const (
	// OutcomeNotFound is the answer for a missing artifact, and for one the
	// requester has no right to see. The two are deliberately the same answer.
	OutcomeNotFound Outcome = iota
	// OutcomeExpired is the answer when the artifact existed and its retention
	// has passed. Only a requester with a right to see it gets this answer.
	OutcomeExpired
	// OutcomeWithdrawn is the answer when the Task that produced the artifact was
	// cancelled or otherwise ended before its retention passed (ADR-0037 §9,
	// #588). Only a requester with a right to see it gets this answer: the body may
	// still exist for an investigation, but it cannot be fetched.
	OutcomeWithdrawn
	// OutcomeUnreadable is the answer when the artifact exists but its metadata
	// cannot be read (I/O error, JSON corruption). It is separated from
	// OutcomeNotFound so audit can distinguish "does not exist" from "exists but
	// unreadable" (#809).
	OutcomeUnreadable
	// OutcomeDeliver means the body may be streamed to the requester.
	OutcomeDeliver
)

// Decide answers a retrieval request. requester and owner are Principal
// identities; owner is the principal that submitted the Task the artifact came
// from.
//
// The order matters: ownership is checked first, so the expired case can only be
// reached by someone who is allowed to know that the artifact existed. Withdrawal
// is checked before expiry because it is a state, not a time: a cancelled Task's
// body is not fetchable even if its retention would have allowed it.
func Decide(requester, owner string, exists, expired, withdrawn bool) Outcome {
	if !exists || owner == "" || requester != owner {
		return OutcomeNotFound
	}
	if withdrawn {
		return OutcomeWithdrawn
	}
	if expired {
		return OutcomeExpired
	}
	return OutcomeDeliver
}
