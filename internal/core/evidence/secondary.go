package evidence

import "slices"

// Secondary holds the auxiliary metrics. They are reported for reference only,
// and ADR-0008 forbids claiming success from them alone. The type is kept apart
// from Primary so that a secondary number cannot be read as a primary one by
// accident. Secondary has no field that Primary contains, and the guard test
// checks this.
type Secondary struct {
	// SourceIDs lists every GovernorReclaim and ContributionRecord observation that
	// was examined, in ascending order. Unverified records are included.
	SourceIDs []string
	// WorkerCount is the number of distinct worker IDs in GovernorReclaim records and
	// in verified ContributionRecords. Unverified contribution records do not add a
	// worker, because an unverified claim must not change a count.
	WorkerCount int
}

// DeriveSecondary derives the secondary metrics from an append-only log. It applies
// the same validation as DerivePrimary, and it does not modify the log.
//
// FLOPS and utilization are not derived here. No observation kind defined by
// ADR-0008 carries them, so they cannot be computed from this log.
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
		if c.Verified {
			workers[c.WorkerID] = struct{}{}
		}
	}
	slices.Sort(ids)
	return Secondary{SourceIDs: ids, WorkerCount: len(workers)}, nil
}
