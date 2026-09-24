package modeldataset

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	residualsim "eki.local/learned-scheduling-residual-simulation"
)

func TestFreezeIsDeterministicAndRetainsShadowBoundary(t *testing.T) {
	first, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceDatasetDigest != second.SourceDatasetDigest ||
		first.ProjectedDatasetDigest != second.ProjectedDatasetDigest ||
		first.BaselineBundleDigest != second.BaselineBundleDigest {
		t.Fatal("same seed must produce stable freeze digests")
	}
	if first.TrainingPerformed ||
		first.PlacementInfluenced ||
		first.AuthorizationInfluenced ||
		first.SingleCompositeScore ||
		first.WinnerSelected {
		t.Fatal("0C-1 freeze must remain no-training/shadow/no-winner")
	}
	if first.Baselines.Valid.PlacementInfluenced ||
		first.Baselines.Valid.AuthorizationInfluenced ||
		first.Baselines.Test.PlacementInfluenced ||
		first.Baselines.Test.AuthorizationInfluenced {
		t.Fatal("baseline bundle influenced authority")
	}
	if first.Baselines.Valid.WinnerSelected ||
		first.Baselines.Test.WinnerSelected {
		t.Fatal("baseline bundle selected a winner")
	}
}

func TestDifferentSeedChangesFrozenDatasetIdentity(t *testing.T) {
	left, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Freeze(43)
	if err != nil {
		t.Fatal(err)
	}
	if left.SourceDatasetDigest == right.SourceDatasetDigest {
		t.Fatal("different deterministic seed must change source dataset identity")
	}
	if left.ProjectedDatasetDigest == right.ProjectedDatasetDigest {
		t.Fatal("different deterministic seed must change projected dataset identity")
	}
}

func TestFeatureProjectionExcludesProvenanceTargetsAndAuthority(t *testing.T) {
	bundle, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Dataset.Rows) == 0 {
		t.Fatal("missing projected rows")
	}
	raw, err := json.Marshal(bundle.Dataset.Rows[0].Features)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{
		"task_class": true,
		"worker_class": true,
		"total_input_bytes": true,
		"local_input_bytes": true,
		"transfer_input_bytes": true,
		"transfer_output_bytes": true,
		"transfer_bytes": true,
		"transfer_millis": true,
		"compute_millis": true,
		"estimated_millis": true,
		"local_input_fraction": true,
		"deadline_slack_millis": true,
	}
	if len(fields) != len(expected) {
		t.Fatalf("feature field count=%d want=%d: %v", len(fields), len(expected), fields)
	}
	for key := range fields {
		if !expected[key] {
			t.Fatalf("unexpected model feature %q", key)
		}
	}
	for _, forbidden := range []string{
		"scenario_family",
		"worker_population",
		"time_regime",
		"split",
		"outcome",
		"actual_completion_millis",
		"duration_residual_millis",
		"completion_before_deadline",
		"policy_eligible",
		"authorization",
		"worker_population_family",
		"time_regime_family",
		"shift_axes",
		"evaluation_axes",
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("model features leaked %q: %s", forbidden, raw)
		}
	}
}

func TestCensoredAndIneligibleRowsDoNotAcquireInventedTargets(t *testing.T) {
	bundle, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	var sawCensored, sawPreempted, sawIneligible bool
	for _, row := range bundle.Dataset.Rows {
		switch {
		case strings.Contains(row.Grouping.ScenarioFamily, "s4-churn-censored"):
			sawCensored = true
			if row.Targets.DurationResidualMillis != nil ||
				row.Targets.CompletionBeforeDeadline != nil {
				t.Fatal("censored row acquired optimization target")
			}
		case strings.Contains(row.Grouping.ScenarioFamily, "s4-churn-preempt"):
			sawPreempted = true
			if row.Targets.DurationResidualMillis != nil {
				t.Fatal("preempted row acquired duration target")
			}
			if row.Targets.CompletionBeforeDeadline == nil ||
				*row.Targets.CompletionBeforeDeadline {
				t.Fatal("preempted terminal outcome must be a known deadline miss")
			}
		case strings.Contains(row.Grouping.ScenarioFamily, "policy-reject"):
			sawIneligible = true
			if row.PolicyEligible {
				t.Fatal("policy-rejected row marked policy eligible")
			}
			if row.Targets.DurationResidualMillis != nil ||
				row.Targets.CompletionBeforeDeadline != nil {
				t.Fatal("policy-rejected row acquired optimization target")
			}
		}
	}
	if !sawCensored || !sawPreempted || !sawIneligible {
		t.Fatalf("fixture coverage censored=%v preempted=%v ineligible=%v", sawCensored, sawPreempted, sawIneligible)
	}
}

func TestProjectDatasetRejectsSourceIdentityAndSplitLeakage(t *testing.T) {
	source, err := residualsim.GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	badIdentity := source
	badIdentity.Schema = "unexpected"
	if _, _, err := ProjectDataset(badIdentity); err == nil {
		t.Fatal("expected source identity rejection")
	}

	leaky, err := residualsim.GenerateDataset(42)
	if err != nil {
		t.Fatal(err)
	}
	var trainFamily string
	for _, observation := range leaky.Observations {
		if observation.Split.Name == "train" {
			trainFamily = observation.ScenarioFamily
			break
		}
	}
	if trainFamily == "" {
		t.Fatal("missing train fixture")
	}
	mutated := false
	for index := range leaky.Observations {
		if leaky.Observations[index].Split.Name == "valid" {
			leaky.Observations[index].ScenarioFamily = trainFamily
			leaky.Observations[index].Split.ScenarioFamily = trainFamily
			mutated = true
			break
		}
	}
	if !mutated {
		t.Fatal("missing valid fixture")
	}
	if _, _, err := ProjectDataset(leaky); err == nil {
		t.Fatal("expected split leakage rejection")
	}
}

func TestCurrentScenarioSetProvidesLeakageCleanIsolatedABViews(t *testing.T) {
	bundle, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	readiness := bundle.Readiness
	if readiness.Schema != ReadinessSchema {
		t.Fatalf("readiness schema=%q", readiness.Schema)
	}
	if readiness.Slices.TestAUnseenTimeOnly != 1 {
		t.Fatalf("isolated Test-A rows=%d want=1", readiness.Slices.TestAUnseenTimeOnly)
	}
	if readiness.Slices.TestADurationTargets != 1 || readiness.Slices.TestACompletionTargets != 1 {
		t.Fatalf("Test-A targets=%+v", readiness.Slices)
	}
	if readiness.Slices.TestBUnseenWorkerOnly != 1 {
		t.Fatalf("isolated Test-B rows=%d want=1", readiness.Slices.TestBUnseenWorkerOnly)
	}
	if readiness.Slices.TestBDurationTargets != 1 || readiness.Slices.TestBCompletionTargets != 1 {
		t.Fatalf("Test-B targets=%+v", readiness.Slices)
	}
	if readiness.Slices.TestCCompound != 6 {
		t.Fatalf("compound Test-C rows=%d want=6", readiness.Slices.TestCCompound)
	}
	if readiness.Slices.TestCDurationTargets != 4 {
		t.Fatalf("Test-C duration targets=%d want=4", readiness.Slices.TestCDurationTargets)
	}
	if readiness.Slices.TestCCompletionTargets != 5 {
		t.Fatalf("Test-C completion targets=%d want=5", readiness.Slices.TestCCompletionTargets)
	}
	if !readiness.ModelFitReady {
		t.Fatalf("isolated Test-A/B/C coverage should make the next model-fit work unit mechanically ready: %v", readiness.BlockingReasons)
	}
	if len(readiness.BlockingReasons) != 0 {
		t.Fatalf("unexpected blockers=%v", readiness.BlockingReasons)
	}
}

func TestValidationOnlyShiftDoesNotSatisfyFinalTestReadiness(t *testing.T) {
	rows := []ComparisonRow{
		{
			ObservationID: "train",
			Split:         "train",
			Grouping: Grouping{
				ScenarioFamily:   "train-family",
				WorkerPopulation: "worker-train-concrete",
				TimeRegime:       "time-train-concrete",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-known",
				TimeRegimeFamily: "time-known",
			},
		},
		{
			ObservationID: "valid-time-only",
			Split:         "valid",
			Grouping: Grouping{
				ScenarioFamily:   "valid-time-shift",
				WorkerPopulation: "worker-valid-a",
				TimeRegime:       "time-valid-a",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-known",
				TimeRegimeFamily: "time-unseen",
				ShiftAxes: []string{"time_regime"},
			},
		},
		{
			ObservationID: "valid-worker-only",
			Split:         "valid",
			Grouping: Grouping{
				ScenarioFamily:   "valid-worker-shift",
				WorkerPopulation: "worker-valid-b",
				TimeRegime:       "time-valid-b",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-unseen",
				TimeRegimeFamily: "time-known",
				ShiftAxes: []string{"worker_population"},
			},
		},
		{
			ObservationID: "test-compound",
			Split:         "test",
			Grouping: Grouping{
				ScenarioFamily:   "s6-compound-test",
				WorkerPopulation: "worker-test-c",
				TimeRegime:       "time-test-c",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-unseen",
				TimeRegimeFamily: "time-unseen",
				ShiftAxes: []string{"worker_population", "time_regime", "compound"},
			},
		},
	}

	readiness := AssessModelFitReadiness(rows)
	if readiness.Slices.TestAUnseenTimeOnly != 0 {
		t.Fatalf("validation-only time shift counted as Test-A: %+v", readiness.Slices)
	}
	if readiness.Slices.TestBUnseenWorkerOnly != 0 {
		t.Fatalf("validation-only worker shift counted as Test-B: %+v", readiness.Slices)
	}
	if readiness.Slices.TestCCompound != 1 {
		t.Fatalf("Test-C count=%d want=1", readiness.Slices.TestCCompound)
	}
	if readiness.ModelFitReady {
		t.Fatal("validation-only shift coverage must not unlock model fitting")
	}
}

func TestHeldOutSliceWithoutKnownTargetsDoesNotUnlockReadiness(t *testing.T) {
	duration := 10.0
	completion := true
	rows := []ComparisonRow{
		{
			ObservationID: "train",
			Split:         "train",
			Grouping: Grouping{
				ScenarioFamily:   "train-family",
				WorkerPopulation: "worker-train-concrete",
				TimeRegime:       "time-train-concrete",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-known",
				TimeRegimeFamily: "time-known",
			},
			Targets: TargetView{
				DurationResidualMillis:   &duration,
				CompletionBeforeDeadline: &completion,
			},
		},
		{
			ObservationID: "test-a-censored",
			Split:         "test",
			Grouping: Grouping{
				ScenarioFamily:   "test-a",
				WorkerPopulation: "worker-test-a",
				TimeRegime:       "time-test-a",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-known",
				TimeRegimeFamily: "time-unseen",
				ShiftAxes: []string{"time_regime"},
			},
		},
		{
			ObservationID: "test-b-ineligible",
			Split:         "test",
			Grouping: Grouping{
				ScenarioFamily:   "test-b",
				WorkerPopulation: "worker-test-b",
				TimeRegime:       "time-test-b",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-unseen",
				TimeRegimeFamily: "time-known",
				ShiftAxes: []string{"worker_population"},
			},
		},
		{
			ObservationID: "test-c-known",
			Split:         "test",
			Grouping: Grouping{
				ScenarioFamily:   "s6-compound-test",
				WorkerPopulation: "worker-test-c",
				TimeRegime:       "time-test-c",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-unseen",
				TimeRegimeFamily: "time-unseen",
				ShiftAxes: []string{"worker_population", "time_regime", "compound"},
			},
			Targets: TargetView{
				DurationResidualMillis:   &duration,
				CompletionBeforeDeadline: &completion,
			},
			PolicyEligible: true,
		},
	}

	readiness := AssessModelFitReadiness(rows)
	if readiness.Slices.TestAUnseenTimeOnly != 1 || readiness.Slices.TestBUnseenWorkerOnly != 1 {
		t.Fatalf("missing structural held-out slices: %+v", readiness.Slices)
	}
	if readiness.Slices.TestADurationTargets != 0 || readiness.Slices.TestACompletionTargets != 0 {
		t.Fatalf("Test-A unexpectedly acquired targets: %+v", readiness.Slices)
	}
	if readiness.Slices.TestBDurationTargets != 0 || readiness.Slices.TestBCompletionTargets != 0 {
		t.Fatalf("Test-B unexpectedly acquired targets: %+v", readiness.Slices)
	}
	if readiness.ModelFitReady {
		t.Fatal("targetless held-out slices must not unlock model fitting")
	}
	if len(readiness.BlockingReasons) != 4 {
		t.Fatalf("target-coverage blockers=%v", readiness.BlockingReasons)
	}
}

func TestReadinessIgnoresTargetsOnPolicyIneligibleRows(t *testing.T) {
	duration := 10.0
	completion := true
	rows := []ComparisonRow{
		{
			ObservationID: "train",
			Split:         "train",
			Grouping: Grouping{
				ScenarioFamily:   "train-family",
				WorkerPopulation: "worker-train-concrete",
				TimeRegime:       "time-train-concrete",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-known",
				TimeRegimeFamily: "time-known",
			},
			PolicyEligible: true,
		},
		{
			ObservationID: "test-a-ineligible",
			Split:         "test",
			Grouping: Grouping{
				ScenarioFamily:   "test-a",
				WorkerPopulation: "worker-test-a",
				TimeRegime:       "time-test-a",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-known",
				TimeRegimeFamily: "time-unseen",
				ShiftAxes: []string{"time_regime"},
			},
			Targets: TargetView{
				DurationResidualMillis:   &duration,
				CompletionBeforeDeadline: &completion,
			},
			PolicyEligible: false,
		},
		{
			ObservationID: "test-b-ineligible",
			Split:         "test",
			Grouping: Grouping{
				ScenarioFamily:   "test-b",
				WorkerPopulation: "worker-test-b",
				TimeRegime:       "time-test-b",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-unseen",
				TimeRegimeFamily: "time-known",
				ShiftAxes: []string{"worker_population"},
			},
			Targets: TargetView{
				DurationResidualMillis:   &duration,
				CompletionBeforeDeadline: &completion,
			},
			PolicyEligible: false,
		},
		{
			ObservationID: "test-c-ineligible",
			Split:         "test",
			Grouping: Grouping{
				ScenarioFamily:   "s6-compound-test",
				WorkerPopulation: "worker-test-c",
				TimeRegime:       "time-test-c",
			},
			EvaluationAxes: residualsim.GeneralizationAxes{
				Schema: residualsim.EvaluationAxesSchema,
				WorkerPopulationFamily: "worker-unseen",
				TimeRegimeFamily: "time-unseen",
				ShiftAxes: []string{"worker_population", "time_regime", "compound"},
			},
			Targets: TargetView{
				DurationResidualMillis:   &duration,
				CompletionBeforeDeadline: &completion,
			},
			PolicyEligible: false,
		},
	}

	readiness := AssessModelFitReadiness(rows)
	if readiness.Slices.TestAUnseenTimeOnly != 1 ||
		readiness.Slices.TestBUnseenWorkerOnly != 1 ||
		readiness.Slices.TestCCompound != 1 {
		t.Fatalf("missing structural held-out coverage: %+v", readiness.Slices)
	}
	if readiness.Slices.TestADurationTargets != 0 ||
		readiness.Slices.TestACompletionTargets != 0 ||
		readiness.Slices.TestBDurationTargets != 0 ||
		readiness.Slices.TestBCompletionTargets != 0 ||
		readiness.Slices.TestCDurationTargets != 0 ||
		readiness.Slices.TestCCompletionTargets != 0 {
		t.Fatalf("policy-ineligible rows counted as optimization targets: %+v", readiness.Slices)
	}
	if readiness.ModelFitReady {
		t.Fatal("policy-ineligible target pointers must not unlock model fitting")
	}
}

func TestPolicyEligibilityDoesNotImplyTargetAvailability(t *testing.T) {
	bundle, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	var sawCensored bool
	for _, row := range bundle.Dataset.Rows {
		if !strings.Contains(row.Grouping.ScenarioFamily, "s4-churn-censored") {
			continue
		}
		sawCensored = true
		if !row.PolicyEligible {
			t.Fatal("censored fixture should remain policy eligible")
		}
		if row.Targets.DurationResidualMillis != nil ||
			row.Targets.CompletionBeforeDeadline != nil {
			t.Fatal("policy eligibility must not imply optimization-target availability")
		}
	}
	if !sawCensored {
		t.Fatal("missing censored fixture")
	}
}

func TestProjectionRetainsTargetAndGroupingOutsideFeatureVector(t *testing.T) {
	bundle, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	var completed *ComparisonRow
	for index := range bundle.Dataset.Rows {
		row := &bundle.Dataset.Rows[index]
		if row.Targets.DurationResidualMillis != nil {
			completed = row
			break
		}
	}
	if completed == nil {
		t.Fatal("missing completed target row")
	}
	if completed.Grouping.ScenarioFamily == "" ||
		completed.Grouping.WorkerPopulation == "" ||
		completed.Grouping.TimeRegime == "" {
		t.Fatal("evaluation grouping provenance was lost")
	}
	if completed.Targets.CompletionBeforeDeadline == nil {
		t.Fatal("completed row with deadline should have completion target")
	}
	if completed.Features.LocalInputFraction == nil {
		t.Fatal("expected local-input fraction for non-empty input")
	}
	if *completed.Features.LocalInputFraction < 0 ||
		*completed.Features.LocalInputFraction > 1 ||
		math.IsNaN(*completed.Features.LocalInputFraction) {
		t.Fatal("invalid local-input fraction")
	}
}

func TestEvaluationAxesRemainOutsideModelFeatures(t *testing.T) {
	bundle, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	var sawA, sawB bool
	for _, row := range bundle.Dataset.Rows {
		raw, err := json.Marshal(row.Features)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "family") || strings.Contains(string(raw), "shift_axes") {
			t.Fatalf("evaluation axes leaked into model features: %s", raw)
		}
		if row.Split == "test" {
			if row.EvaluationAxes.TimeRegimeFamily == "time-family-unseen" &&
				row.EvaluationAxes.WorkerPopulationFamily == "worker-family-known" {
				sawA = true
			}
			if row.EvaluationAxes.WorkerPopulationFamily == "worker-family-unseen" &&
				row.EvaluationAxes.TimeRegimeFamily == "time-family-known" {
				sawB = true
			}
		}
	}
	if !sawA || !sawB {
		t.Fatalf("projected isolated axes A=%v B=%v", sawA, sawB)
	}
}

func TestSignedDifferenceFailsClosedOnInt64Overflow(t *testing.T) {
	if _, err := signedDifference(math.MaxUint64, 0); err == nil {
		t.Fatal("expected positive int64 overflow rejection")
	}
	if _, err := signedDifference(0, math.MaxUint64); err == nil {
		t.Fatal("expected negative int64 overflow rejection")
	}
	if value, err := signedDifference(100, 125); err != nil || value != -25 {
		t.Fatalf("negative difference value=%d err=%v", value, err)
	}
	if value, err := signedDifference(125, 100); err != nil || value != 25 {
		t.Fatalf("positive difference value=%d err=%v", value, err)
	}
}
