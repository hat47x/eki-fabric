package modeldataset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	residualsim "eki.local/learned-scheduling-residual-simulation"
	shadowsched "eki.local/learned-scheduling-shadow-observation"
)

const (
	FrozenDatasetSchema  = "eki.learned-scheduling-model-comparison-dataset/v1"
	FreezeBundleSchema   = "eki.learned-scheduling-model-comparison-freeze/v1"
	BaselineBundleSchema = "eki.learned-scheduling-model-comparison-baselines/v1"
	ReadinessSchema      = "eki.learned-scheduling-model-fit-readiness/v1"
	ProjectionVersion    = "eki-ml-0c-feature-projection-v1"
	FreezeVersion        = "eki-ml-0c-dataset-freeze-v1"
)

type ModelFeatures struct {
	TaskClass           string   `json:"task_class"`
	WorkerClass         string   `json:"worker_class"`
	TotalInputBytes     uint64   `json:"total_input_bytes"`
	LocalInputBytes     uint64   `json:"local_input_bytes"`
	TransferInputBytes  uint64   `json:"transfer_input_bytes"`
	TransferOutputBytes uint64   `json:"transfer_output_bytes"`
	TransferBytes       uint64   `json:"transfer_bytes"`
	TransferMillis      uint64   `json:"transfer_millis"`
	ComputeMillis       uint64   `json:"compute_millis"`
	EstimatedMillis     uint64   `json:"estimated_millis"`
	LocalInputFraction  *float64 `json:"local_input_fraction"`
	DeadlineSlackMillis *int64   `json:"deadline_slack_millis"`
}

type TargetView struct {
	DurationResidualMillis   *float64 `json:"duration_residual_millis"`
	CompletionBeforeDeadline *bool    `json:"completion_before_deadline"`
}

type Grouping struct {
	ScenarioFamily   string `json:"scenario_family"`
	WorkerPopulation string `json:"worker_population"`
	TimeRegime       string `json:"time_regime"`
}

type ComparisonRow struct {
	ObservationID  string                         `json:"observation_id"`
	Split          string                         `json:"split"`
	Grouping       Grouping                       `json:"grouping"`
	EvaluationAxes residualsim.GeneralizationAxes `json:"evaluation_axes"`
	Features       ModelFeatures                  `json:"features"`
	Targets        TargetView                     `json:"targets"`
	PolicyEligible bool                           `json:"policy_eligible"`
}

type FrozenDataset struct {
	Schema              string          `json:"schema"`
	FreezeVersion       string          `json:"freeze_version"`
	ProjectionVersion   string          `json:"projection_version"`
	SourceDatasetSchema string          `json:"source_dataset_schema"`
	ScenarioSetVersion  string          `json:"scenario_set_version"`
	GeneratorVersion    string          `json:"generator_version"`
	SourceFeatureSchema string          `json:"source_feature_schema"`
	Seed                uint64          `json:"seed"`
	Rows                []ComparisonRow `json:"rows"`
}

type BaselineBundle struct {
	Schema string                       `json:"schema"`
	Valid  residualsim.EvaluationResult `json:"valid"`
	Test   residualsim.EvaluationResult `json:"test"`
}

type SliceReadiness struct {
	TestAUnseenTimeOnly    int `json:"test_a_unseen_time_only"`
	TestADurationTargets   int `json:"test_a_duration_targets"`
	TestACompletionTargets int `json:"test_a_completion_targets"`
	TestBUnseenWorkerOnly  int `json:"test_b_unseen_worker_only"`
	TestBDurationTargets   int `json:"test_b_duration_targets"`
	TestBCompletionTargets int `json:"test_b_completion_targets"`
	TestCCompound          int `json:"test_c_compound"`
	TestCDurationTargets   int `json:"test_c_duration_targets"`
	TestCCompletionTargets int `json:"test_c_completion_targets"`
}

type ModelFitReadiness struct {
	Schema          string         `json:"schema"`
	Slices          SliceReadiness `json:"slices"`
	ModelFitReady   bool           `json:"model_fit_ready"`
	BlockingReasons []string       `json:"blocking_reasons"`
}

type FreezeBundle struct {
	Schema                  string            `json:"schema"`
	FreezeVersion           string            `json:"freeze_version"`
	SourceDatasetDigest     string            `json:"source_dataset_digest"`
	ProjectedDatasetDigest  string            `json:"projected_dataset_digest"`
	BaselineBundleDigest    string            `json:"baseline_bundle_digest"`
	Dataset                 FrozenDataset     `json:"dataset"`
	Baselines               BaselineBundle    `json:"baselines"`
	Readiness               ModelFitReadiness `json:"readiness"`
	TrainingPerformed       bool              `json:"training_performed"`
	PlacementInfluenced     bool              `json:"placement_influenced"`
	AuthorizationInfluenced bool              `json:"authorization_influenced"`
	SingleCompositeScore    bool              `json:"single_composite_score"`
	WinnerSelected          bool              `json:"winner_selected"`
}

func Freeze(seed uint64) (FreezeBundle, error) {
	source, err := residualsim.GenerateDataset(seed)
	if err != nil {
		return FreezeBundle{}, err
	}
	projected, readiness, err := ProjectDataset(source)
	if err != nil {
		return FreezeBundle{}, err
	}
	validBaseline, err := residualsim.EvaluateBaselines(source, "valid")
	if err != nil {
		return FreezeBundle{}, fmt.Errorf("valid baseline: %w", err)
	}
	testBaseline, err := residualsim.EvaluateBaselines(source, "test")
	if err != nil {
		return FreezeBundle{}, fmt.Errorf("test baseline: %w", err)
	}
	baselines := BaselineBundle{
		Schema: BaselineBundleSchema,
		Valid:  validBaseline,
		Test:   testBaseline,
	}
	if err := validateBaselineResult(validBaseline); err != nil {
		return FreezeBundle{}, fmt.Errorf("valid baseline boundary: %w", err)
	}
	if err := validateBaselineResult(testBaseline); err != nil {
		return FreezeBundle{}, fmt.Errorf("test baseline boundary: %w", err)
	}

	sourceDigest, err := DigestJSON(source)
	if err != nil {
		return FreezeBundle{}, err
	}
	projectedDigest, err := DigestJSON(projected)
	if err != nil {
		return FreezeBundle{}, err
	}
	baselineDigest, err := DigestJSON(baselines)
	if err != nil {
		return FreezeBundle{}, err
	}

	return FreezeBundle{
		Schema:                  FreezeBundleSchema,
		FreezeVersion:           FreezeVersion,
		SourceDatasetDigest:     sourceDigest,
		ProjectedDatasetDigest:  projectedDigest,
		BaselineBundleDigest:    baselineDigest,
		Dataset:                 projected,
		Baselines:               baselines,
		Readiness:               readiness,
		TrainingPerformed:       false,
		PlacementInfluenced:     false,
		AuthorizationInfluenced: false,
		SingleCompositeScore:    false,
		WinnerSelected:          false,
	}, nil
}

func ProjectDataset(source residualsim.Dataset) (FrozenDataset, ModelFitReadiness, error) {
	if source.Schema != residualsim.DatasetSchema {
		return FrozenDataset{}, ModelFitReadiness{}, errors.New("unsupported source dataset schema")
	}
	if source.ScenarioSetVersion != residualsim.ScenarioSetVersion {
		return FrozenDataset{}, ModelFitReadiness{}, errors.New("unsupported scenario-set version")
	}
	if source.GeneratorVersion != residualsim.GeneratorVersion {
		return FrozenDataset{}, ModelFitReadiness{}, errors.New("unsupported generator version")
	}
	if err := shadowsched.ValidateNoSplitLeakage(source.Observations); err != nil {
		return FrozenDataset{}, ModelFitReadiness{}, fmt.Errorf("source split contract: %w", err)
	}
	if err := residualsim.ValidateEvaluationAxes(source); err != nil {
		return FrozenDataset{}, ModelFitReadiness{}, fmt.Errorf("source evaluation axes: %w", err)
	}

	seen := map[string]bool{}
	rows := make([]ComparisonRow, 0, len(source.Observations))
	for _, observation := range source.Observations {
		if seen[observation.ObservationID] {
			return FrozenDataset{}, ModelFitReadiness{}, fmt.Errorf("duplicate observation_id %q", observation.ObservationID)
		}
		seen[observation.ObservationID] = true
		axes := source.EvaluationAxes[observation.ObservationID]
		row, err := projectObservation(observation, axes)
		if err != nil {
			return FrozenDataset{}, ModelFitReadiness{}, fmt.Errorf("project %s: %w", observation.ObservationID, err)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return FrozenDataset{}, ModelFitReadiness{}, errors.New("source dataset is empty")
	}

	projected := FrozenDataset{
		Schema:              FrozenDatasetSchema,
		FreezeVersion:       FreezeVersion,
		ProjectionVersion:   ProjectionVersion,
		SourceDatasetSchema: source.Schema,
		ScenarioSetVersion:  source.ScenarioSetVersion,
		GeneratorVersion:    source.GeneratorVersion,
		SourceFeatureSchema: residualsim.FeatureSchema,
		Seed:                source.Seed,
		Rows:                rows,
	}
	return projected, AssessModelFitReadiness(rows), nil
}

func projectObservation(
	observation shadowsched.Observation,
	axes residualsim.GeneralizationAxes,
) (ComparisonRow, error) {
	if err := shadowsched.Validate(observation); err != nil {
		return ComparisonRow{}, err
	}
	if observation.FeatureSchemaVersion != residualsim.FeatureSchema {
		return ComparisonRow{}, fmt.Errorf(
			"feature schema %q must equal %q",
			observation.FeatureSchemaVersion,
			residualsim.FeatureSchema,
		)
	}

	var localFraction *float64
	if observation.Baseline.TotalInputBytes > 0 {
		value := float64(observation.Baseline.LocalInputBytes) /
			float64(observation.Baseline.TotalInputBytes)
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return ComparisonRow{}, errors.New("invalid local-input fraction")
		}
		localFraction = float64Ptr(value)
	}

	var deadlineSlack *int64
	if observation.Outcome.DeadlineMillis != nil && observation.Baseline.Eligible {
		value, err := signedDifference(
			*observation.Outcome.DeadlineMillis,
			observation.Baseline.EstimatedMillis,
		)
		if err != nil {
			return ComparisonRow{}, fmt.Errorf("deadline slack: %w", err)
		}
		deadlineSlack = int64Ptr(value)
	}

	targets := TargetView{}
	if residual, known := shadowsched.DurationResidualMillis(observation); known {
		if math.IsNaN(residual) || math.IsInf(residual, 0) {
			return ComparisonRow{}, errors.New("duration residual is not finite")
		}
		targets.DurationResidualMillis = float64Ptr(residual)
	}
	if completion, known := shadowsched.CompletionBeforeDeadline(observation); known {
		targets.CompletionBeforeDeadline = boolPtr(completion)
	}

	return ComparisonRow{
		ObservationID: observation.ObservationID,
		Split:         observation.Split.Name,
		Grouping: Grouping{
			ScenarioFamily:   observation.ScenarioFamily,
			WorkerPopulation: observation.WorkerPopulation,
			TimeRegime:       observation.TimeRegime,
		},
		EvaluationAxes: axes,
		Features: ModelFeatures{
			TaskClass:           observation.TaskClass,
			WorkerClass:         observation.WorkerClass,
			TotalInputBytes:     observation.Baseline.TotalInputBytes,
			LocalInputBytes:     observation.Baseline.LocalInputBytes,
			TransferInputBytes:  observation.Baseline.TransferInputBytes,
			TransferOutputBytes: observation.Baseline.TransferOutputBytes,
			TransferBytes:       observation.Baseline.TransferBytes,
			TransferMillis:      observation.Baseline.TransferMillis,
			ComputeMillis:       observation.Baseline.ComputeMillis,
			EstimatedMillis:     observation.Baseline.EstimatedMillis,
			LocalInputFraction:  localFraction,
			DeadlineSlackMillis: deadlineSlack,
		},
		Targets:        targets,
		PolicyEligible: observation.PolicyEligible,
	}, nil
}

func AssessModelFitReadiness(rows []ComparisonRow) ModelFitReadiness {
	trainWorkerFamilies := map[string]bool{}
	trainTimeFamilies := map[string]bool{}
	for _, row := range rows {
		if row.Split != "train" {
			continue
		}
		trainWorkerFamilies[row.EvaluationAxes.WorkerPopulationFamily] = true
		trainTimeFamilies[row.EvaluationAxes.TimeRegimeFamily] = true
	}

	slices := SliceReadiness{}
	for _, row := range rows {
		// Issue #50 preregisters Test-A/B/C as read-only final test
		// views. Validation-only regime coverage must never unlock model
		// fitting by masquerading as one of those held-out slices.
		if row.Split != "test" {
			continue
		}
		unseenWorker := !trainWorkerFamilies[row.EvaluationAxes.WorkerPopulationFamily]
		unseenTime := !trainTimeFamilies[row.EvaluationAxes.TimeRegimeFamily]
		targetEligible := row.PolicyEligible
		if unseenTime && !unseenWorker {
			slices.TestAUnseenTimeOnly++
			if targetEligible && row.Targets.DurationResidualMillis != nil {
				slices.TestADurationTargets++
			}
			if targetEligible && row.Targets.CompletionBeforeDeadline != nil {
				slices.TestACompletionTargets++
			}
		}
		if unseenWorker && !unseenTime {
			slices.TestBUnseenWorkerOnly++
			if targetEligible && row.Targets.DurationResidualMillis != nil {
				slices.TestBDurationTargets++
			}
			if targetEligible && row.Targets.CompletionBeforeDeadline != nil {
				slices.TestBCompletionTargets++
			}
		}
		if strings.Contains(row.Grouping.ScenarioFamily, "s6-compound") {
			slices.TestCCompound++
			if targetEligible && row.Targets.DurationResidualMillis != nil {
				slices.TestCDurationTargets++
			}
			if targetEligible && row.Targets.CompletionBeforeDeadline != nil {
				slices.TestCCompletionTargets++
			}
		}
	}

	blockers := []string{}
	if slices.TestAUnseenTimeOnly == 0 {
		blockers = append(blockers, "missing isolated Test-A unseen-time-only rows")
	} else {
		if slices.TestADurationTargets == 0 {
			blockers = append(blockers, "Test-A has no known duration target")
		}
		if slices.TestACompletionTargets == 0 {
			blockers = append(blockers, "Test-A has no known completion target")
		}
	}
	if slices.TestBUnseenWorkerOnly == 0 {
		blockers = append(blockers, "missing isolated Test-B unseen-worker-only rows")
	} else {
		if slices.TestBDurationTargets == 0 {
			blockers = append(blockers, "Test-B has no known duration target")
		}
		if slices.TestBCompletionTargets == 0 {
			blockers = append(blockers, "Test-B has no known completion target")
		}
	}
	if slices.TestCCompound == 0 {
		blockers = append(blockers, "missing Test-C compound-shift rows")
	} else {
		if slices.TestCDurationTargets == 0 {
			blockers = append(blockers, "Test-C has no known duration target")
		}
		if slices.TestCCompletionTargets == 0 {
			blockers = append(blockers, "Test-C has no known completion target")
		}
	}
	return ModelFitReadiness{
		Schema:          ReadinessSchema,
		Slices:          slices,
		ModelFitReady:   len(blockers) == 0,
		BlockingReasons: blockers,
	}
}

func DigestJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateBaselineResult(result residualsim.EvaluationResult) error {
	if result.Schema != residualsim.ResultSchema {
		return errors.New("unexpected baseline result schema")
	}
	if !result.ShadowOnly {
		return errors.New("baseline result must remain shadow-only")
	}
	if result.PlacementInfluenced || result.AuthorizationInfluenced {
		return errors.New("baseline result must not influence placement or authorization")
	}
	if result.SingleCompositeScore || result.WinnerSelected {
		return errors.New("baseline result must not select a composite winner")
	}
	return nil
}

func signedDifference(left, right uint64) (int64, error) {
	if left >= right {
		delta := left - right
		if delta > uint64(math.MaxInt64) {
			return 0, errors.New("positive difference exceeds int64")
		}
		return int64(delta), nil
	}
	delta := right - left
	if delta > uint64(math.MaxInt64) {
		return 0, errors.New("negative difference exceeds int64")
	}
	return -int64(delta), nil
}

func float64Ptr(value float64) *float64 { return &value }
func int64Ptr(value int64) *int64       { return &value }
func boolPtr(value bool) *bool          { return &value }
