package evidence

import (
	"cmp"
	"slices"
	"time"
)

// Primary は、ADR-0008 の五つの主指標を表す。
//
// 各フィールドの SourceIDs は、その指標が導出時に確認した観測のIDである。
// カウントに入らなかった観測（未検証の申告や、未完了の reclaim など）も含む。
// 実際にカウントされた観測は、別のフィールドに分けて示す。
type Primary struct {
	ReclaimLatency        ReclaimLatency
	VerifiedResults       VerifiedResults
	SuitabilityOutcomes   SuitabilityCounts
	RetryAuthorityGrowth  RetryAuthorityGrowth
	VerifiedContributions VerifiedContributions
}

// DerivePrimary derives the five primary metrics from an append-only log.
//
// It reads the log and never modifies it. Its output does not depend on the order
// of the log. When any observation is invalid, it returns an error and no partial
// result, so a bad record cannot quietly shift a metric.
func DerivePrimary(log []Observation) (Primary, error) {
	b, err := split(log)
	if err != nil {
		return Primary{}, err
	}
	return Primary{
		ReclaimLatency:        latencyOf(b.reclaims),
		VerifiedResults:       verifiedOf(b.results),
		SuitabilityOutcomes:   suitabilityOf(b.outcomes),
		RetryAuthorityGrowth:  retryOf(b.retries),
		VerifiedContributions: contributionsOf(b.contribs),
	}, nil
}

// ReclaimLatency is the distribution of the time from a reclaim starting to the
// resource being released. It is measured on the governor's own records, not on
// central bookkeeping.
type ReclaimLatency struct {
	// SourceIDs lists every GovernorReclaim observation that was examined, both
	// completed and pending, in ascending order.
	SourceIDs []string
	// Samples lists the completed reclaims, in ascending order of latency. Ties are
	// broken by observation ID.
	Samples []ReclaimSample
	// Pending lists the reclaims that started but have no completion time. They are
	// kept here so that a resource still in use after the user returned is visible.
	// They are not in the distribution.
	Pending []string
}

// ReclaimSample is one completed reclaim and the observation it came from.
type ReclaimSample struct {
	ObservationID string
	WorkerID      string
	Latency       time.Duration
}

// Percentile returns the nearest-rank percentile of the completed reclaim
// latencies. For p in [0, 100] it returns the sample at rank ceil(p * n / 100),
// with the rank clamped to at least 1. The percentile is computed in integer
// arithmetic, so no floating-point rounding can move a rank.
//
// The second result is false when there are no completed reclaims. In that case
// the latency is not reported as zero. An out-of-range p returns ErrInvalidPercentile.
func (l ReclaimLatency) Percentile(p int) (time.Duration, bool, error) {
	if p < 0 || p > 100 {
		return 0, false, ErrInvalidPercentile
	}
	n := len(l.Samples)
	if n == 0 {
		return 0, false, nil
	}
	rank := max((p*n+99)/100, 1)
	return l.Samples[rank-1].Latency, true, nil
}

// EvidenceLink connects one observation to one piece of evidence that it cites, such as
// a verification record. A link lets a reader follow from a count to the record behind
// it. One record can back several results, so several links can share an EvidenceID.
type EvidenceLink struct {
	ObservationID string
	EvidenceID    string
}

// VerifiedResults is the share of accepted results that a verifier established.
type VerifiedResults struct {
	// SourceIDs lists every ResultDecision observation that was examined, together with
	// the verification record IDs those observations cite, in ascending order. Each ID
	// appears once even when several results cite the same record. Results that were
	// not accepted are included here but do not enter the share.
	SourceIDs []string
	// Accepted lists the accepted results. This is the denominator of the share.
	Accepted []string
	// Verified lists the results that were both accepted and verified. This is the
	// numerator of the share.
	Verified []string
	// UnverifiedAccepted lists the accepted results that were not verified. They are
	// in the denominator and never in the numerator.
	UnverifiedAccepted []string
	// VerificationRecords links each result in Verified to the record that established
	// it, in ascending order of observation ID. A rejected result has no link here, even
	// when it was verified, because it does not enter the share.
	VerificationRecords []EvidenceLink
}

// Share returns len(Verified) / len(Accepted). The second result is false when
// nothing was accepted. In that case no share exists, and the share is not reported
// as zero or as one.
func (v VerifiedResults) Share() (float64, bool) {
	if len(v.Accepted) == 0 {
		return 0, false
	}
	return float64(len(v.Verified)) / float64(len(v.Accepted)), true
}

// SuitabilityCounts is how many times the suitability gate returned each outcome,
// with the observation IDs behind each count. A decision not to distribute a task
// is a normal result and is counted like any other outcome.
type SuitabilityCounts struct {
	// SourceIDs lists every SuitabilityDecision observation, in ascending order.
	SourceIDs        []string
	Justified        []string
	NotJustified     []string
	NeedsMeasurement []string
}

// Count returns how many decisions had outcome o. An outcome outside the closed
// set returns zero.
func (c SuitabilityCounts) Count(o SuitabilityOutcome) int {
	switch o {
	case SuitabilityFabricJustified:
		return len(c.Justified)
	case SuitabilityFabricNotJustified:
		return len(c.NotJustified)
	case SuitabilityNeedsMeasurement:
		return len(c.NeedsMeasurement)
	}
	return 0
}

// RetryAuthorityGrowth counts the retries that broke the reliability contract of
// ADR-0008. A retry breaks it when the authority grew beyond the original attempt, or
// when it made the same work run a second time. The product invariant requires both
// kinds of count to be zero. The retries are also grouped by cause, so a reader can
// see whether reclaims or worker departures produce them.
type RetryAuthorityGrowth struct {
	// SourceIDs lists every RetryEvent observation that was examined, in ascending order.
	SourceIDs []string
	// AuthorityGrew lists the retries where authority grew, in ascending order.
	AuthorityGrew []string
	// DuplicateExecution lists the retries that made the same work run a second time, in
	// ascending order.
	DuplicateExecution []string
	// Harmful lists the retries that grew authority, caused duplicate execution, or both,
	// in ascending order. A retry that does both appears here once.
	Harmful []string
	// ByCause groups every examined retry under its cause.
	ByCause RetryCauseCounts
}

// RetryCauseCounts lists the examined retries under each cause, in ascending order.
// Every examined retry appears under exactly one cause, because the derivation rejects
// any other value.
type RetryCauseCounts struct {
	Reclaim         []string
	WorkerDeparture []string
	Other           []string
}

// Count returns how many retries had cause c. A value outside the closed set returns zero.
func (c RetryCauseCounts) Count(cause RetryCause) int {
	switch cause {
	case RetryCauseReclaim:
		return len(c.Reclaim)
	case RetryCauseWorkerDeparture:
		return len(c.WorkerDeparture)
	case RetryCauseOther:
		return len(c.Other)
	}
	return 0
}

// Count returns the number of retries where authority grew.
func (r RetryAuthorityGrowth) Count() int { return len(r.AuthorityGrew) }

// HarmfulCount returns the number of retries that grew authority, caused duplicate
// execution, or both.
func (r RetryAuthorityGrowth) HarmfulCount() int { return len(r.Harmful) }

// InvariantHolds reports whether the evidence supports the invariant. It requires at
// least one examined retry and no harmful retry. An empty log does not prove that
// authority never grew or that work never ran twice, so it returns false.
func (r RetryAuthorityGrowth) InvariantHolds() bool {
	return len(r.SourceIDs) > 0 && len(r.Harmful) == 0
}

// VerifiedContributions counts the verified contribution records. An unverified
// record is a claim and is never counted here.
type VerifiedContributions struct {
	// SourceIDs lists every ContributionRecord observation that was examined, together
	// with the supporting fact IDs those observations cite, in ascending order. This
	// includes unverified records. Each ID appears once.
	SourceIDs []string
	// Verified lists the records that were verified, in ascending order.
	Verified []string
	// SupportingFacts links each verified record to each fact it cites, in ascending
	// order of record ID. Unverified records have no link here, because a claim does not
	// count toward the metric.
	SupportingFacts []EvidenceLink
}

// Count returns the number of verified contribution records.
func (c VerifiedContributions) Count() int { return len(c.Verified) }

func latencyOf(rs []GovernorReclaim) ReclaimLatency {
	var out ReclaimLatency
	for _, r := range rs {
		out.SourceIDs = append(out.SourceIDs, r.ID)
		if r.ReclaimCompleted.IsZero() {
			out.Pending = append(out.Pending, r.ID)
			continue
		}
		out.Samples = append(out.Samples, ReclaimSample{
			ObservationID: r.ID,
			WorkerID:      r.WorkerID,
			Latency:       r.ReclaimCompleted.Sub(r.ReclaimStart),
		})
	}
	slices.SortFunc(out.Samples, func(a, b ReclaimSample) int {
		if c := cmp.Compare(a.Latency, b.Latency); c != 0 {
			return c
		}
		return cmp.Compare(a.ObservationID, b.ObservationID)
	})
	slices.Sort(out.SourceIDs)
	slices.Sort(out.Pending)
	return out
}

func verifiedOf(rs []ResultDecision) VerifiedResults {
	var out VerifiedResults
	for _, r := range rs {
		out.SourceIDs = append(out.SourceIDs, r.ID)
		if r.VerificationRecordID != "" {
			out.SourceIDs = append(out.SourceIDs, r.VerificationRecordID)
		}
		if !r.Accepted {
			continue
		}
		out.Accepted = append(out.Accepted, r.ID)
		if r.Verified {
			out.Verified = append(out.Verified, r.ID)
			out.VerificationRecords = append(out.VerificationRecords, EvidenceLink{ObservationID: r.ID, EvidenceID: r.VerificationRecordID})
		} else {
			out.UnverifiedAccepted = append(out.UnverifiedAccepted, r.ID)
		}
	}
	slices.Sort(out.SourceIDs)
	out.SourceIDs = slices.Compact(out.SourceIDs)
	slices.Sort(out.Accepted)
	slices.Sort(out.Verified)
	slices.Sort(out.UnverifiedAccepted)
	out.VerificationRecords = sortLinks(out.VerificationRecords)
	return out
}

// sortLinks orders links by observation ID and then by evidence ID, and drops exact
// duplicates. Ordering makes the output independent of the order of the log.
func sortLinks(links []EvidenceLink) []EvidenceLink {
	slices.SortFunc(links, func(a, b EvidenceLink) int {
		if c := cmp.Compare(a.ObservationID, b.ObservationID); c != 0 {
			return c
		}
		return cmp.Compare(a.EvidenceID, b.EvidenceID)
	})
	return slices.Compact(links)
}

func suitabilityOf(ds []SuitabilityDecision) SuitabilityCounts {
	var out SuitabilityCounts
	for _, d := range ds {
		out.SourceIDs = append(out.SourceIDs, d.ID)
		switch d.Outcome {
		case SuitabilityFabricJustified:
			out.Justified = append(out.Justified, d.ID)
		case SuitabilityFabricNotJustified:
			out.NotJustified = append(out.NotJustified, d.ID)
		case SuitabilityNeedsMeasurement:
			out.NeedsMeasurement = append(out.NeedsMeasurement, d.ID)
		}
	}
	slices.Sort(out.SourceIDs)
	slices.Sort(out.Justified)
	slices.Sort(out.NotJustified)
	slices.Sort(out.NeedsMeasurement)
	return out
}

func retryOf(rs []RetryEvent) RetryAuthorityGrowth {
	var out RetryAuthorityGrowth
	for _, r := range rs {
		out.SourceIDs = append(out.SourceIDs, r.ID)
		if r.AuthorityGrew {
			out.AuthorityGrew = append(out.AuthorityGrew, r.ID)
		}
		if r.DuplicateExecution {
			out.DuplicateExecution = append(out.DuplicateExecution, r.ID)
		}
		if r.AuthorityGrew || r.DuplicateExecution {
			out.Harmful = append(out.Harmful, r.ID)
		}
		// split has already rejected every cause outside the closed set, so the switch
		// covers each retry exactly once.
		switch r.Cause {
		case RetryCauseReclaim:
			out.ByCause.Reclaim = append(out.ByCause.Reclaim, r.ID)
		case RetryCauseWorkerDeparture:
			out.ByCause.WorkerDeparture = append(out.ByCause.WorkerDeparture, r.ID)
		case RetryCauseOther:
			out.ByCause.Other = append(out.ByCause.Other, r.ID)
		}
	}
	slices.Sort(out.SourceIDs)
	slices.Sort(out.AuthorityGrew)
	slices.Sort(out.DuplicateExecution)
	slices.Sort(out.Harmful)
	slices.Sort(out.ByCause.Reclaim)
	slices.Sort(out.ByCause.WorkerDeparture)
	slices.Sort(out.ByCause.Other)
	return out
}

func contributionsOf(cs []ContributionRecord) VerifiedContributions {
	var out VerifiedContributions
	for _, c := range cs {
		out.SourceIDs = append(out.SourceIDs, c.ID)
		out.SourceIDs = append(out.SourceIDs, c.SupportingFactIDs...)
		if c.Verified {
			out.Verified = append(out.Verified, c.ID)
			for _, f := range c.SupportingFactIDs {
				out.SupportingFacts = append(out.SupportingFacts, EvidenceLink{ObservationID: c.ID, EvidenceID: f})
			}
		}
	}
	slices.Sort(out.SourceIDs)
	out.SourceIDs = slices.Compact(out.SourceIDs)
	slices.Sort(out.Verified)
	out.SupportingFacts = sortLinks(out.SupportingFacts)
	return out
}
