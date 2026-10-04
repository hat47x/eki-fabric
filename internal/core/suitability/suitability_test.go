package suitability

import (
	"errors"
	"slices"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestEvaluateFixedSuitabilityFixtures(t *testing.T) {
	tests := []struct {
		name        string
		input       Input
		want        Decision
		wantReason  ReasonCode
		wantFabric  uint64
	}{
		{
			name: "S0-1 large transfer is executable but not justified",
			input: fixtureInput(
				60_000,
				[]ComponentEstimate{
					component(ComponentInputTransfer, 81_920, "spike:artifact-locality-cost:8gib-transfer"),
					component(ComponentCompute, 20_000, "spike:artifact-locality-cost:remote-compute"),
				},
				excludeExcept(ComponentInputTransfer, ComponentCompute),
			),
			want:       DecisionFabricNotJustified,
			wantReason: ReasonTransferCostDominates,
			wantFabric: 101_920,
		},
		{
			name: "S0-2 small transfer makes Fabric worthwhile",
			input: fixtureInput(
				60_000,
				[]ComponentEstimate{
					component(ComponentInputTransfer, 100, "spike:artifact-locality-cost:10mib-transfer"),
					component(ComponentCompute, 20_000, "spike:artifact-locality-cost:remote-compute"),
				},
				excludeExcept(ComponentInputTransfer, ComponentCompute),
			),
			want:       DecisionFabricJustified,
			wantReason: ReasonEstimatedFabricAdvantage,
			wantFabric: 20_100,
		},
		{
			name: "S0-6 output transfer remains part of the Fabric path",
			input: fixtureInput(
				8_000,
				[]ComponentEstimate{
					component(ComponentCompute, 5_000, "spike:artifact-locality-cost:output-case-compute"),
					component(ComponentOutputTransfer, 10_000, "spike:artifact-locality-cost:500mib-output"),
				},
				excludeExcept(ComponentCompute, ComponentOutputTransfer),
			),
			want:       DecisionFabricNotJustified,
			wantReason: ReasonTransferCostDominates,
			wantFabric: 15_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(tt.input)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if got.Decision != tt.want {
				t.Fatalf("Decision = %q, want %q", got.Decision, tt.want)
			}
			if !slices.Contains(got.Reasons, tt.wantReason) {
				t.Fatalf("Reasons = %v, want %q", got.Reasons, tt.wantReason)
			}
			if got.FabricMillis == nil || *got.FabricMillis != tt.wantFabric {
				t.Fatalf("FabricMillis = %v, want %d", got.FabricMillis, tt.wantFabric)
			}
		})
	}
}

func TestBaselineMissingNeedsMeasurement(t *testing.T) {
	in := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentInputTransfer, 100, "fixture:input"),
			component(ComponentCompute, 20_000, "fixture:compute"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)
	in.Baseline = nil

	got, err := Evaluate(in)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got.Decision != DecisionNeedsMeasurement {
		t.Fatalf("Decision = %q", got.Decision)
	}
	if !slices.Contains(got.Reasons, ReasonBaselineMissing) {
		t.Fatalf("Reasons = %v", got.Reasons)
	}
	if !slices.Contains(got.Unresolved, "baseline") {
		t.Fatalf("Unresolved = %v", got.Unresolved)
	}
}

func TestMissingFabricComponentDoesNotBecomeImplicitZero(t *testing.T) {
	in := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentInputTransfer, 100, "fixture:input"),
			component(ComponentCompute, 20_000, "fixture:compute"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)
	in.Excluded = slices.DeleteFunc(in.Excluded, func(item ComponentExclusion) bool {
		return item.Kind == ComponentQueue
	})

	got, err := Evaluate(in)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got.Decision != DecisionNeedsMeasurement {
		t.Fatalf("Decision = %q", got.Decision)
	}
	if !slices.Contains(got.Reasons, ReasonFabricEstimateIncomplete) {
		t.Fatalf("Reasons = %v", got.Reasons)
	}
	if !slices.Contains(got.Unresolved, "component:queue") {
		t.Fatalf("Unresolved = %v", got.Unresolved)
	}
	if got.FabricMillis != nil {
		t.Fatalf("incomplete Fabric estimate must not expose a total: %v", *got.FabricMillis)
	}
}

func TestSuitabilityCannotReplaceSecurityDataOrResourceFeasibility(t *testing.T) {
	in := fixtureInput(
		60_000,
		[]ComponentEstimate{component(ComponentCompute, 20_000, "fixture:compute")},
		excludeExcept(ComponentCompute),
	)
	in.FeasibilityConfirmed = false

	_, err := Evaluate(in)
	if !errors.Is(err, ErrFeasibilityNotConfirmed) {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

func TestDecisionOverheadCanMakeSmallTaskNotWorthDistributing(t *testing.T) {
	in := fixtureInput(
		3_000,
		[]ComponentEstimate{
			component(ComponentSuitabilityDecision, 3_100, "fixture:measured-decision-overhead"),
			component(ComponentCompute, 100, "fixture:compute"),
		},
		excludeExcept(ComponentSuitabilityDecision, ComponentCompute),
	)

	got, err := Evaluate(in)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got.Decision != DecisionFabricNotJustified {
		t.Fatalf("Decision = %q", got.Decision)
	}
	if !slices.Contains(got.Reasons, ReasonDecisionOverheadDominates) {
		t.Fatalf("Reasons = %v", got.Reasons)
	}
}

func TestBenefitInsideConfiguredMarginNeedsMeasurement(t *testing.T) {
	in := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentInputTransfer, 1_000, "fixture:input"),
			component(ComponentCompute, 58_500, "fixture:compute"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)
	in.MinimumBenefitMillis = 1_000

	got, err := Evaluate(in)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got.Decision != DecisionNeedsMeasurement {
		t.Fatalf("Decision = %q", got.Decision)
	}
	if !slices.Contains(got.Reasons, ReasonBenefitMarginInsufficient) {
		t.Fatalf("Reasons = %v", got.Reasons)
	}
}

func TestComputeCannotBeExcludedFromComparison(t *testing.T) {
	in := fixtureInput(
		60_000,
		[]ComponentEstimate{component(ComponentInputTransfer, 100, "fixture:input")},
		excludeExcept(ComponentInputTransfer),
	)

	_, err := Evaluate(in)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

func TestMinimumBenefitMarginMustBeExplicit(t *testing.T) {
	in := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentInputTransfer, 100, "fixture:input"),
			component(ComponentCompute, 20_000, "fixture:compute"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)
	in.MinimumBenefitMillis = 0

	_, err := Evaluate(in)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

func TestReceiptCarriesNormalizedDecisionBasis(t *testing.T) {
	in := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentCompute, 20_000, "fixture:compute"),
			component(ComponentInputTransfer, 100, "fixture:input"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)
	slices.Reverse(in.Excluded)

	got, err := Evaluate(in)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got.WorkloadDigest.String() != in.WorkloadDigest.String() {
		t.Fatalf("WorkloadDigest = %s, want %s", got.WorkloadDigest, in.WorkloadDigest)
	}
	if got.PolicyRevision != in.PolicyRevision || got.EvaluatorVersion != in.EvaluatorVersion {
		t.Fatalf("receipt lost policy/evaluator identity: %+v", got)
	}
	if got.MinimumBenefitMillis != in.MinimumBenefitMillis {
		t.Fatalf("MinimumBenefitMillis = %d", got.MinimumBenefitMillis)
	}
	if len(got.Components) != 2 || got.Components[0].Kind != ComponentInputTransfer || got.Components[1].Kind != ComponentCompute {
		t.Fatalf("Components are not normalized: %+v", got.Components)
	}
	for i := 1; i < len(got.Excluded); i++ {
		if componentRank(got.Excluded[i-1].Kind) > componentRank(got.Excluded[i].Kind) {
			t.Fatalf("Excluded components are not normalized: %+v", got.Excluded)
		}
	}
}

func TestDecisionBasisIsDeterministicAndChangesWithEvidence(t *testing.T) {
	in := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentInputTransfer, 100, "fixture:input"),
			component(ComponentCompute, 20_000, "fixture:compute"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)

	first, err := Evaluate(in)
	if err != nil {
		t.Fatalf("Evaluate(first) error = %v", err)
	}

	shuffled := in
	shuffled.Components = []ComponentEstimate{in.Components[1], in.Components[0]}
	shuffled.Excluded = append([]ComponentExclusion(nil), in.Excluded...)
	slices.Reverse(shuffled.Excluded)

	second, err := Evaluate(shuffled)
	if err != nil {
		t.Fatalf("Evaluate(shuffled) error = %v", err)
	}
	if first.BasisDigest.String() != second.BasisDigest.String() {
		t.Fatalf("same evidence basis produced different digests: %s != %s", first.BasisDigest, second.BasisDigest)
	}
	if first.Decision != second.Decision {
		t.Fatalf("same evidence basis produced different decisions: %q != %q", first.Decision, second.Decision)
	}

	changed := in
	changed.Baseline = &DurationEvidence{Millis: 30_000, EvidenceRef: "fixture:baseline-v2"}
	third, err := Evaluate(changed)
	if err != nil {
		t.Fatalf("Evaluate(changed) error = %v", err)
	}
	if first.BasisDigest.String() == third.BasisDigest.String() {
		t.Fatal("changed baseline evidence reused the previous decision basis digest")
	}
}

func TestEvaluateForFabricEntryReturnsEntryOnlyWhenJustified(t *testing.T) {
	justified := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentInputTransfer, 100, "fixture:input"),
			component(ComponentCompute, 20_000, "fixture:compute"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)
	receipt, entry, err := EvaluateForFabricEntry(justified)
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry(justified) error = %v", err)
	}
	if receipt.Decision != DecisionFabricJustified {
		t.Fatalf("Decision = %q", receipt.Decision)
	}
	if entry == nil || !entry.Valid() {
		t.Fatalf("justified evaluation did not produce a valid FabricEntry: %+v", entry)
	}
	if entry.WorkloadDigest().String() != receipt.WorkloadDigest.String() {
		t.Fatalf("WorkloadDigest = %s, want %s", entry.WorkloadDigest(), receipt.WorkloadDigest)
	}
	if entry.BasisDigest().String() != receipt.BasisDigest.String() {
		t.Fatalf("BasisDigest = %s, want %s", entry.BasisDigest(), receipt.BasisDigest)
	}

	notJustified := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentInputTransfer, 81_920, "fixture:input"),
			component(ComponentCompute, 20_000, "fixture:compute"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)
	receipt, entry, err = EvaluateForFabricEntry(notJustified)
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry(not justified) error = %v", err)
	}
	if receipt.Decision != DecisionFabricNotJustified {
		t.Fatalf("Decision = %q", receipt.Decision)
	}
	if entry != nil {
		t.Fatalf("not-justified evaluation produced FabricEntry: %+v", entry)
	}

	needsMeasurement := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentInputTransfer, 100, "fixture:input"),
			component(ComponentCompute, 20_000, "fixture:compute"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)
	needsMeasurement.Baseline = nil
	receipt, entry, err = EvaluateForFabricEntry(needsMeasurement)
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry(needs measurement) error = %v", err)
	}
	if receipt.Decision != DecisionNeedsMeasurement {
		t.Fatalf("Decision = %q", receipt.Decision)
	}
	if entry != nil {
		t.Fatalf("needs-measurement evaluation produced FabricEntry: %+v", entry)
	}
}

func TestDecisionReceiptCannotBeConvertedIntoFabricEntry(t *testing.T) {
	fabricated := DecisionReceipt{
		WorkloadDigest:   contract.SHA256([]byte("fabricated-workload")),
		PolicyRevision:   1,
		EvaluatorVersion: "fabricated",
		Decision:         DecisionFabricJustified,
		BasisDigest:      contract.SHA256([]byte("fabricated-basis")),
	}
	if fabricated.Decision != DecisionFabricJustified {
		t.Fatal("fixture error")
	}

	var zero FabricEntry
	if zero.Valid() {
		t.Fatal("zero FabricEntry unexpectedly valid")
	}
	// There is deliberately no exported constructor from DecisionReceipt to
	// FabricEntry. A persisted or caller-supplied enum remains evidence only.
}

func TestFabricEntryCarriesSuitabilityBasisButNoPlacementAuthority(t *testing.T) {
	in := fixtureInput(
		60_000,
		[]ComponentEstimate{
			component(ComponentInputTransfer, 100, "fixture:input"),
			component(ComponentCompute, 20_000, "fixture:compute"),
		},
		excludeExcept(ComponentInputTransfer, ComponentCompute),
	)
	receipt, entry, err := EvaluateForFabricEntry(in)
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry() error = %v", err)
	}
	if entry == nil {
		t.Fatal("entry is nil")
	}
	if entry.PolicyRevision() != receipt.PolicyRevision {
		t.Fatalf("PolicyRevision = %d, want %d", entry.PolicyRevision(), receipt.PolicyRevision)
	}
	if entry.EvaluatorVersion() != receipt.EvaluatorVersion {
		t.Fatalf("EvaluatorVersion = %q, want %q", entry.EvaluatorVersion(), receipt.EvaluatorVersion)
	}
	// FabricEntry intentionally exposes no Worker, Attempt, Lease, ArtifactGrant,
	// WorkOrder, or execution fields. Those authorities remain downstream.
}

func fixtureInput(
	baselineMillis uint64,
	components []ComponentEstimate,
	excluded []ComponentExclusion,
) Input {
	return Input{
		WorkloadDigest:       contract.SHA256([]byte("fixture-workload")),
		PolicyRevision:       1,
		EvaluatorVersion:     "suitability-v0",
		FeasibilityConfirmed: true,
		Baseline: &DurationEvidence{
			Millis:      baselineMillis,
			EvidenceRef: "fixture:local-baseline",
		},
		Components:           components,
		Excluded:             excluded,
		MinimumBenefitMillis: 1_000,
	}
}

func component(kind ComponentKind, millis uint64, evidence string) ComponentEstimate {
	return ComponentEstimate{
		Kind:        kind,
		Millis:      millis,
		EvidenceRef: evidence,
	}
}

func excludeExcept(included ...ComponentKind) []ComponentExclusion {
	keep := map[ComponentKind]bool{}
	for _, kind := range included {
		keep[kind] = true
	}
	var out []ComponentExclusion
	for _, kind := range componentOrder {
		if keep[kind] {
			continue
		}
		out = append(out, ComponentExclusion{
			Kind:   kind,
			Reason: "fixture explicitly excludes this path cost",
		})
	}
	return out
}
