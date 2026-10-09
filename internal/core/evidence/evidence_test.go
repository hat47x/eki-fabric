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

func decision(id, resultID string, accepted, verified bool) ResultDecision {
	return ResultDecision{ID: id, ResultID: resultID, Accepted: accepted, Verified: verified}
}

func suitability(id string, outcome SuitabilityOutcome) SuitabilityDecision {
	return SuitabilityDecision{ID: id, Outcome: outcome}
}

func retry(id string, authorityGrew bool) RetryEvent {
	return RetryEvent{ID: id, AuthorityGrew: authorityGrew}
}

func contribution(id, worker string, verified bool) ContributionRecord {
	return ContributionRecord{ID: id, WorkerID: worker, Verified: verified}
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
	if !slices.Equal(p.VerifiedResults.SourceIDs, []string{"res-obs-1", "res-obs-2"}) {
		t.Errorf("SourceIDs = %v, want both decisions", p.VerifiedResults.SourceIDs)
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
	if !slices.Equal(c.SourceIDs, []string{"con-1", "con-2", "con-3"}) {
		t.Errorf("SourceIDs = %v, want all three records", c.SourceIDs)
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
		contribution("con-1", "wkr-a", true),
		contribution("con-2", "wkr-c", false),
	}
	input := map[string]bool{}
	for _, o := range log {
		input[o.ObservationID()] = true
	}
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
	if !slices.Equal(s.SourceIDs, []string{"con-1", "con-2", "rec-1", "rec-2"}) {
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
