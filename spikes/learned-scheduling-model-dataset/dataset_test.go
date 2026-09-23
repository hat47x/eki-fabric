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
			if row.OptimizationEligible {
				t.Fatal("policy-rejected row marked optimization eligible")
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

func TestCurrentScenarioSetFailsClosedOnMissingIsolatedABViews(t *testing.T) {
	bundle, err := Freeze(42)
	if err != nil {
		t.Fatal(err)
	}
	readiness := bundle.Readiness
	if readiness.Schema != ReadinessSchema {
		t.Fatalf("readiness schema=%q", readiness.Schema)
	}
	if readiness.Slices.TestAUnseenTimeOnly != 0 {
		t.Fatalf("unexpected isolated Test-A rows: %d", readiness.Slices.TestAUnseenTimeOnly)
	}
	if readiness.Slices.TestBUnseenWorkerOnly != 0 {
		t.Fatalf("unexpected isolated Test-B rows: %d", readiness.Slices.TestBUnseenWorkerOnly)
	}
	if readiness.Slices.TestCCompound != 6 {
		t.Fatalf("compound Test-C rows=%d want=6", readiness.Slices.TestCCompound)
	}
	if readiness.ModelFitReady {
		t.Fatal("model fitting must remain blocked until isolated Test-A/B exist")
	}
	if len(readiness.BlockingReasons) != 2 {
		t.Fatalf("blocking reasons=%v", readiness.BlockingReasons)
	}
	if !strings.Contains(readiness.BlockingReasons[0], "Test-A") ||
		!strings.Contains(readiness.BlockingReasons[1], "Test-B") {
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
				WorkerPopulation: "worker-known",
				TimeRegime:       "time-known",
			},
		},
		{
			ObservationID: "valid-time-only",
			Split:         "valid",
			Grouping: Grouping{
				ScenarioFamily:   "valid-time-shift",
				WorkerPopulation: "worker-known",
				TimeRegime:       "time-unseen",
			},
		},
		{
			ObservationID: "valid-worker-only",
			Split:         "valid",
			Grouping: Grouping{
				ScenarioFamily:   "valid-worker-shift",
				WorkerPopulation: "worker-unseen",
				TimeRegime:       "time-known",
			},
		},
		{
			ObservationID: "test-compound",
			Split:         "test",
			Grouping: Grouping{
				ScenarioFamily:   "s6-compound-test",
				WorkerPopulation: "worker-unseen",
				TimeRegime:       "time-unseen",
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
