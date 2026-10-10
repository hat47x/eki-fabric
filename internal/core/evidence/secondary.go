package evidence

import "slices"

// Secondary holds the auxiliary metrics. They are reported for reference only,
// and ADR-0008 forbids claiming success from them alone. The type is kept apart
// from Primary so that a secondary number cannot be read as a primary one by
// accident. The guard test TestPrimaryMetricsCarryNoSecondaryMembers checks two things
// about the types reachable from Primary through struct fields and slices: no field in
// that tree has the Secondary type (so Primary does not embed Secondary), and no field
// name in that tree contains one of the secondaryMemberFragments, such as "workercount".
// The check is by fragment, not by full field name, so a field such as SourceIDs appears
// in both trees without failing the guard.
type Secondary struct {
	// SourceIDs lists every GovernorReclaim and ContributionRecord observation that
	// was examined, together with the supporting fact IDs of the contribution records,
	// in ascending order. Unverified records are included.
	SourceIDs []string
	// WorkerCount is the number of distinct worker IDs in GovernorReclaim records and
	// in verified ContributionRecords. Unverified contribution records do not add a
	// worker, because an unverified claim must not change a count.
	WorkerCount int
}

// DeriveSecondary derives the secondary metrics from an append-only log. It applies
// the same validation as DerivePrimary, and it does not modify the log.
//
// FLOPS and resource utilization are intentionally not derived yet. ADR-0008 names them
// as secondary metrics. The observation kinds in this package (GovernorReclaim,
// ResultDecision, SuitabilityDecision, RetryEvent, ContributionRecord) are defined here,
// not by ADR-0008, and none of them carries a FLOPS or utilization value, so this log
// cannot compute them. Deriving them needs an observation kind that records resource
// usage, and that design is still open. The criterion stays open rather than silently
// absent. See doc.go.
func DeriveSecondary(log []Observation) (Secondary, error) {
	b, err := split(log)
	if err != nil {
		return Secondary{}, err
	}
	workers := make(map[string]struct{})
	var ids []string
	for _, r := range b.reclaims {
		ids = append(ids, r.ID)
		workers[r.WorkerID] = struct{}{}
	}
	for _, c := range b.contribs {
		ids = append(ids, c.ID)
		ids = append(ids, c.SupportingFactIDs...)
		if c.Verified {
			workers[c.WorkerID] = struct{}{}
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	return Secondary{SourceIDs: ids, WorkerCount: len(workers)}, nil
}
