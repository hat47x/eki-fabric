package residualsim

import (
	"errors"
	"fmt"
	"math"
	"sort"

	locality "eki.local/artifact-locality-spike"
	shadowsched "eki.local/learned-scheduling-shadow-observation"
)

const (
	DatasetSchema       = "eki.learned-scheduling-residual-dataset/v0"
	ScenarioSetVersion  = "eki-ml-0b-scenarios-v0"
	GeneratorVersion    = "eki-ml-0b-generator-v0"
	EvaluatorVersion    = "eki-ml-0b-baseline-evaluator-v0"
	FeatureSchema       = "eki.learned-scheduling-features/v0"
	ResultSchema        = "eki.learned-scheduling-baseline-evaluation/v0"
)

type Dataset struct {
	Schema             string                    `json:"schema"`
	ScenarioSetVersion string                    `json:"scenario_set_version"`
	GeneratorVersion   string                    `json:"generator_version"`
	Seed               uint64                    `json:"seed"`
	Observations       []shadowsched.Observation `json:"observations"`
}

type ScenarioSpec struct {
	ID                       string
	Split                    string
	ScenarioFamily           string
	WorkerPopulation         string
	TimeRegime               string
	TaskClass                string
	WorkerClass              string
	InputBytes               uint64
	SecondInputBytes         uint64
	ExpectedOutputBytes      uint64
	BaselineComputeMillis    uint64
	DownloadBytesPerSec      uint64
	UploadBytesPerSec        uint64
	CacheHit                 bool
	SecondCacheHit           bool
	ActualComputePermille    uint64
	ActualDownloadPermille   uint64
	ActualUploadPermille     uint64
	ExtraTransferBytes       uint64
	DeadlineMillis           uint64
	OutcomeStatus            string
	ObservedFractionPermille uint64
	PolicyEligible           bool
}

type DurationMetrics struct {
	KnownCompleted           int                `json:"known_completed"`
	DeterministicMAE         float64            `json:"deterministic_mae_millis"`
	GlobalMeanCorrectedMAE   float64            `json:"global_mean_corrected_mae_millis"`
	GlobalMedianCorrectedMAE float64            `json:"global_median_corrected_mae_millis"`
	GroupedMeanCorrectedMAE  float64            `json:"grouped_mean_corrected_mae_millis"`
	MedianAbsoluteError      float64            `json:"deterministic_median_absolute_error_millis"`
	P90AbsoluteError         float64            `json:"deterministic_p90_absolute_error_millis"`
	SignedBiasByScenario     map[string]float64 `json:"signed_bias_by_scenario_family"`
	SignedBiasByWorkerPop    map[string]float64 `json:"signed_bias_by_worker_population"`
}

type CompletionMetrics struct {
	KnownTargets              int     `json:"known_targets"`
	TotalEligible             int     `json:"total_eligible"`
	CensoredEligible          int     `json:"censored_eligible"`
	KnownTargetCoverage       float64 `json:"known_target_coverage"`
	CensoredFraction          float64 `json:"censored_fraction"`
	UnconditionalProbability float64 `json:"unconditional_probability"`
	UnconditionalBrier        float64 `json:"unconditional_brier"`
	GroupedBrier              float64 `json:"grouped_brier"`
}

type EvaluationResult struct {
	Schema                  string            `json:"schema"`
	ScenarioSetVersion      string            `json:"scenario_set_version"`
	GeneratorVersion        string            `json:"generator_version"`
	EvaluatorVersion        string            `json:"evaluator_version"`
	DatasetSeed             uint64            `json:"dataset_seed"`
	FeatureSchemaVersion    string            `json:"feature_schema_version"`
	TrainCount              int               `json:"train_count"`
	EvaluationCount         int               `json:"evaluation_count"`
	Duration                DurationMetrics   `json:"duration"`
	Completion              CompletionMetrics `json:"completion"`
	ShadowOnly              bool              `json:"shadow_only"`
	PlacementInfluenced     bool              `json:"placement_influenced"`
	AuthorizationInfluenced bool              `json:"authorization_influenced"`
	SingleCompositeScore    bool              `json:"single_composite_score"`
	WinnerSelected          bool              `json:"winner_selected"`
}

func GenerateDataset(seed uint64) (Dataset, error) {
	specs := defaultScenarioSpecs(seed)
	observations := make([]shadowsched.Observation, 0, len(specs))
	for _, spec := range specs {
		o, err := generateObservation(seed, spec)
		if err != nil {
			return Dataset{}, fmt.Errorf("generate %s: %w", spec.ID, err)
		}
		observations = append(observations, o)
	}
	if err := shadowsched.ValidateNoSplitLeakage(observations); err != nil {
		return Dataset{}, fmt.Errorf("generated dataset violates split contract: %w", err)
	}
	return Dataset{
		Schema:             DatasetSchema,
		ScenarioSetVersion: ScenarioSetVersion,
		GeneratorVersion:   GeneratorVersion,
		Seed:               seed,
		Observations:       observations,
	}, nil
}

func EvaluateBaselines(dataset Dataset, evalSplits ...string) (EvaluationResult, error) {
	if dataset.Schema != DatasetSchema || dataset.ScenarioSetVersion != ScenarioSetVersion || dataset.GeneratorVersion != GeneratorVersion {
		return EvaluationResult{}, errors.New("unsupported dataset identity")
	}
	if err := shadowsched.ValidateNoSplitLeakage(dataset.Observations); err != nil {
		return EvaluationResult{}, err
	}
	selected := map[string]bool{}
	for _, split := range evalSplits {
		if split != "valid" && split != "test" {
			return EvaluationResult{}, fmt.Errorf("evaluation split %q must be valid or test", split)
		}
		selected[split] = true
	}
	if len(selected) == 0 {
		selected["valid"] = true
		selected["test"] = true
	}
	var train, eval []shadowsched.Observation
	for _, o := range dataset.Observations {
		switch {
		case o.Split.Name == "train":
			train = append(train, o)
		case selected[o.Split.Name]:
			eval = append(eval, o)
		}
	}
	if len(train) == 0 || len(eval) == 0 {
		return EvaluationResult{}, errors.New("training and evaluation observations are required")
	}

	duration, err := evaluateDuration(train, eval)
	if err != nil {
		return EvaluationResult{}, err
	}
	completion, err := evaluateCompletion(train, eval)
	if err != nil {
		return EvaluationResult{}, err
	}
	return EvaluationResult{
		Schema:                  ResultSchema,
		ScenarioSetVersion:      dataset.ScenarioSetVersion,
		GeneratorVersion:        dataset.GeneratorVersion,
		EvaluatorVersion:        EvaluatorVersion,
		DatasetSeed:             dataset.Seed,
		FeatureSchemaVersion:    FeatureSchema,
		TrainCount:              len(train),
		EvaluationCount:         len(eval),
		Duration:                duration,
		Completion:              completion,
		ShadowOnly:              true,
		PlacementInfluenced:     false,
		AuthorizationInfluenced: false,
		SingleCompositeScore:    false,
		WinnerSelected:          false,
	}, nil
}

func defaultScenarioSpecs(seed uint64) []ScenarioSpec {
	_ = seed // seed changes bounded jitter; scenario identities stay stable.
	var specs []ScenarioSpec
	add := func(split, kind string, idx int, configure func(*ScenarioSpec)) {
		s := ScenarioSpec{
			ID:                       fmt.Sprintf("%s-%s-%02d", split, kind, idx),
			Split:                    split,
			ScenarioFamily:           fmt.Sprintf("%s-%s", split, kind),
			WorkerPopulation:         fmt.Sprintf("%s-worker-pop-%d", split, idx%2),
			TimeRegime:               fmt.Sprintf("%s-time-regime-%d", split, idx%2),
			TaskClass:                []string{"map", "reduce"}[idx%2],
			WorkerClass:              []string{"cpu-small", "cpu-large"}[idx%2],
			InputBytes:               64 << 20,
			ExpectedOutputBytes:      8 << 20,
			BaselineComputeMillis:    2000 + uint64(idx%3)*500,
			DownloadBytesPerSec:      64 << 20,
			UploadBytesPerSec:        32 << 20,
			CacheHit:                 idx%2 == 0,
			ActualComputePermille:    1000,
			ActualDownloadPermille:   1000,
			ActualUploadPermille:     1000,
			DeadlineMillis:           5000,
			OutcomeStatus:            "completed",
			ObservedFractionPermille: 1000,
			PolicyEligible:           true,
		}
		configure(&s)
		specs = append(specs, s)
	}

	add("train", "s1-compute-accurate", 0, func(*ScenarioSpec) {})
	add("train", "s1-compute-mild-slow", 1, func(s *ScenarioSpec) { s.ActualComputePermille = 1100 })
	add("train", "s1-compute-mild-slow", 2, func(s *ScenarioSpec) { s.ActualComputePermille = 1300 })
	add("train", "s1-worker-class-bias", 18, func(s *ScenarioSpec) { s.ActualComputePermille = 1150 })
	add("train", "s2-bandwidth-nominal", 19, func(*ScenarioSpec) {})
	add("train", "s2-bandwidth-mild", 3, func(s *ScenarioSpec) { s.ActualDownloadPermille = 850 })
	add("train", "s3-cache-full", 4, configureTwoInputCache(true, true, 0))
	add("train", "s3-cache-partial", 8, configureTwoInputCache(true, false, 0))
	add("train", "s3-cache-cold", 9, configureTwoInputCache(false, false, 0))
	add("train", "s3-cache-stale", 14, configureTwoInputCache(true, true, 16<<20))
	add("train", "s4-churn-preempt", 5, func(s *ScenarioSpec) {
		s.OutcomeStatus = "preempted"
		s.ObservedFractionPermille = 550
	})
	add("train", "s4-churn-censored", 6, func(s *ScenarioSpec) {
		s.OutcomeStatus = "censored"
		s.ObservedFractionPermille = 450
	})
	add("train", "s5-deadline-loose", 15, func(s *ScenarioSpec) { s.DeadlineMillis = 10000 })
	add("train", "s5-deadline-near", 7, func(s *ScenarioSpec) { s.DeadlineMillis = 3200 })

	add("valid", "s1-compute-fast", 10, func(s *ScenarioSpec) { s.ActualComputePermille = 900 })
	add("valid", "s1-compute-very-slow", 17, func(s *ScenarioSpec) { s.ActualComputePermille = 2000 })
	add("valid", "s2-bandwidth-burst", 16, func(s *ScenarioSpec) { s.ActualDownloadPermille = 400 })
	add("valid", "s2-bandwidth-asymmetric", 11, func(s *ScenarioSpec) {
		s.ActualDownloadPermille = 700
		s.ActualUploadPermille = 900
	})
	add("valid", "s3-cache-stale", 12, configureTwoInputCache(true, true, 24<<20))
	add("valid", "s5-deadline-impossible", 20, func(s *ScenarioSpec) { s.DeadlineMillis = 1000 })
	add("valid", "s5-deadline-censored", 13, func(s *ScenarioSpec) {
		s.DeadlineMillis = 3000
		s.OutcomeStatus = "censored"
		s.ObservedFractionPermille = 500
	})

	for i := 0; i < 6; i++ {
		add("test", "s6-compound", 20+i, func(s *ScenarioSpec) {
			s.ActualComputePermille = []uint64{1400, 2000, 800, 1600, 1200, 1800}[i]
			s.ActualDownloadPermille = []uint64{700, 500, 1000, 750, 600, 450}[i]
			s.ActualUploadPermille = []uint64{1000, 800, 1000, 650, 900, 500}[i]
			if i%2 == 1 {
				s.ExtraTransferBytes = 32 << 20
			}
			s.DeadlineMillis = []uint64{4500, 3500, 6000, 4000, 5000, 3000}[i]
			if i == 3 {
				s.OutcomeStatus = "preempted"
				s.ObservedFractionPermille = 600
			}
			if i == 5 {
				s.OutcomeStatus = "censored"
				s.ObservedFractionPermille = 400
			}
		})
	}
	add("test", "policy-reject", 90, func(s *ScenarioSpec) {
		s.PolicyEligible = false
		s.OutcomeStatus = "failed"
		s.ObservedFractionPermille = 0
	})
	return specs
}

func configureTwoInputCache(firstCached, secondCached bool, extraActualTransfer uint64) func(*ScenarioSpec) {
	return func(s *ScenarioSpec) {
		s.InputBytes = 32 << 20
		s.SecondInputBytes = 32 << 20
		s.CacheHit = firstCached
		s.SecondCacheHit = secondCached
		s.ExtraTransferBytes = extraActualTransfer
	}
}

func generateObservation(seed uint64, spec ScenarioSpec) (shadowsched.Observation, error) {
	inputs := []locality.Artifact{{Digest: "sha256:input-a", SizeBytes: spec.InputBytes}}
	if spec.SecondInputBytes > 0 {
		inputs = append(inputs, locality.Artifact{Digest: "sha256:input-b", SizeBytes: spec.SecondInputBytes})
	}
	task := locality.Task{
		Inputs:              inputs,
		ExpectedOutputBytes: spec.ExpectedOutputBytes,
		MaxTransferBytes:    spec.InputBytes + spec.SecondInputBytes + spec.ExpectedOutputBytes + (1 << 20),
	}
	worker := locality.Worker{
		ID:                     spec.ID,
		HardConstraintsOK:      spec.PolicyEligible,
		RejectReason:           "policy-ineligible fixture",
		ComputeMillis:          spec.BaselineComputeMillis,
		DownloadBytesPerSecond: spec.DownloadBytesPerSec,
		UploadBytesPerSecond:   spec.UploadBytesPerSec,
		CachedDigests: map[string]bool{
			"sha256:input-a": spec.CacheHit,
			"sha256:input-b": spec.SecondCacheHit,
		},
	}
	estimate := locality.EstimateTask(task, worker)
	baseline := shadowsched.BaselineEstimate{
		Eligible:            estimate.Eligible,
		RejectReason:        estimate.RejectReason,
		TotalInputBytes:     estimate.TotalInputBytes,
		LocalInputBytes:     estimate.LocalInputBytes,
		TransferInputBytes:  estimate.TransferInputBytes,
		TransferOutputBytes: estimate.TransferOutputBytes,
		TransferBytes:       estimate.TransferBytes,
		TransferMillis:      estimate.TransferMillis,
		ComputeMillis:       estimate.ComputeMillis,
		EstimatedMillis:     estimate.EstimatedMillis,
	}
	policyReject := ""
	if !spec.PolicyEligible {
		policyReject = estimate.RejectReason
	}
	deadline := spec.DeadlineMillis
	outcome := shadowsched.Outcome{Status: spec.OutcomeStatus, DeadlineMillis: &deadline}
	if spec.PolicyEligible {
		actual := actualDuration(spec, baseline, seed)
		switch spec.OutcomeStatus {
		case "completed":
			outcome.ActualMillis = &actual
			outcome.ObservedMillis = actual
		case "preempted", "censored":
			outcome.ObservedMillis = actual * spec.ObservedFractionPermille / 1000
		case "failed":
			outcome.ObservedMillis = actual / 3
		default:
			return shadowsched.Observation{}, fmt.Errorf("unsupported status %q", spec.OutcomeStatus)
		}
	}
	o := shadowsched.Observation{
		Schema:               shadowsched.Schema,
		ObservationID:        fmt.Sprintf("%s/%d", spec.ID, seed),
		FeatureSchemaVersion: FeatureSchema,
		ScenarioFamily:       spec.ScenarioFamily,
		WorkerPopulation:     spec.WorkerPopulation,
		TimeRegime:           spec.TimeRegime,
		TaskClass:            spec.TaskClass,
		WorkerClass:          spec.WorkerClass,
		PolicyEligible:       spec.PolicyEligible,
		PolicyRejectReason:   policyReject,
		Baseline:             baseline,
		Outcome:              outcome,
		Split: shadowsched.Split{
			Name:             spec.Split,
			ScenarioFamily:   spec.ScenarioFamily,
			WorkerPopulation: spec.WorkerPopulation,
			TimeRegime:       spec.TimeRegime,
		},
		Boundary: shadowsched.ResearchBoundary{ShadowOnly: true},
	}
	if err := shadowsched.Validate(o); err != nil {
		return shadowsched.Observation{}, err
	}
	return o, nil
}

func actualDuration(spec ScenarioSpec, baseline shadowsched.BaselineEstimate, seed uint64) uint64 {
	compute := scaleMillis(baseline.ComputeMillis, spec.ActualComputePermille)
	transferInput := baseline.TransferInputBytes + spec.ExtraTransferBytes
	download := transferMillis(transferInput, spec.DownloadBytesPerSec, spec.ActualDownloadPermille)
	upload := transferMillis(baseline.TransferOutputBytes, spec.UploadBytesPerSec, spec.ActualUploadPermille)
	jitter := deterministicJitter(seed, spec.ID)
	base := compute + download + upload
	if jitter < 0 {
		delta := uint64(-jitter)
		if delta >= base {
			return 1
		}
		return base - delta
	}
	return base + uint64(jitter)
}

func scaleMillis(value, permille uint64) uint64 {
	return uint64(math.Ceil(float64(value) * float64(permille) / 1000.0))
}

func transferMillis(bytes, nominalBytesPerSec, bandwidthPermille uint64) uint64 {
	if bytes == 0 {
		return 0
	}
	if nominalBytesPerSec == 0 || bandwidthPermille == 0 {
		return math.MaxUint64 / 8
	}
	actualBps := float64(nominalBytesPerSec) * float64(bandwidthPermille) / 1000.0
	return uint64(math.Ceil(float64(bytes) / actualBps * 1000.0))
}

func deterministicJitter(seed uint64, id string) int64 {
	x := seed ^ 0x9e3779b97f4a7c15
	for i := 0; i < len(id); i++ {
		x ^= uint64(id[i]) + 0x9e3779b97f4a7c15 + (x << 6) + (x >> 2)
	}
	return int64(x%101) - 50
}

func evaluateDuration(train, eval []shadowsched.Observation) (DurationMetrics, error) {
	var trainResiduals []float64
	groupResiduals := map[string][]float64{}
	for _, o := range train {
		if r, ok := shadowsched.DurationResidualMillis(o); ok {
			trainResiduals = append(trainResiduals, r)
			groupResiduals[groupKey(o)] = append(groupResiduals[groupKey(o)], r)
		}
	}
	if len(trainResiduals) == 0 {
		return DurationMetrics{}, errors.New("no completed eligible training residuals")
	}
	globalMean := mean(trainResiduals)
	globalMedian := median(trainResiduals)
	groupMeans := map[string]float64{}
	for k, rs := range groupResiduals {
		groupMeans[k] = mean(rs)
	}

	var deterministicErrors, meanErrors, medianErrors, groupedErrors []float64
	scenarioBias := map[string][]float64{}
	workerBias := map[string][]float64{}
	for _, o := range eval {
		r, ok := shadowsched.DurationResidualMillis(o)
		if !ok {
			continue
		}
		deterministicErrors = append(deterministicErrors, math.Abs(r))
		meanErrors = append(meanErrors, math.Abs(r-globalMean))
		medianErrors = append(medianErrors, math.Abs(r-globalMedian))
		correction, ok := groupMeans[groupKey(o)]
		if !ok {
			correction = globalMean
		}
		groupedErrors = append(groupedErrors, math.Abs(r-correction))
		scenarioBias[o.ScenarioFamily] = append(scenarioBias[o.ScenarioFamily], r)
		workerBias[o.WorkerPopulation] = append(workerBias[o.WorkerPopulation], r)
	}
	if len(deterministicErrors) == 0 {
		return DurationMetrics{}, errors.New("no completed eligible evaluation residuals")
	}
	return DurationMetrics{
		KnownCompleted:           len(deterministicErrors),
		DeterministicMAE:         mean(deterministicErrors),
		GlobalMeanCorrectedMAE:   mean(meanErrors),
		GlobalMedianCorrectedMAE: mean(medianErrors),
		GroupedMeanCorrectedMAE:  mean(groupedErrors),
		MedianAbsoluteError:      median(deterministicErrors),
		P90AbsoluteError:         quantileNearestRank(deterministicErrors, 0.90),
		SignedBiasByScenario:     meanMap(scenarioBias),
		SignedBiasByWorkerPop:    meanMap(workerBias),
	}, nil
}

func evaluateCompletion(train, eval []shadowsched.Observation) (CompletionMetrics, error) {
	var trainLabels []float64
	groupLabels := map[string][]float64{}
	for _, o := range train {
		if value, known := shadowsched.CompletionBeforeDeadline(o); known {
			y := 0.0
			if value {
				y = 1.0
			}
			trainLabels = append(trainLabels, y)
			groupLabels[groupKey(o)] = append(groupLabels[groupKey(o)], y)
		}
	}
	if len(trainLabels) == 0 {
		return CompletionMetrics{}, errors.New("no known completion targets in training data")
	}
	globalP := mean(trainLabels)
	groupP := map[string]float64{}
	for k, labels := range groupLabels {
		groupP[k] = mean(labels)
	}

	var globalLoss, groupedLoss float64
	known, eligible, censored := 0, 0, 0
	for _, o := range eval {
		if !o.PolicyEligible {
			continue
		}
		eligible++
		if o.Outcome.Status == "censored" {
			censored++
		}
		value, targetKnown := shadowsched.CompletionBeforeDeadline(o)
		if !targetKnown {
			continue
		}
		known++
		y := 0.0
		if value {
			y = 1.0
		}
		globalLoss += square(globalP - y)
		p, ok := groupP[groupKey(o)]
		if !ok {
			p = globalP
		}
		groupedLoss += square(p - y)
	}
	if known == 0 {
		return CompletionMetrics{}, errors.New("no known completion targets in evaluation data")
	}
	return CompletionMetrics{
		KnownTargets:              known,
		TotalEligible:             eligible,
		CensoredEligible:          censored,
		KnownTargetCoverage:       float64(known) / float64(eligible),
		CensoredFraction:          float64(censored) / float64(eligible),
		UnconditionalProbability: globalP,
		UnconditionalBrier:        globalLoss / float64(known),
		GroupedBrier:              groupedLoss / float64(known),
	}, nil
}

func groupKey(o shadowsched.Observation) string { return o.WorkerClass + "|" + o.TaskClass }
func square(v float64) float64                  { return v * v }

func mean(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func median(v []float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

func quantileNearestRank(v []float64, q float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	rank := int(math.Ceil(q*float64(len(c)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(c) {
		rank = len(c) - 1
	}
	return c[rank]
}

func meanMap(values map[string][]float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range values {
		out[k] = mean(v)
	}
	return out
}
