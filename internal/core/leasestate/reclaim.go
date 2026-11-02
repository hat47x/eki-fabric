package leasestate

import "time"

// ReclaimReason says why a lease is no longer active at now, and "" while it still
// is. Today the model records only an expiry, so the answer is "expired"; the
// function exists so a caller that counts reclamations (ADR-0039 §2.1) reads one
// decision instead of guessing from ActiveAt.
func (r LeaseRecord) ReclaimReason(now time.Time) string {
	if r.ActiveAt(now) {
		return ""
	}
	return "expired"
}
