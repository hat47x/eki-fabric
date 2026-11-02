package leasestate

import "time"

// Reclaim is one lease that is no longer active and the reason it is not.
type Reclaim struct {
	Lease  LeaseRecord
	Reason string
}

// Reclaimable picks the leases a scan should take back. It is the decision half of
// #555: the scan itself needs the store and the state transitions, while the
// question "which of these are done" is a pure function and can be tested without
// either.
//
// A lease is only picked when its reason is known. An entry the model cannot
// explain is left alone rather than reclaimed on a guess: taking a lease back from
// a Worker that is still working would be worse than leaving it a little longer.
func Reclaimable(leases []LeaseRecord, now time.Time) []Reclaim {
	out := make([]Reclaim, 0, len(leases))
	for _, lease := range leases {
		reason := lease.ReclaimReason(now)
		if reason == "" {
			continue
		}
		out = append(out, Reclaim{Lease: lease, Reason: reason})
	}
	return out
}
