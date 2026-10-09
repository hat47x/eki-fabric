package evidence

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)

// at returns the time sec seconds after t0. Test timestamps are inputs, so they are
// built from constants rather than from the clock.
func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func completedReclaim(id, worker string, startSec, doneSec int) GovernorReclaim {
	return GovernorReclaim{ID: id, WorkerID: worker, ReclaimStart: at(startSec), ReclaimCompleted: at(doneSec)}
}

func pendingReclaim(id, worker string, startSec int) GovernorReclaim {
	return GovernorReclaim{ID: id, WorkerID: worker, ReclaimStart: at(startSec)}
}

// decision builds a result decision. A verified decision names a verification record
// derived from its ID, so the helper gives a valid verified result by default.
func decision(id, resultID string, accepted, verified bool) ResultDecision {
	d := ResultDecision{ID: id, ResultID: resultID, Accepted: accepted, Verified: verified}
	if verified {
		d.VerificationRecordID = "rec-" + id
	}
	return d
}

func suitability(id string, outcome SuitabilityOutcome) SuitabilityDecision {
	return SuitabilityDecision{ID: id, Outcome: outcome}
}

// retry builds a reclaim-caused retry. Tests that care about the cause or about duplicate
// execution build the RetryEvent literal directly.
func retry(id string, authorityGrew bool) RetryEvent {
	return RetryEvent{ID: id, Cause: RetryCauseReclaim, AuthorityGrew: authorityGrew}
}

// contribution builds a contribution record. A verified record cites a supporting fact
// derived from its ID, so the helper gives a valid verified record by default.
func contribution(id, worker string, verified bool) ContributionRecord {
	c := ContributionRecord{ID: id, WorkerID: worker, Verified: verified}
	if verified {
		c.SupportingFactIDs = []string{"fact-" + id}
	}
	return c
}

// mustDerive fails the test on error, so the call sites read as the main path.
func mustDerive(t *testing.T, log []Observation) Primary {
	t.Helper()
	p, err := DerivePrimary(log)
	if err != nil {
		t.Fatalf("DerivePrimary() error = %v", err)
	}
	return p
}

// TestReclaimLatencyIsDerivedFromGovernorTimestamps pins the nearest-rank rule with
// three completed reclaims of 2s, 3s, and 10s.
func TestReclaimLatencyIsDerivedFromGovernorTimestamps(t *testing.T) {
	log := []Observation{
		completedReclaim("rec-1", "wkr-a", 0, 3),
		completedReclaim("rec-2", "wkr-b", 0, 10),
		completedReclaim("rec-3", "wkr-c", 5, 7),
	}
	p := mustDerive(t, log)

	if got := p.ReclaimLatency.SourceIDs; !slices.Equal(got, []string{"rec-1", "rec-2", "rec-3"}) {
		t.Fatalf("SourceIDs = %v", got)
	}
	wantOrder := []ReclaimSample{
		{ObservationID: "rec-3", WorkerID: "wkr-c", Latency: 2 * time.Second},
		{ObservationID: "rec-1", WorkerID: "wkr-a", Latency: 3 * time.Second},
		{ObservationID: "rec-2", WorkerID: "wkr-b", Latency: 10 * time.Second},
	}
	if !reflect.DeepEqual(p.ReclaimLatency.Samples, wantOrder) {
		t.Fatalf("Samples = %+v, want %+v", p.ReclaimLatency.Samples, wantOrder)
	}
	cases := []struct {
		p    int
		want time.Duration
	}{
		{0, 2 * time.Second},
		{33, 2 * time.Second},
		{34, 3 * time.Second},
		{50, 3 * time.Second},
		{67, 10 * time.Second},
		{100, 10 * time.Second},
	}
	for _, c := range cases {
		got, ok, err := p.ReclaimLatency.Percentile(c.p)
		if err != nil || !ok || got != c.want {
			t.Errorf("Percentile(%d) = %v, %v, %v; want %v", c.p, got, ok, err, c.want)
		}
	}
}

// TestPendingReclaimIsListedButNotInTheDistribution keeps an unfinished reclaim
// visible. Dropping it would hide a resource that is still in use after opt-out.
func TestPendingReclaimIsListedButNotInTheDistribution(t *testing.T) {
	log := []Observation{
		completedReclaim("rec-1", "wkr-a", 0, 4),
		pendingReclaim("rec-2", "wkr-b", 1),
	}
	p := mustDerive(t, log)

	if !slices.Equal(p.ReclaimLatency.Pending, []string{"rec-2"}) {
		t.Fatalf("Pending = %v, want [rec-2]", p.ReclaimLatency.Pending)
	}
	if len(p.ReclaimLatency.Samples) != 1 || p.ReclaimLatency.Samples[0].ObservationID != "rec-1" {
		t.Fatalf("Samples = %+v, want only rec-1", p.ReclaimLatency.Samples)
	}
	if !slices.Equal(p.ReclaimLatency.SourceIDs, []string{"rec-1", "rec-2"}) {
		t.Fatalf("SourceIDs = %v, want both reclaims", p.ReclaimLatency.SourceIDs)
	}
}

// TestPercentileRejectsOutOfRangeAndReportsNoSampleAsAbsent checks that an empty
// distribution returns no latency instead of zero.
func TestPercentileRejectsOutOfRangeAndReportsNoSampleAsAbsent(t *testing.T) {
	p := mustDerive(t, []Observation{pendingReclaim("rec-1", "wkr-a", 0)})

	if _, _, err := p.ReclaimLatency.Percentile(-1); !errors.Is(err, ErrInvalidPercentile) {
		t.Errorf("Percentile(-1) error = %v, want ErrInvalidPercentile", err)
	}
	if _, _, err := p.ReclaimLatency.Percentile(101); !errors.Is(err, ErrInvalidPercentile) {
		t.Errorf("Percentile(101) error = %v, want ErrInvalidPercentile", err)
	}
	got, ok, err := p.ReclaimLatency.Percentile(50)
	if err != nil || ok || got != 0 {
		t.Errorf("Percentile(50) with only pending = %v, %v, %v; want absent", got, ok, err)
	}
}

func TestInvalidReclaimTimestampsAndIdentityAreRejected(t *testing.T) {
	cases := map[string]GovernorReclaim{
		"completed before start": {ID: "rec-1", WorkerID: "wkr-a", ReclaimStart: at(5), ReclaimCompleted: at(4)},
		"missing start":          {ID: "rec-1", WorkerID: "wkr-a"},
		"missing worker":         {ID: "rec-1", ReclaimStart: at(0), ReclaimCompleted: at(1)},
	}
	for name, rec := range cases {
		_, err := DerivePrimary([]Observation{rec})
		if !errors.Is(err, ErrInvalidObservation) {
			t.Errorf("%s: error = %v, want ErrInvalidObservation", name, err)
		}
	}
}

// TestUnverifiedAcceptedResultIsExcludedFromVerifiedShare is the negative test
// required for the verified share. An accepted result without verification stays in
// the denominator and never enters the numerator.
func TestUnverifiedAcceptedResultIsExcludedFromVerifiedShare(t *testing.T) {
	log := []Observation{
		decision("res-obs-1", "result-a", true, true),
		decision("res-obs-2", "result-b", true, false),
		decision("res-obs-3", "result-c", true, false),
	}
	p := mustDerive(t, log)

	share, ok := p.VerifiedResults.Share()
	if !ok || share != 1.0/3.0 {
		t.Fatalf("Share() = %v, %v; want 1/3, true", share, ok)
	}
	if !slices.Equal(p.VerifiedResults.Verified, []string{"res-obs-1"}) {
		t.Errorf("Verified = %v, want only res-obs-1", p.VerifiedResults.Verified)
	}
	if !slices.Equal(p.VerifiedResults.UnverifiedAccepted, []string{"res-obs-2", "res-obs-3"}) {
		t.Errorf("UnverifiedAccepted = %v", p.VerifiedResults.UnverifiedAccepted)
	}
	if !slices.Equal(p.VerifiedResults.Accepted, []string{"res-obs-1", "res-obs-2", "res-obs-3"}) {
		t.Errorf("Accepted = %v", p.VerifiedResults.Accepted)
	}
}

// TestOnlyUnverifiedAcceptedResultsGiveZeroShareNotOne checks that an all-unverified
// log reports a share of zero, not an absent or full share.
func TestOnlyUnverifiedAcceptedResultsGiveZeroShare(t *testing.T) {
	p := mustDerive(t, []Observation{decision("res-obs-1", "result-a", true, false)})
	share, ok := p.VerifiedResults.Share()
	if !ok || share != 0 {
		t.Fatalf("Share() = %v, %v; want 0, true", share, ok)
	}
}

// TestVerifiedButNotAcceptedResultStaysOutsideTheShare shows that a rejected result
// is not evidence of value, even when it was verified.
func TestVerifiedButNotAcceptedResultStaysOutsideTheShare(t *testing.T) {
	log := []Observation{
		decision("res-obs-1", "result-a", false, true),
		decision("res-obs-2", "result-b", true, true),
	}
	p := mustDerive(t, log)

	share, ok := p.VerifiedResults.Share()
	if !ok || share != 1 {
		t.Fatalf("Share() = %v, %v; want 1, true", share, ok)
	}
	if !slices.Equal(p.VerifiedResults.Accepted, []string{"res-obs-2"}) {
		t.Errorf("Accepted = %v, want only res-obs-2", p.VerifiedResults.Accepted)
	}
	// Both decisions and both verification records are examined, so all four IDs are traced.
	if !slices.Equal(p.VerifiedResults.SourceIDs, []string{"rec-res-obs-1", "rec-res-obs-2", "res-obs-1", "res-obs-2"}) {
		t.Errorf("SourceIDs = %v, want both decisions and both records", p.VerifiedResults.SourceIDs)
	}
}

// TestVerifiedResultWithoutVerificationRecordIsRejected checks that a verified result
// must name the record that verified it. The rule applies to a rejected result too,
// because it concerns the verified flag itself. The derivation fails with no partial
// result.
func TestVerifiedResultWithoutVerificationRecordIsRejected(t *testing.T) {
	cases := map[string]ResultDecision{
		"accepted, no record":  {ID: "res-obs-1", ResultID: "result-a", Accepted: true, Verified: true},
		"rejected, no record":  {ID: "res-obs-2", ResultID: "result-b", Accepted: false, Verified: true},
		"verified, blank name": {ID: "res-obs-3", ResultID: "result-c", Accepted: true, Verified: true, VerificationRecordID: "   "},
	}
	for name, d := range cases {
		p, err := DerivePrimary([]Observation{d})
		if !errors.Is(err, ErrInvalidObservation) {
			t.Errorf("%s: error = %v, want ErrInvalidObservation", name, err)
		}
		if !reflect.DeepEqual(p, Primary{}) {
			t.Errorf("%s: a rejected log must not yield a partial result: %+v", name, p)
		}
	}
}

// TestVerificationRecordIDMustBeAWellFormedIdentifier checks that an unverified result
// may carry a record name, but the name must still be a valid identifier, because it is
// traced into the output.
func TestVerificationRecordIDMustBeAWellFormedIdentifier(t *testing.T) {
	bad := ResultDecision{ID: "res-obs-1", ResultID: "result-a", Accepted: true, VerificationRecordID: " "}
	if _, err := DerivePrimary([]Observation{bad}); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("blank record name on an unverified result: error = %v, want ErrInvalidObservation", err)
	}
	ok := ResultDecision{ID: "res-obs-1", ResultID: "result-a", Accepted: true, VerificationRecordID: "ver-1"}
	if _, err := DerivePrimary([]Observation{ok}); err != nil {
		t.Fatalf("unverified result with a valid record name: error = %v", err)
	}
}

// TestVerificationRecordIDAppearsInSourceIDsAndLinksToItsResult checks that every
// verification record of a counted result is in SourceIDs, and that the links show
// which record verified which result. Two results may cite one record. It then appears
// once in SourceIDs, and both results link to it.
func TestVerificationRecordIDAppearsInSourceIDsAndLinksToItsResult(t *testing.T) {
	log := []Observation{
		ResultDecision{ID: "res-obs-2", ResultID: "result-b", Accepted: true, Verified: true, VerificationRecordID: "ver-batch-7"},
		ResultDecision{ID: "res-obs-1", ResultID: "result-a", Accepted: true, Verified: true, VerificationRecordID: "ver-batch-7"},
		ResultDecision{ID: "res-obs-3", ResultID: "result-c", Accepted: true, Verified: true, VerificationRecordID: "ver-3"},
	}
	p := mustDerive(t, log)

	wantSource := []string{"res-obs-1", "res-obs-2", "res-obs-3", "ver-3", "ver-batch-7"}
	if !slices.Equal(p.VerifiedResults.SourceIDs, wantSource) {
		t.Fatalf("SourceIDs = %v, want %v", p.VerifiedResults.SourceIDs, wantSource)
	}
	wantLinks := []EvidenceLink{
		{ObservationID: "res-obs-1", EvidenceID: "ver-batch-7"},
		{ObservationID: "res-obs-2", EvidenceID: "ver-batch-7"},
		{ObservationID: "res-obs-3", EvidenceID: "ver-3"},
	}
	if !reflect.DeepEqual(p.VerifiedResults.VerificationRecords, wantLinks) {
		t.Fatalf("VerificationRecords = %+v, want %+v", p.VerifiedResults.VerificationRecords, wantLinks)
	}
}

// TestVerificationRecordOfRejectedResultIsTracedButNotCounted checks that a rejected
// result's record is still examined, so it is in SourceIDs, but it does not become a
// link, because the rejected result is not in the share.
func TestVerificationRecordOfRejectedResultIsTracedButNotCounted(t *testing.T) {
	log := []Observation{
		ResultDecision{ID: "res-obs-1", ResultID: "result-a", Accepted: false, Verified: true, VerificationRecordID: "ver-1"},
		decision("res-obs-2", "result-b", true, true),
	}
	p := mustDerive(t, log)

	wantSource := []string{"rec-res-obs-2", "res-obs-1", "res-obs-2", "ver-1"}
	if !slices.Equal(p.VerifiedResults.SourceIDs, wantSource) {
		t.Fatalf("SourceIDs = %v, want %v", p.VerifiedResults.SourceIDs, wantSource)
	}
	wantLinks := []EvidenceLink{{ObservationID: "res-obs-2", EvidenceID: "rec-res-obs-2"}}
	if !reflect.DeepEqual(p.VerifiedResults.VerificationRecords, wantLinks) {
		t.Fatalf("VerificationRecords = %+v, want %+v", p.VerifiedResults.VerificationRecords, wantLinks)
	}
}

// TestSupportingFactMayNotReuseAVerificationRecordID checks that a supporting fact and a
// verification record cannot share a name, in either order. Both appear in SourceIDs, and
// that list keeps one entry per name, so a shared name would hide which kind of evidence
// a trace refers to. Two facts may still share a name, because one fact can back two
// records.
func TestSupportingFactMayNotReuseAVerificationRecordID(t *testing.T) {
	result := ResultDecision{ID: "res-obs-1", ResultID: "result-a", Accepted: true, Verified: true, VerificationRecordID: "ver-1"}
	fact := ContributionRecord{ID: "con-1", WorkerID: "wkr-a", Verified: true, SupportingFactIDs: []string{"ver-1"}}
	logs := map[string][]Observation{
		"record before fact": {result, fact},
		"fact before record": {fact, result},
	}
	for name, log := range logs {
		p, err := DerivePrimary(log)
		if !errors.Is(err, ErrInvalidObservation) {
			t.Errorf("%s: error = %v, want ErrInvalidObservation", name, err)
		}
		if !reflect.DeepEqual(p, Primary{}) {
			t.Errorf("%s: a rejected log must not yield a partial result: %+v", name, p)
		}
	}
}

// TestEvidenceIDMayNotReuseAnObservationID checks that a verification record or a
// supporting fact cannot share its name with an observation in the same log, in either
// order. Both kinds of ID appear in SourceIDs, so a shared name would make the trace
// ambiguous.
func TestEvidenceIDMayNotReuseAnObservationID(t *testing.T) {
	recordClash := ResultDecision{ID: "res-obs-1", ResultID: "result-a", Accepted: true, Verified: true, VerificationRecordID: "rty-1"}
	factClash := ContributionRecord{ID: "con-1", WorkerID: "wkr-a", Verified: true, SupportingFactIDs: []string{"rty-1"}}
	logs := map[string][]Observation{
		"record after observation":  {retry("rty-1", false), recordClash},
		"record before observation": {recordClash, retry("rty-1", false)},
		"fact after observation":    {retry("rty-1", false), factClash},
		"fact before observation":   {factClash, retry("rty-1", false)},
	}
	for name, log := range logs {
		p, err := DerivePrimary(log)
		if !errors.Is(err, ErrInvalidObservation) {
			t.Errorf("%s: error = %v, want ErrInvalidObservation", name, err)
		}
		if !reflect.DeepEqual(p, Primary{}) {
			t.Errorf("%s: a rejected log must not yield a partial result: %+v", name, p)
		}
	}
}

// TestNoAcceptedResultMeansNoShare checks that 0/0 is not reported as zero or as one.
func TestNoAcceptedResultMeansNoShare(t *testing.T) {
	p := mustDerive(t, []Observation{decision("res-obs-1", "result-a", false, false)})
	if share, ok := p.VerifiedResults.Share(); ok {
		t.Fatalf("Share() = %v, true; want no share", share)
	}
}

func TestDuplicateResultDecisionIsRejected(t *testing.T) {
	log := []Observation{
		decision("res-obs-1", "result-a", false, false),
		decision("res-obs-2", "result-a", true, true),
	}
	if _, err := DerivePrimary(log); !errors.Is(err, ErrDuplicateResult) {
		t.Fatalf("error = %v, want ErrDuplicateResult", err)
	}
}

// TestSuitabilityNotJustifiedIsCountedAsNormalOutcome checks that a decision to keep
// work local is counted as a result, not dropped or treated as a failure.
func TestSuitabilityNotJustifiedIsCountedAsNormalOutcome(t *testing.T) {
	log := []Observation{
		suitability("suit-1", SuitabilityFabricJustified),
		suitability("suit-2", SuitabilityFabricNotJustified),
		suitability("suit-3", SuitabilityFabricNotJustified),
		suitability("suit-4", SuitabilityNeedsMeasurement),
	}
	p := mustDerive(t, log)

	if got := p.SuitabilityOutcomes.Count(SuitabilityFabricJustified); got != 1 {
		t.Errorf("justified count = %d, want 1", got)
	}
	if got := p.SuitabilityOutcomes.Count(SuitabilityFabricNotJustified); got != 2 {
		t.Errorf("not-justified count = %d, want 2", got)
	}
	if got := p.SuitabilityOutcomes.Count(SuitabilityNeedsMeasurement); got != 1 {
		t.Errorf("needs-measurement count = %d, want 1", got)
	}
	if !slices.Equal(p.SuitabilityOutcomes.NotJustified, []string{"suit-2", "suit-3"}) {
		t.Errorf("NotJustified = %v", p.SuitabilityOutcomes.NotJustified)
	}
	if got := p.SuitabilityOutcomes.Count("PRICE_BASED"); got != 0 {
		t.Errorf("Count(unknown) = %d, want 0", got)
	}
}

func TestUnknownSuitabilityOutcomeIsRejected(t *testing.T) {
	_, err := DerivePrimary([]Observation{suitability("suit-1", "PRICE_BASED")})
	if !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("error = %v, want ErrInvalidObservation", err)
	}
}

func TestSuitabilityOutcomesIsTheClosedSet(t *testing.T) {
	got := SuitabilityOutcomes()
	want := []SuitabilityOutcome{"FABRIC_JUSTIFIED", "FABRIC_NOT_JUSTIFIED", "NEEDS_MEASUREMENT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SuitabilityOutcomes() = %v, want %v", got, want)
	}
	for _, o := range got {
		if !o.Valid() {
			t.Errorf("%q is listed but not Valid", o)
		}
	}
	if SuitabilityOutcome("").Valid() {
		t.Errorf("empty outcome must not be Valid")
	}
}

// TestAuthorityGrowthRetryIsCounted is the negative test for the retry invariant. A
// retry where authority grew must appear in the count and must break the invariant.
func TestAuthorityGrowthRetryIsCounted(t *testing.T) {
	log := []Observation{
		retry("rty-1", false),
		retry("rty-2", true),
		retry("rty-3", false),
	}
	p := mustDerive(t, log)
	r := p.RetryAuthorityGrowth

	if r.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", r.Count())
	}
	if !slices.Equal(r.AuthorityGrew, []string{"rty-2"}) {
		t.Errorf("AuthorityGrew = %v, want [rty-2]", r.AuthorityGrew)
	}
	if r.InvariantHolds() {
		t.Errorf("InvariantHolds() = true, want false when authority grew")
	}
	if !slices.Equal(r.SourceIDs, []string{"rty-1", "rty-2", "rty-3"}) {
		t.Errorf("SourceIDs = %v, want all three retries", r.SourceIDs)
	}
}

func TestRetriesWithoutAuthorityGrowthHoldTheInvariant(t *testing.T) {
	p := mustDerive(t, []Observation{retry("rty-1", false), retry("rty-2", false)})
	if !p.RetryAuthorityGrowth.InvariantHolds() || p.RetryAuthorityGrowth.Count() != 0 {
		t.Fatalf("invariant should hold with zero growth retries: %+v", p.RetryAuthorityGrowth)
	}
}

// TestRetryCauseIsRequiredAndClosed checks that every retry names one of the closed
// causes. A retry with no cause or an unknown cause cannot be attributed, so the
// derivation rejects it instead of counting it as "other".
func TestRetryCauseIsRequiredAndClosed(t *testing.T) {
	for _, bad := range []RetryCause{"", "PRICE_BASED", "reclaim"} {
		_, err := DerivePrimary([]Observation{RetryEvent{ID: "rty-1", Cause: bad}})
		if !errors.Is(err, ErrInvalidObservation) {
			t.Errorf("cause %q: error = %v, want ErrInvalidObservation", bad, err)
		}
	}
	want := []RetryCause{"RECLAIM", "WORKER_DEPARTURE", "OTHER"}
	if got := RetryCauses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RetryCauses() = %v, want %v", got, want)
	}
	for _, c := range RetryCauses() {
		if !c.Valid() {
			t.Errorf("%q is listed but not Valid", c)
		}
	}
	if RetryCause("").Valid() {
		t.Errorf("empty cause must not be Valid")
	}
}

// TestRetriesAreCountedByCause checks that each examined retry appears under its own
// cause, and that the counts by cause add up to the number of examined retries.
func TestRetriesAreCountedByCause(t *testing.T) {
	log := []Observation{
		RetryEvent{ID: "rty-3", Cause: RetryCauseOther},
		RetryEvent{ID: "rty-1", Cause: RetryCauseReclaim},
		RetryEvent{ID: "rty-2", Cause: RetryCauseWorkerDeparture},
		RetryEvent{ID: "rty-4", Cause: RetryCauseReclaim},
	}
	r := mustDerive(t, log).RetryAuthorityGrowth

	if got := r.ByCause.Count(RetryCauseReclaim); got != 2 {
		t.Errorf("reclaim count = %d, want 2", got)
	}
	if got := r.ByCause.Count(RetryCauseWorkerDeparture); got != 1 {
		t.Errorf("worker-departure count = %d, want 1", got)
	}
	if got := r.ByCause.Count(RetryCauseOther); got != 1 {
		t.Errorf("other count = %d, want 1", got)
	}
	if got := r.ByCause.Count("UNKNOWN"); got != 0 {
		t.Errorf("Count(unknown) = %d, want 0", got)
	}
	if !slices.Equal(r.ByCause.Reclaim, []string{"rty-1", "rty-4"}) {
		t.Errorf("Reclaim = %v, want [rty-1 rty-4]", r.ByCause.Reclaim)
	}
	if !slices.Equal(r.ByCause.WorkerDeparture, []string{"rty-2"}) {
		t.Errorf("WorkerDeparture = %v, want [rty-2]", r.ByCause.WorkerDeparture)
	}
	if !slices.Equal(r.ByCause.Other, []string{"rty-3"}) {
		t.Errorf("Other = %v, want [rty-3]", r.ByCause.Other)
	}
	total := r.ByCause.Count(RetryCauseReclaim) + r.ByCause.Count(RetryCauseWorkerDeparture) + r.ByCause.Count(RetryCauseOther)
	if total != len(r.SourceIDs) {
		t.Errorf("counts by cause sum to %d, want %d examined retries", total, len(r.SourceIDs))
	}
}

// TestDuplicateExecutionRetryIsCountedAndBreaksTheInvariant checks that a retry which
// made the same work run twice is harmful, even when authority did not grow. It is not
// counted as authority growth.
func TestDuplicateExecutionRetryIsCountedAndBreaksTheInvariant(t *testing.T) {
	log := []Observation{
		RetryEvent{ID: "rty-1", Cause: RetryCauseWorkerDeparture, DuplicateExecution: true},
		retry("rty-2", false),
	}
	r := mustDerive(t, log).RetryAuthorityGrowth

	if !slices.Equal(r.DuplicateExecution, []string{"rty-1"}) {
		t.Errorf("DuplicateExecution = %v, want [rty-1]", r.DuplicateExecution)
	}
	if !slices.Equal(r.Harmful, []string{"rty-1"}) {
		t.Errorf("Harmful = %v, want [rty-1]", r.Harmful)
	}
	if r.HarmfulCount() != 1 {
		t.Errorf("HarmfulCount() = %d, want 1", r.HarmfulCount())
	}
	if r.Count() != 0 || len(r.AuthorityGrew) != 0 {
		t.Errorf("duplicate execution must not count as authority growth: %+v", r)
	}
	if r.InvariantHolds() {
		t.Errorf("InvariantHolds() = true, want false when work ran twice")
	}
}

// TestRetryThatGrewAuthorityAndDuplicatedIsCountedOnce checks that a retry with both
// harms is one harmful retry, and that it appears in each specific list.
func TestRetryThatGrewAuthorityAndDuplicatedIsCountedOnce(t *testing.T) {
	log := []Observation{
		RetryEvent{ID: "rty-1", Cause: RetryCauseOther, AuthorityGrew: true, DuplicateExecution: true},
		retry("rty-2", false),
	}
	r := mustDerive(t, log).RetryAuthorityGrowth

	if r.HarmfulCount() != 1 || !slices.Equal(r.Harmful, []string{"rty-1"}) {
		t.Errorf("Harmful = %v (count %d), want [rty-1] once", r.Harmful, r.HarmfulCount())
	}
	if r.Count() != 1 || !slices.Equal(r.AuthorityGrew, []string{"rty-1"}) {
		t.Errorf("AuthorityGrew = %v, want [rty-1]", r.AuthorityGrew)
	}
	if !slices.Equal(r.DuplicateExecution, []string{"rty-1"}) {
		t.Errorf("DuplicateExecution = %v, want [rty-1]", r.DuplicateExecution)
	}
	if r.InvariantHolds() {
		t.Errorf("InvariantHolds() = true, want false")
	}
}

// TestEmptyLogDoesNotProveTheRetryInvariant guards against reading "no retries
// observed" as "authority never grew".
func TestEmptyLogDoesNotProveTheRetryInvariant(t *testing.T) {
	p := mustDerive(t, nil)
	if p.RetryAuthorityGrowth.InvariantHolds() {
		t.Fatalf("InvariantHolds() = true for an empty log, want false")
	}
}

// TestUnverifiedContributionIsNotCounted is the negative test for the verified
// contribution count. An unverified claim must never add to the count.
func TestUnverifiedContributionIsNotCounted(t *testing.T) {
	log := []Observation{
		contribution("con-1", "wkr-a", true),
		contribution("con-2", "wkr-b", false),
		contribution("con-3", "wkr-c", false),
	}
	p := mustDerive(t, log)
	c := p.VerifiedContributions

	if c.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", c.Count())
	}
	if !slices.Equal(c.Verified, []string{"con-1"}) {
		t.Errorf("Verified = %v, want [con-1]", c.Verified)
	}
	if !slices.Equal(c.SourceIDs, []string{"con-1", "con-2", "con-3", "fact-con-1"}) {
		t.Errorf("SourceIDs = %v, want all three records and the fact of the verified one", c.SourceIDs)
	}
}

// TestVerifiedContributionWithoutSupportingFactIsRejected checks that a verified
// contribution must name at least one supporting fact. The derivation fails with no
// partial result.
func TestVerifiedContributionWithoutSupportingFactIsRejected(t *testing.T) {
	cases := map[string]ContributionRecord{
		"verified, no facts":   {ID: "con-1", WorkerID: "wkr-a", Verified: true},
		"verified, blank fact": {ID: "con-2", WorkerID: "wkr-b", Verified: true, SupportingFactIDs: []string{"  "}},
	}
	for name, c := range cases {
		p, err := DerivePrimary([]Observation{c})
		if !errors.Is(err, ErrInvalidObservation) {
			t.Errorf("%s: error = %v, want ErrInvalidObservation", name, err)
		}
		if !reflect.DeepEqual(p, Primary{}) {
			t.Errorf("%s: a rejected log must not yield a partial result: %+v", name, p)
		}
	}
}

// TestSupportingFactIDMustBeWellFormed checks that a fact named by an unverified record
// is still a valid identifier, because it is traced into the output.
func TestSupportingFactIDMustBeWellFormed(t *testing.T) {
	bad := ContributionRecord{ID: "con-1", WorkerID: "wkr-a", SupportingFactIDs: []string{"fact\n1"}}
	if _, err := DerivePrimary([]Observation{bad}); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("control character in an unverified fact: error = %v, want ErrInvalidObservation", err)
	}
}

// TestSupportingFactIDsAppearInSourceIDsAndLinkToTheirRecord checks that the facts of
// every examined record are in SourceIDs. Links appear only for verified records. A
// fact that two records cite appears once in SourceIDs, and a repeated fact within one
// record gives one link.
func TestSupportingFactIDsAppearInSourceIDsAndLinkToTheirRecord(t *testing.T) {
	log := []Observation{
		ContributionRecord{ID: "con-2", WorkerID: "wkr-b", Verified: true, SupportingFactIDs: []string{"fact-a"}},
		ContributionRecord{ID: "con-1", WorkerID: "wkr-a", Verified: true, SupportingFactIDs: []string{"fact-b", "fact-a", "fact-b"}},
		ContributionRecord{ID: "con-3", WorkerID: "wkr-c", Verified: false, SupportingFactIDs: []string{"fact-c"}},
	}
	p := mustDerive(t, log)
	c := p.VerifiedContributions

	wantSource := []string{"con-1", "con-2", "con-3", "fact-a", "fact-b", "fact-c"}
	if !slices.Equal(c.SourceIDs, wantSource) {
		t.Fatalf("SourceIDs = %v, want %v", c.SourceIDs, wantSource)
	}
	wantLinks := []EvidenceLink{
		{ObservationID: "con-1", EvidenceID: "fact-a"},
		{ObservationID: "con-1", EvidenceID: "fact-b"},
		{ObservationID: "con-2", EvidenceID: "fact-a"},
	}
	if !reflect.DeepEqual(c.SupportingFacts, wantLinks) {
		t.Fatalf("SupportingFacts = %+v, want %+v", c.SupportingFacts, wantLinks)
	}
	if c.Count() != 2 {
		t.Fatalf("Count() = %d, want 2", c.Count())
	}
}

func TestDuplicateObservationIDIsRejectedWithoutPartialResult(t *testing.T) {
	log := []Observation{
		completedReclaim("obs-x", "wkr-a", 0, 1),
		contribution("obs-x", "wkr-a", true),
	}
	p, err := DerivePrimary(log)
	if !errors.Is(err, ErrDuplicateObservation) {
		t.Fatalf("error = %v, want ErrDuplicateObservation", err)
	}
	if !reflect.DeepEqual(p, Primary{}) {
		t.Fatalf("a rejected log must not yield a partial result: %+v", p)
	}
}

func TestNilAndForeignObservationsAreRejected(t *testing.T) {
	if _, err := DerivePrimary([]Observation{nil}); !errors.Is(err, ErrInvalidObservation) {
		t.Errorf("nil observation: error = %v, want ErrInvalidObservation", err)
	}
	// A pointer to a defined type has a different dynamic type and is not a value of
	// the closed set, so it must be refused rather than read through the pointer.
	ptr := &RetryEvent{ID: "rty-1"}
	if _, err := DerivePrimary([]Observation{ptr}); !errors.Is(err, ErrUnknownObservation) {
		t.Errorf("pointer observation: error = %v, want ErrUnknownObservation", err)
	}
	if _, err := DerivePrimary([]Observation{foreignObservation{}}); !errors.Is(err, ErrUnknownObservation) {
		t.Errorf("foreign observation: error = %v, want ErrUnknownObservation", err)
	}
}

// foreignObservation satisfies Observation but is not one of the defined kinds.
type foreignObservation struct{}

func (foreignObservation) ObservationID() string { return "foreign-1" }
func (foreignObservation) observation()          {}

func TestIdentifierBoundsAreEnforced(t *testing.T) {
	long := strings.Repeat("a", MaxIDLength+1)
	exact := strings.Repeat("a", MaxIDLength)
	cases := map[string]struct {
		id      string
		wantErr bool
	}{
		"control character": {"obs\nx", true},
		"surrounding space": {" obs-1", true},
		"too long":          {long, true},
		"exact maximum":     {exact, false},
		"blank":             {"   ", true},
	}
	for name, c := range cases {
		_, err := DerivePrimary([]Observation{retry(c.id, false)})
		if c.wantErr && !errors.Is(err, ErrInvalidObservation) {
			t.Errorf("%s: error = %v, want ErrInvalidObservation", name, err)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}
}

// TestDerivationIsOrderIndependentAndDoesNotMutateInput checks two properties. The
// log is read but not changed, and the output does not depend on record order.
func TestDerivationIsOrderIndependentAndDoesNotMutateInput(t *testing.T) {
	log := []Observation{
		completedReclaim("rec-1", "wkr-a", 0, 3),
		pendingReclaim("rec-2", "wkr-b", 1),
		decision("res-obs-1", "result-a", true, true),
		decision("res-obs-2", "result-b", true, false),
		suitability("suit-1", SuitabilityFabricNotJustified),
		retry("rty-1", false),
		contribution("con-1", "wkr-a", true),
	}
	snapshot := slices.Clone(log)
	first := mustDerive(t, log)

	if !reflect.DeepEqual(log, snapshot) {
		t.Fatalf("DerivePrimary modified its input")
	}
	reversed := slices.Clone(log)
	slices.Reverse(reversed)
	second := mustDerive(t, reversed)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("output depends on record order:\nfirst  %+v\nsecond %+v", first, second)
	}
}

// TestEverySourceIDTracesBackToTheInput is the traceability check. Each metric's IDs
// must exist in the log, and each counted subset must be inside its SourceIDs. Every
// SourceIDs list is checked against the input, so no metric can cite an ID the log
// never recorded. The containment check runs even when a SourceIDs list is empty, so a
// deleted append cannot make the check pass by skipping.
func TestEverySourceIDTracesBackToTheInput(t *testing.T) {
	log := []Observation{
		completedReclaim("rec-1", "wkr-a", 0, 3),
		pendingReclaim("rec-2", "wkr-b", 1),
		decision("res-obs-1", "result-a", true, true),
		decision("res-obs-2", "result-b", true, false),
		decision("res-obs-3", "result-c", false, true),
		suitability("suit-1", SuitabilityFabricJustified),
		suitability("suit-2", SuitabilityNeedsMeasurement),
		retry("rty-1", true),
		RetryEvent{ID: "rty-2", Cause: RetryCauseWorkerDeparture, DuplicateExecution: true},
		contribution("con-1", "wkr-a", true),
		contribution("con-2", "wkr-c", false),
	}
	input := knownIDs(log)
	p := mustDerive(t, log)

	assertTraced := func(name string, ids []string, source []string) {
		t.Helper()
		for _, id := range ids {
			if !input[id] {
				t.Errorf("%s cites %q, which is not in the input", name, id)
			}
			if !slices.Contains(source, id) {
				t.Errorf("%s counts %q, which is outside its SourceIDs", name, id)
			}
		}
	}
	assertSourced := func(name string, source []string) {
		t.Helper()
		for _, id := range source {
			if !input[id] {
				t.Errorf("%s SourceIDs cites %q, which is not in the input", name, id)
			}
		}
	}
	assertTraced("reclaim sample", sampleIDs(p.ReclaimLatency.Samples), p.ReclaimLatency.SourceIDs)
	assertTraced("reclaim pending", p.ReclaimLatency.Pending, p.ReclaimLatency.SourceIDs)
	assertTraced("verified results", p.VerifiedResults.Verified, p.VerifiedResults.SourceIDs)
	assertTraced("accepted results", p.VerifiedResults.Accepted, p.VerifiedResults.SourceIDs)
	assertTraced("suitability justified", p.SuitabilityOutcomes.Justified, p.SuitabilityOutcomes.SourceIDs)
	assertTraced("suitability needs measurement", p.SuitabilityOutcomes.NeedsMeasurement, p.SuitabilityOutcomes.SourceIDs)
	assertTraced("retry growth", p.RetryAuthorityGrowth.AuthorityGrew, p.RetryAuthorityGrowth.SourceIDs)
	assertTraced("retry duplicate execution", p.RetryAuthorityGrowth.DuplicateExecution, p.RetryAuthorityGrowth.SourceIDs)
	assertTraced("retry harmful", p.RetryAuthorityGrowth.Harmful, p.RetryAuthorityGrowth.SourceIDs)
	assertTraced("retry by cause", p.RetryAuthorityGrowth.ByCause.WorkerDeparture, p.RetryAuthorityGrowth.SourceIDs)
	assertTraced("verified contributions", p.VerifiedContributions.Verified, p.VerifiedContributions.SourceIDs)

	assertSourced("reclaim latency", p.ReclaimLatency.SourceIDs)
	assertSourced("verified results", p.VerifiedResults.SourceIDs)
	assertSourced("suitability", p.SuitabilityOutcomes.SourceIDs)
	assertSourced("retry", p.RetryAuthorityGrowth.SourceIDs)
	assertSourced("verified contributions", p.VerifiedContributions.SourceIDs)

	secondary, err := DeriveSecondary(log)
	if err != nil {
		t.Fatalf("DeriveSecondary() error = %v", err)
	}
	assertSourced("secondary", secondary.SourceIDs)
}

// TestSuitabilitySourceIDsAreExactlyTheDecisionIDs pins the SourceIDs of the suitability
// counts to the suitability observations only. A retry or a reclaim in the same log must
// not leak in, and no decision may be dropped. Deleting the append in suitabilityOf must
// fail this test.
func TestSuitabilitySourceIDsAreExactlyTheDecisionIDs(t *testing.T) {
	log := []Observation{
		suitability("suit-3", SuitabilityNeedsMeasurement),
		retry("rty-1", false),
		suitability("suit-1", SuitabilityFabricJustified),
		completedReclaim("rec-1", "wkr-a", 0, 1),
		suitability("suit-2", SuitabilityFabricNotJustified),
	}
	p := mustDerive(t, log)

	want := []string{"suit-1", "suit-2", "suit-3"}
	if got := p.SuitabilityOutcomes.SourceIDs; !slices.Equal(got, want) {
		t.Fatalf("SourceIDs = %v, want %v", got, want)
	}
}

// knownIDs returns every identifier that the log supplies: the observation IDs and the
// evidence IDs that observations cite. A metric may cite only these.
func knownIDs(log []Observation) map[string]bool {
	known := map[string]bool{}
	for _, o := range log {
		known[o.ObservationID()] = true
		switch v := o.(type) {
		case ResultDecision:
			if v.VerificationRecordID != "" {
				known[v.VerificationRecordID] = true
			}
		case ContributionRecord:
			for _, f := range v.SupportingFactIDs {
				known[f] = true
			}
		}
	}
	return known
}

func sampleIDs(samples []ReclaimSample) []string {
	out := make([]string, 0, len(samples))
	for _, s := range samples {
		out = append(out, s.ObservationID)
	}
	return out
}

// TestSecondaryWorkerCountExcludesUnverifiedContributions checks that an unverified
// claim cannot add a worker to the auxiliary count, even though it is examined.
func TestSecondaryWorkerCountExcludesUnverifiedContributions(t *testing.T) {
	log := []Observation{
		completedReclaim("rec-1", "wkr-a", 0, 1),
		pendingReclaim("rec-2", "wkr-b", 0),
		contribution("con-1", "wkr-c", true),
		contribution("con-2", "wkr-d", false),
	}
	s, err := DeriveSecondary(log)
	if err != nil {
		t.Fatalf("DeriveSecondary() error = %v", err)
	}
	if s.WorkerCount != 3 {
		t.Errorf("WorkerCount = %d, want 3 (wkr-a, wkr-b, wkr-c)", s.WorkerCount)
	}
	if !slices.Equal(s.SourceIDs, []string{"con-1", "con-2", "fact-con-1", "rec-1", "rec-2"}) {
		t.Errorf("SourceIDs = %v", s.SourceIDs)
	}
}

func TestSecondaryAppliesTheSameValidationAsPrimary(t *testing.T) {
	log := []Observation{retry("obs-1", false), retry("obs-1", true)}
	if _, err := DeriveSecondary(log); !errors.Is(err, ErrDuplicateObservation) {
		t.Fatalf("DeriveSecondary() error = %v, want ErrDuplicateObservation", err)
	}
}

func TestEmptyLogYieldsNoEvidence(t *testing.T) {
	p := mustDerive(t, nil)
	if _, ok := p.VerifiedResults.Share(); ok {
		t.Error("empty log must give no share")
	}
	if _, ok, _ := p.ReclaimLatency.Percentile(50); ok {
		t.Error("empty log must give no latency")
	}
	if p.VerifiedContributions.Count() != 0 || p.RetryAuthorityGrowth.Count() != 0 {
		t.Error("empty log must give zero counts")
	}
}
