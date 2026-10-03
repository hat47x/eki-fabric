package suitability

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

var (
	ErrInvalidInput              = errors.New("invalid suitability input")
	ErrFeasibilityNotConfirmed   = errors.New("security/data/resource feasibility is not confirmed")
)

type Decision string

const (
	DecisionFabricJustified    Decision = "FABRIC_JUSTIFIED"
	DecisionFabricNotJustified Decision = "FABRIC_NOT_JUSTIFIED"
	DecisionNeedsMeasurement   Decision = "NEEDS_MEASUREMENT"
)

type ReasonCode string

const (
	ReasonBaselineMissing           ReasonCode = "baseline_missing"
	ReasonFabricEstimateIncomplete  ReasonCode = "fabric_estimate_incomplete"
	ReasonBenefitMarginInsufficient ReasonCode = "benefit_margin_insufficient"
	ReasonTransferCostDominates     ReasonCode = "transfer_cost_dominates"
	ReasonDecisionOverheadDominates ReasonCode = "decision_overhead_dominates"
	ReasonDispatchOverheadDominates ReasonCode = "dispatch_overhead_dominates"
	ReasonFabricPathSlower          ReasonCode = "fabric_path_slower"
	ReasonEstimatedFabricAdvantage  ReasonCode = "estimated_fabric_advantage"
)

type ComponentKind string

const (
	ComponentSuitabilityDecision ComponentKind = "suitability_decision"
	ComponentAdmissionPlacement  ComponentKind = "admission_placement"
	ComponentDispatch            ComponentKind = "dispatch"
	ComponentQueue               ComponentKind = "queue"
	ComponentInputTransfer       ComponentKind = "input_transfer"
	ComponentCompute             ComponentKind = "compute"
	ComponentRetryPenalty        ComponentKind = "retry_penalty"
	ComponentOutputTransfer      ComponentKind = "output_transfer"
	ComponentVerification        ComponentKind = "verification"
)

var componentOrder = []ComponentKind{
	ComponentSuitabilityDecision,
	ComponentAdmissionPlacement,
	ComponentDispatch,
	ComponentQueue,
	ComponentInputTransfer,
	ComponentCompute,
	ComponentRetryPenalty,
	ComponentOutputTransfer,
	ComponentVerification,
}

type DurationEvidence struct {
	Millis      uint64
	EvidenceRef string
}

type ComponentEstimate struct {
	Kind        ComponentKind
	Millis      uint64
	EvidenceRef string
}

type ComponentExclusion struct {
	Kind   ComponentKind
	Reason string
}

// Input is evaluated only after security, data, and resource feasibility has
// been established by the existing authority boundaries. Suitability decides
// whether entering the Fabric path is justified; it never authorizes a Worker.
//
// Every Fabric path component must be either estimated or explicitly excluded.
// Absence is unresolved and must not be interpreted as zero cost.
type Input struct {
	WorkloadDigest        contract.Digest
	PolicyRevision        uint64
	EvaluatorVersion      string
	FeasibilityConfirmed  bool
	Baseline              *DurationEvidence
	Components            []ComponentEstimate
	Excluded              []ComponentExclusion
	MinimumBenefitMillis  uint64
}

type DecisionReceipt struct {
	WorkloadDigest       contract.Digest
	PolicyRevision       uint64
	EvaluatorVersion     string
	Decision             Decision
	Reasons              []ReasonCode
	Unresolved           []string
	Components           []ComponentEstimate
	Excluded             []ComponentExclusion
	MinimumBenefitMillis uint64
	FabricMillis         *uint64
	BaselineMillis       *uint64
	EvidenceRefs         []string
	BasisDigest          contract.Digest
}

// Evaluate compares a fully described Fabric path with an existing execution
// baseline. It does not start local execution, select a Worker, or create any
// placement/lease/work-order authority.
func Evaluate(in Input) (DecisionReceipt, error) {
	components, exclusions, err := validateAndNormalize(in)
	if err != nil {
		return DecisionReceipt{}, err
	}

	basisDigest, err := digestBasis(in, components, exclusions)
	if err != nil {
		return DecisionReceipt{}, err
	}

	receipt := DecisionReceipt{
		WorkloadDigest:       in.WorkloadDigest,
		PolicyRevision:       in.PolicyRevision,
		EvaluatorVersion:     in.EvaluatorVersion,
		Components:           append([]ComponentEstimate(nil), components...),
		Excluded:             append([]ComponentExclusion(nil), exclusions...),
		MinimumBenefitMillis: in.MinimumBenefitMillis,
		BasisDigest:          basisDigest,
		EvidenceRefs:         collectEvidenceRefs(in.Baseline, components),
	}

	missing := missingComponents(components, exclusions)
	if in.Baseline == nil {
		receipt.Reasons = append(receipt.Reasons, ReasonBaselineMissing)
		receipt.Unresolved = append(receipt.Unresolved, "baseline")
	} else {
		baselineMillis := in.Baseline.Millis
		receipt.BaselineMillis = &baselineMillis
	}

	if len(missing) > 0 {
		receipt.Reasons = append(receipt.Reasons, ReasonFabricEstimateIncomplete)
		for _, kind := range missing {
			receipt.Unresolved = append(receipt.Unresolved, "component:"+string(kind))
		}
	}

	if in.Baseline == nil || len(missing) > 0 {
		receipt.Decision = DecisionNeedsMeasurement
		return receipt, nil
	}

	fabricMillis, err := sumComponents(components)
	if err != nil {
		return DecisionReceipt{}, err
	}
	receipt.FabricMillis = &fabricMillis

	baselineMillis := in.Baseline.Millis
	if fabricMillis >= baselineMillis {
		receipt.Decision = DecisionFabricNotJustified
		receipt.Reasons = dominanceReasons(components, baselineMillis)
		receipt.Reasons = append(receipt.Reasons, ReasonFabricPathSlower)
		return receipt, nil
	}

	benefitMillis := baselineMillis - fabricMillis
	if benefitMillis <= in.MinimumBenefitMillis {
		receipt.Decision = DecisionNeedsMeasurement
		receipt.Reasons = []ReasonCode{ReasonBenefitMarginInsufficient}
		receipt.Unresolved = []string{"benefit_margin"}
		return receipt, nil
	}

	receipt.Decision = DecisionFabricJustified
	receipt.Reasons = []ReasonCode{ReasonEstimatedFabricAdvantage}
	return receipt, nil
}

func validateAndNormalize(in Input) ([]ComponentEstimate, []ComponentExclusion, error) {
	if in.WorkloadDigest.IsZero() {
		return nil, nil, fmt.Errorf("%w: workload digest is required", ErrInvalidInput)
	}
	if in.PolicyRevision == 0 {
		return nil, nil, fmt.Errorf("%w: policy revision must be positive", ErrInvalidInput)
	}
	if in.EvaluatorVersion == "" {
		return nil, nil, fmt.Errorf("%w: evaluator version is required", ErrInvalidInput)
	}
	if !in.FeasibilityConfirmed {
		return nil, nil, ErrFeasibilityNotConfirmed
	}
	if in.MinimumBenefitMillis == 0 {
		return nil, nil, fmt.Errorf("%w: minimum benefit margin must be positive", ErrInvalidInput)
	}
	if in.Baseline != nil {
		if in.Baseline.Millis == 0 {
			return nil, nil, fmt.Errorf("%w: baseline duration must be positive", ErrInvalidInput)
		}
		if in.Baseline.EvidenceRef == "" {
			return nil, nil, fmt.Errorf("%w: baseline evidence reference is required", ErrInvalidInput)
		}
	}

	known := make(map[ComponentKind]bool, len(componentOrder))
	for _, kind := range componentOrder {
		known[kind] = true
	}

	components := append([]ComponentEstimate(nil), in.Components...)
	seenComponents := map[ComponentKind]bool{}
	for _, component := range components {
		if !known[component.Kind] {
			return nil, nil, fmt.Errorf("%w: unknown Fabric component %q", ErrInvalidInput, component.Kind)
		}
		if seenComponents[component.Kind] {
			return nil, nil, fmt.Errorf("%w: duplicate Fabric component %q", ErrInvalidInput, component.Kind)
		}
		if component.EvidenceRef == "" {
			return nil, nil, fmt.Errorf("%w: component %q requires an evidence reference", ErrInvalidInput, component.Kind)
		}
		seenComponents[component.Kind] = true
	}

	exclusions := append([]ComponentExclusion(nil), in.Excluded...)
	seenExclusions := map[ComponentKind]bool{}
	for _, exclusion := range exclusions {
		if !known[exclusion.Kind] {
			return nil, nil, fmt.Errorf("%w: unknown excluded component %q", ErrInvalidInput, exclusion.Kind)
		}
		if seenExclusions[exclusion.Kind] {
			return nil, nil, fmt.Errorf("%w: duplicate excluded component %q", ErrInvalidInput, exclusion.Kind)
		}
		if seenComponents[exclusion.Kind] {
			return nil, nil, fmt.Errorf("%w: component %q cannot be both estimated and excluded", ErrInvalidInput, exclusion.Kind)
		}
		if exclusion.Kind == ComponentCompute {
			return nil, nil, fmt.Errorf("%w: compute duration cannot be excluded from a Fabric comparison", ErrInvalidInput)
		}
		if exclusion.Reason == "" {
			return nil, nil, fmt.Errorf("%w: excluded component %q requires a reason", ErrInvalidInput, exclusion.Kind)
		}
		seenExclusions[exclusion.Kind] = true
	}

	sort.Slice(components, func(i, j int) bool {
		return componentRank(components[i].Kind) < componentRank(components[j].Kind)
	})
	sort.Slice(exclusions, func(i, j int) bool {
		return componentRank(exclusions[i].Kind) < componentRank(exclusions[j].Kind)
	})

	return components, exclusions, nil
}

func missingComponents(components []ComponentEstimate, exclusions []ComponentExclusion) []ComponentKind {
	covered := make(map[ComponentKind]bool, len(components)+len(exclusions))
	for _, component := range components {
		covered[component.Kind] = true
	}
	for _, exclusion := range exclusions {
		covered[exclusion.Kind] = true
	}

	var missing []ComponentKind
	for _, kind := range componentOrder {
		if !covered[kind] {
			missing = append(missing, kind)
		}
	}
	return missing
}

func sumComponents(components []ComponentEstimate) (uint64, error) {
	var total uint64
	for _, component := range components {
		if math.MaxUint64-total < component.Millis {
			return 0, fmt.Errorf("%w: Fabric duration overflow", ErrInvalidInput)
		}
		total += component.Millis
	}
	return total, nil
}

func dominanceReasons(components []ComponentEstimate, baselineMillis uint64) []ReasonCode {
	values := make(map[ComponentKind]uint64, len(components))
	for _, component := range components {
		values[component.Kind] = component.Millis
	}

	var reasons []ReasonCode
	transferMillis := values[ComponentInputTransfer] + values[ComponentOutputTransfer]
	if transferMillis >= baselineMillis {
		reasons = append(reasons, ReasonTransferCostDominates)
	}
	if values[ComponentSuitabilityDecision] >= baselineMillis {
		reasons = append(reasons, ReasonDecisionOverheadDominates)
	}
	dispatchMillis := values[ComponentAdmissionPlacement] + values[ComponentDispatch] + values[ComponentQueue]
	if dispatchMillis >= baselineMillis {
		reasons = append(reasons, ReasonDispatchOverheadDominates)
	}
	return reasons
}

func collectEvidenceRefs(baseline *DurationEvidence, components []ComponentEstimate) []string {
	seen := map[string]bool{}
	if baseline != nil {
		seen[baseline.EvidenceRef] = true
	}
	for _, component := range components {
		seen[component.EvidenceRef] = true
	}

	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func componentRank(kind ComponentKind) int {
	for i, candidate := range componentOrder {
		if candidate == kind {
			return i
		}
	}
	return len(componentOrder)
}

type basisDocument struct {
	WorkloadDigest       string
	PolicyRevision       uint64
	EvaluatorVersion     string
	FeasibilityConfirmed bool
	Baseline             *DurationEvidence
	Components           []ComponentEstimate
	Excluded             []ComponentExclusion
	MinimumBenefitMillis uint64
}

func digestBasis(in Input, components []ComponentEstimate, exclusions []ComponentExclusion) (contract.Digest, error) {
	doc := basisDocument{
		WorkloadDigest:       in.WorkloadDigest.String(),
		PolicyRevision:       in.PolicyRevision,
		EvaluatorVersion:     in.EvaluatorVersion,
		FeasibilityConfirmed: in.FeasibilityConfirmed,
		Baseline:             in.Baseline,
		Components:           components,
		Excluded:             exclusions,
		MinimumBenefitMillis: in.MinimumBenefitMillis,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return contract.Digest{}, fmt.Errorf("%w: encode decision basis: %v", ErrInvalidInput, err)
	}
	return contract.SHA256(raw), nil
}
