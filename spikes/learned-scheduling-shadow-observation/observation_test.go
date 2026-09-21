package shadowsched

import (
	"encoding/json"
	"testing"
)

func u64(v uint64) *uint64 { return &v }

func fixture() Observation {
	return Observation{
		Schema:               Schema,
		ObservationID:        "obs-001",
		FeatureSchemaVersion: "eki-ml-0a-v0",
		ScenarioFamily:       "large-cached-artifact",
		WorkerPopulation:     "managed-cpu-v0",
		TimeRegime:           "sim-epoch-0",
		TaskClass:            "artifact-compute",
		WorkerClass:          "managed-workstation",
		PolicyEligible:       true,
		Baseline: BaselineEstimate{
			Eligible:            true,
			TotalInputBytes:     1000,
			LocalInputBytes:     600,
			TransferInputBytes:  400,
			TransferOutputBytes: 100,
			TransferBytes:       500,
			TransferMillis:      20,
			ComputeMillis:       80,
			EstimatedMillis:     100,
		},
		Outcome: Outcome{
			Status:         "completed",
			ObservedMillis: 120,
			ActualMillis:   u64(120),
			DeadlineMillis: u64(150),
		},
		Split: Split{
			Name:             "train",
			ScenarioFamily:   "large-cached-artifact",
			WorkerPopulation: "managed-cpu-v0",
			TimeRegime:       "sim-epoch-0",
		},
		Boundary: ResearchBoundary{ShadowOnly: true},
	}
}

func TestValidShadowObservationAndResidual(t *testing.T) {
	o := fixture()
	if err := Validate(o); err != nil {
		t.Fatal(err)
	}
	residual, ok := DurationResidualMillis(o)
	if !ok || residual != 20 {
		t.Fatalf("residual=%v ok=%v", residual, ok)
	}
	relative, ok := RelativeResidual(o)
	if !ok || relative != 0.2 {
		t.Fatalf("relative=%v ok=%v", relative, ok)
	}
	beforeDeadline, known := CompletionBeforeDeadline(o)
	if !known || !beforeDeadline {
		t.Fatalf("deadline=%v known=%v", beforeDeadline, known)
	}
}

func TestResearchBoundaryCannotInfluencePlacementOrAuthorization(t *testing.T) {
	for _, mutate := range []func(*Observation){
		func(o *Observation) { o.Boundary.ShadowOnly = false },
		func(o *Observation) { o.Boundary.PlacementInfluenced = true },
		func(o *Observation) { o.Boundary.AuthorizationInfluenced = true },
	} {
		o := fixture()
		mutate(&o)
		if err := Validate(o); err == nil {
			t.Fatal("expected research boundary rejection")
		}
	}
}

func TestBaselineArithmeticMustRemainSelfConsistent(t *testing.T) {
	cases := []func(*Observation){
		func(o *Observation) { o.Baseline.TransferInputBytes++ },
		func(o *Observation) { o.Baseline.TransferBytes++ },
		func(o *Observation) { o.Baseline.EstimatedMillis++ },
		func(o *Observation) { o.Baseline.LocalInputBytes = o.Baseline.TotalInputBytes + 1 },
	}
	for _, mutate := range cases {
		o := fixture()
		mutate(&o)
		if err := Validate(o); err == nil {
			t.Fatal("expected baseline arithmetic rejection")
		}
	}
}

func TestIneligibleRowsRemainSeparatedFromOptimization(t *testing.T) {
	o := fixture()
	o.PolicyEligible = false
	o.PolicyRejectReason = "data class not allowed"
	o.Baseline.Eligible = false
	o.Baseline.RejectReason = "data class not allowed"
	o.Baseline.EstimatedMillis = 0
	if err := Validate(o); err != nil {
		t.Fatal(err)
	}
	if _, ok := DurationResidualMillis(o); ok {
		t.Fatal("ineligible row must not yield residual target")
	}
}

func TestCensoredOrPreemptedRowsDoNotInventCompletionDuration(t *testing.T) {
	for _, status := range []string{"censored", "preempted", "failed"} {
		o := fixture()
		o.Outcome.Status = status
		o.Outcome.ActualMillis = nil
		o.Outcome.ObservedMillis = 70
		if err := Validate(o); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if _, ok := DurationResidualMillis(o); ok {
			t.Fatalf("%s produced residual", status)
		}
	}
}

func TestNonCompletedActualDurationFailsClosed(t *testing.T) {
	o := fixture()
	o.Outcome.Status = "preempted"
	if err := Validate(o); err == nil {
		t.Fatal("expected non-completed actual duration rejection")
	}
}

func TestStrictDecoderRejectsUnknownFields(t *testing.T) {
	raw, err := json.Marshal(fixture())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["scheduler_score"] = 0.9
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStrict(raw); err == nil {
		t.Fatal("expected unknown-field rejection")
	}
}

func TestSplitLeakageRejectsScenarioWorkerOrTimeGroupsAcrossSplits(t *testing.T) {
	a := fixture()
	b := fixture()
	b.ObservationID = "obs-002"
	b.Split.Name = "valid"
	if err := ValidateNoSplitLeakage([]Observation{a, b}); err == nil {
		t.Fatal("expected split leakage")
	}
}

func TestIndependentGroupsMayUseDifferentSplits(t *testing.T) {
	a := fixture()
	b := fixture()
	b.ObservationID = "obs-002"
	b.ScenarioFamily = "small-artifact"
	b.WorkerPopulation = "ad-hoc-cpu-v0"
	b.TimeRegime = "sim-epoch-1"
	b.Split = Split{Name: "valid", ScenarioFamily: b.ScenarioFamily, WorkerPopulation: b.WorkerPopulation, TimeRegime: b.TimeRegime}
	if err := ValidateNoSplitLeakage([]Observation{a, b}); err != nil {
		t.Fatal(err)
	}
}

func TestDeadlineForNonCompletionIsKnownFailureWhenDeadlinePresent(t *testing.T) {
	o := fixture()
	o.Outcome.Status = "preempted"
	o.Outcome.ActualMillis = nil
	o.Outcome.ObservedMillis = 60
	before, known := CompletionBeforeDeadline(o)
	if !known || before {
		t.Fatalf("before=%v known=%v", before, known)
	}
}
