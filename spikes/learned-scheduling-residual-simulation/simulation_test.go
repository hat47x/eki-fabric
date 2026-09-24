package residualsim

import (
	"reflect"
	"testing"

	shadowsched "eki.local/learned-scheduling-shadow-observation"
)

func TestGenerateDatasetDeterministicAndValidatorClean(t *testing.T) {
	first, err := GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same seed must produce identical dataset")
	}
	if first.Schema != DatasetSchema || first.ScenarioSetVersion != ScenarioSetVersion || first.GeneratorVersion != GeneratorVersion {
		t.Fatal("dataset identity mismatch")
	}
	if len(first.Observations) < 15 {
		t.Fatalf("expected broad scenario coverage, got %d", len(first.Observations))
	}
	for _, o := range first.Observations {
		if err := shadowsched.Validate(o); err != nil {
			t.Fatalf("%s: %v", o.ObservationID, err)
		}
		if !o.Boundary.ShadowOnly || o.Boundary.PlacementInfluenced || o.Boundary.AuthorizationInfluenced {
			t.Fatal("research boundary changed")
		}
	}
	if err := shadowsched.ValidateNoSplitLeakage(first.Observations); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEvaluationAxes(first); err != nil {
		t.Fatal(err)
	}
}

func TestDifferentSeedChangesOnlyDeterministicNoiseIdentity(t *testing.T) {
	a, err := GenerateDataset(1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateDataset(2)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(a, b) {
		t.Fatal("different seed must change dataset identity/outcomes")
	}
	if len(a.Observations) != len(b.Observations) {
		t.Fatal("seed must not change scenario count")
	}
}

func TestScenarioSetCoversPreregisteredAxes(t *testing.T) {
	d, err := GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	required := []string{
		"s1-compute-accurate", "s1-compute-mild-slow", "s1-compute-fast",
		"s1-compute-very-slow", "s1-worker-class-bias",
		"s2-bandwidth-nominal", "s2-bandwidth-mild", "s2-bandwidth-burst", "s2-bandwidth-asymmetric",
		"s3-cache-full", "s3-cache-partial", "s3-cache-cold", "s3-cache-stale",
		"s4-churn-preempt", "s4-churn-censored",
		"s5-deadline-loose", "s5-deadline-near", "s5-deadline-impossible", "s5-deadline-censored",
		"s6-compound",
	}
	seen := map[string]bool{}
	for _, o := range d.Observations {
		for _, name := range required {
			if len(o.ScenarioFamily) >= len(o.Split.Name)+1+len(name) &&
				o.ScenarioFamily[len(o.Split.Name)+1:] == name {
				seen[name] = true
			}
		}
	}
	for _, name := range required {
		if !seen[name] {
			t.Fatalf("missing preregistered scenario family %s", name)
		}
	}

	var partial *shadowsched.Observation
	for i := range d.Observations {
		o := &d.Observations[i]
		if o.ScenarioFamily == "train-s3-cache-partial" {
			partial = o
			break
		}
	}
	if partial == nil {
		t.Fatal("missing partial-cache fixture")
	}
	if partial.Baseline.LocalInputBytes == 0 || partial.Baseline.LocalInputBytes >= partial.Baseline.TotalInputBytes {
		t.Fatalf("partial cache must retain some but not all input bytes: %+v", partial.Baseline)
	}
}

func TestGeneralizationAxesSeparateConcreteLeakageGroupsFromFamilies(t *testing.T) {
	d, err := GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	trainWorkers := map[string]bool{}
	trainTimes := map[string]bool{}
	for _, o := range d.Observations {
		if o.Split.Name != "train" {
			continue
		}
		axes := d.EvaluationAxes[o.ObservationID]
		trainWorkers[axes.WorkerPopulationFamily] = true
		trainTimes[axes.TimeRegimeFamily] = true
	}
	var sawA, sawB, sawC bool
	for _, o := range d.Observations {
		if o.Split.Name != "test" {
			continue
		}
		axes := d.EvaluationAxes[o.ObservationID]
		unseenWorker := !trainWorkers[axes.WorkerPopulationFamily]
		unseenTime := !trainTimes[axes.TimeRegimeFamily]
		if unseenTime && !unseenWorker {
			sawA = true
		}
		if unseenWorker && !unseenTime {
			sawB = true
		}
		if unseenWorker && unseenTime && o.ScenarioFamily == "test-s6-compound" {
			sawC = true
		}
	}
	if !sawA || !sawB || !sawC {
		t.Fatalf("held-out axes A=%v B=%v C=%v", sawA, sawB, sawC)
	}
	if err := shadowsched.ValidateNoSplitLeakage(d.Observations); err != nil {
		t.Fatalf("abstract family reuse must not weaken concrete split identity: %v", err)
	}

	bad := d
	delete(bad.EvaluationAxes, bad.Observations[0].ObservationID)
	if err := ValidateEvaluationAxes(bad); err == nil {
		t.Fatal("missing evaluation-axis metadata must fail closed")
	}
}

func TestEvaluationAxesRejectConcreteGroupFamilyDrift(t *testing.T) {
	d, err := GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	firstByWorker := map[string]string{}
	mutated := false
	for _, o := range d.Observations {
		if firstID, ok := firstByWorker[o.WorkerPopulation]; ok {
			axes := d.EvaluationAxes[o.ObservationID]
			axes.WorkerPopulationFamily = d.EvaluationAxes[firstID].WorkerPopulationFamily + "-conflict"
			d.EvaluationAxes[o.ObservationID] = axes
			mutated = true
			break
		}
		firstByWorker[o.WorkerPopulation] = o.ObservationID
	}
	if !mutated {
		t.Fatal("fixture must contain a repeated concrete worker population")
	}
	if err := ValidateEvaluationAxes(d); err == nil {
		t.Fatal("one concrete worker population must not map to multiple abstract families")
	}
}

func TestResidualRederivesExactlyFromObservation(t *testing.T) {
	d, err := GenerateDataset(7)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, o := range d.Observations {
		r, ok := shadowsched.DurationResidualMillis(o)
		if !ok {
			continue
		}
		want := float64(*o.Outcome.ActualMillis) - float64(o.Baseline.EstimatedMillis)
		if r != want {
			t.Fatalf("%s residual=%v want=%v", o.ObservationID, r, want)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("expected completed residual targets")
	}
}

func TestCensoredAndIneligibleRowsNeverBecomeOptimizationTargets(t *testing.T) {
	d, err := GenerateDataset(9)
	if err != nil {
		t.Fatal(err)
	}
	seenCensored, seenIneligible := false, false
	for _, o := range d.Observations {
		if o.Outcome.Status == "censored" {
			seenCensored = true
			if _, known := shadowsched.CompletionBeforeDeadline(o); known {
				t.Fatalf("%s censored target became known", o.ObservationID)
			}
		}
		if !o.PolicyEligible {
			seenIneligible = true
			if _, ok := shadowsched.DurationResidualMillis(o); ok {
				t.Fatal("ineligible row received residual target")
			}
			if _, known := shadowsched.CompletionBeforeDeadline(o); known {
				t.Fatal("ineligible row received completion target")
			}
		}
	}
	if !seenCensored || !seenIneligible {
		t.Fatalf("fixture coverage censored=%v ineligible=%v", seenCensored, seenIneligible)
	}
}

func TestBaselineEvaluatorHasNoWinnerOrAuthority(t *testing.T) {
	d, err := GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	result, err := EvaluateBaselines(d, "valid", "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Schema != ResultSchema || result.ScenarioSetVersion != ScenarioSetVersion || result.GeneratorVersion != GeneratorVersion || result.EvaluatorVersion != EvaluatorVersion || result.DatasetSeed != d.Seed {
		t.Fatal("result identity mismatch")
	}
	if !result.ShadowOnly || result.PlacementInfluenced || result.AuthorizationInfluenced {
		t.Fatal("evaluation crossed research boundary")
	}
	if result.SingleCompositeScore || result.WinnerSelected {
		t.Fatal("baseline evaluator must not emit composite winner")
	}
	if result.Duration.KnownCompleted == 0 || result.Completion.KnownTargets == 0 {
		t.Fatal("expected evaluable targets")
	}
	if result.Completion.CensoredEligible == 0 {
		t.Fatal("expected censored evaluation row")
	}
	if result.Completion.KnownTargetCoverage >= 1.0 {
		t.Fatal("censoring should reduce known-target coverage")
	}
}

func TestSplitLeakageFailsClosed(t *testing.T) {
	d, err := GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	var train, valid *shadowsched.Observation
	for i := range d.Observations {
		o := &d.Observations[i]
		if o.Split.Name == "train" && train == nil {
			train = o
		}
		if o.Split.Name == "valid" && valid == nil {
			valid = o
		}
	}
	if train == nil || valid == nil {
		t.Fatal("missing split fixtures")
	}
	valid.ScenarioFamily = train.ScenarioFamily
	valid.Split.ScenarioFamily = train.ScenarioFamily
	if err := shadowsched.ValidateNoSplitLeakage(d.Observations); err == nil {
		t.Fatal("expected split leakage rejection")
	}
}

func TestEvaluationRejectsTrainAsEvaluationSplit(t *testing.T) {
	d, err := GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateBaselines(d, "train"); err == nil {
		t.Fatal("expected evaluation split rejection")
	}
}
