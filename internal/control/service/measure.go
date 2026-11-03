package service

import "github.com/hat47x/eki-fabric/internal/core/measurement"

// recallCounter turns a reclaim reason into an ADR-0039 count. It is a small
// adapter on purpose: leaserecall reports reasons, measurement knows the kinds,
// and neither has to import the other.
//
// An unknown reason is still counted, under AdmissionRejected's sibling rule: a
// reason this mapping does not know is a bug worth seeing in the numbers rather
// than dropping on the floor.
func recallCounter(counters *measurement.Counters) func(reason string) {
	if counters == nil {
		return nil
	}
	return func(reason string) {
		counters.Add(measurement.LeaseReclaimed, reason)
	}
}
