package resultaccept

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func digestOf(s string) string { return contract.SHA256([]byte(s)).Hex() }

var (
	claimedDigest   = digestOf("claimed output")
	differentDigest = digestOf("a different output")
)

// producerDims are the trusted dimension values of the Worker that made the claim.
func producerDims() map[Dimension]string {
	return map[Dimension]string{
		DimensionPrincipal:      "principal-P0",
		DimensionOperator:       "operator-O0",
		DimensionExecutorDigest: digestOf("executor-E0"),
	}
}

func baseClaim() ResultClaim {
	return ResultClaim{
		TaskID:       "task-1",
		AttemptID:    "attempt-1",
		WorkerID:     "worker-0",
		OutputDigest: claimedDigest,
		Dimensions:   producerDims(),
	}
}

// subjectDims gives a verifier its own executor digest unless a test overrides it.
func subjectDims(id, principal, operator string) map[Dimension]string {
	return map[Dimension]string{
		DimensionPrincipal:      principal,
		DimensionOperator:       operator,
		DimensionExecutorDigest: digestOf("executor-of-" + id),
	}
}

func verification(id, principal, operator string, check Check, observed string) Verification {
	return Verification{
		VerifierID:     id,
		TaskID:         "task-1",
		AttemptID:      "attempt-1",
		Check:          check,
		ObservedDigest: observed,
		Dimensions:     subjectDims(id, principal, operator),
	}
}

func reproduced(id, principal, operator string) Verification {
	return verification(id, principal, operator, CheckReproduced, claimedDigest)
}

func differs(id, principal, operator string) Verification {
	return verification(id, principal, operator, CheckDiffers, differentDigest)
}

func validatorAccepted(id, principal, operator string) Verification {
	return verification(id, principal, operator, CheckValidatorAccepted, claimedDigest)
}

func validatorRejected(id, principal, operator string) Verification {
	return verification(id, principal, operator, CheckValidatorRejected, claimedDigest)
}

// sybilSwarm is n identities that all sit behind one principal.
func sybilSwarm(principal string, n int) []Verification {
	out := make([]Verification, 0, n)
	for i := range n {
		out = append(out, reproduced(fmt.Sprintf("sybil-%03d", i), principal, fmt.Sprintf("operator-S%03d", i)))
	}
	return out
}

type input struct {
	claim  ResultClaim
	verifs []Verification
	output OutputClass
	policy *Policy
}

func mk(output OutputClass, policy *Policy, verifs ...Verification) input {
	return input{claim: baseClaim(), verifs: verifs, output: output, policy: policy}
}

// validInput is accepted: two independent voices reproduce the claimed digest.
func validInput() input {
	return mk(OutputMechanical,
		&Policy{Level: LevelRA2, Threshold: 2, DistinctDimensions: []Dimension{DimensionPrincipal, DimensionOperator}},
		reproduced("verifier-1", "principal-P1", "operator-O1"),
		reproduced("verifier-2", "principal-P2", "operator-O2"),
	)
}

func (in input) decide() (Decision, error) {
	return Decide(in.claim, in.verifs, in.output, in.policy)
}

// checkInvariants holds for every accepted, held or rejected decision.
func checkInvariants(t *testing.T, in input, dec Decision) {
	t.Helper()
	_, wantDigest, err := checkPolicy(in.policy)
	if err != nil {
		t.Fatalf("policy under test is invalid: %v", err)
	}
	if dec.PolicyDigest != wantDigest || dec.PolicyDigest.IsZero() {
		t.Errorf("policy digest = %s, want %s", dec.PolicyDigest, wantDigest)
	}
	switch dec.Outcome {
	case OutcomeAccepted:
		if dec.Level != in.policy.Level {
			t.Errorf("accepted at level %s, want %s", dec.Level, in.policy.Level)
		}
		if len(dec.Voices) < in.policy.Threshold {
			t.Errorf("accepted with %d voices, threshold %d", len(dec.Voices), in.policy.Threshold)
		}
		if dec.Reason != ReasonQuorumMet {
			t.Errorf("accepted with reason %q", dec.Reason)
		}
		if in.output == OutputSemanticallyOpen && dec.Level == LevelRA2 {
			t.Errorf("semantically open output reported %s", LevelRA2)
		}
	case OutcomeHeld, OutcomeRejected:
		if dec.Level != LevelRA0 {
			t.Errorf("%s decision reports level %s, want %s", dec.Outcome, dec.Level, LevelRA0)
		}
	default:
		t.Errorf("unknown outcome %q", dec.Outcome)
	}
	for _, v := range dec.Voices {
		if !slices.Contains(v.Checks, requiredCheck(in.policy.Level)) {
			t.Errorf("voice %v carries no check the level needs", v.VerifierIDs)
		}
	}
	if !slices.IsSorted(dec.Verifiers) {
		t.Errorf("verifiers %v are not sorted", dec.Verifiers)
	}
	for _, s := range baseNotGuaranteed {
		if !slices.Contains(dec.NotGuaranteed, s) {
			t.Errorf("NotGuaranteed lacks %q", s)
		}
	}
	want := NotGuaranteedMechanicalCheckLimit
	if in.output == OutputSemanticallyOpen {
		want = NotGuaranteedSemanticCorrectness
	}
	if !slices.Contains(dec.NotGuaranteed, want) {
		t.Errorf("NotGuaranteed lacks %q", want)
	}
}

type decisionCase struct {
	name     string
	in       input
	outcome  Outcome
	reason   Reason
	level    AssuranceLevel
	voices   int
	dissent  int
	excluded int
}

func decisionCases() []decisionCase {
	pAndO := []Dimension{DimensionPrincipal, DimensionOperator}
	onlyP := []Dimension{DimensionPrincipal}
	ra2 := func(threshold int, distinct []Dimension) *Policy {
		return &Policy{Level: LevelRA2, Threshold: threshold, DistinctDimensions: distinct}
	}
	ra1 := func(threshold int, distinct []Dimension) *Policy {
		return &Policy{Level: LevelRA1, Threshold: threshold, DistinctDimensions: distinct}
	}
	withAuthority := func(distinct []Dimension, authorities ...string) *Policy {
		return &Policy{Level: LevelRA2, Threshold: 2, DistinctDimensions: distinct, RejectAuthorities: authorities}
	}
	swapExecutor := reproduced("verifier-x", "principal-PX", "operator-OX")
	swapExecutor.Dimensions[DimensionExecutorDigest] = digestOf("executor-E0")

	return []decisionCase{
		// Quorum and independence
		{
			name:    "no verification is held",
			in:      mk(OutputMechanical, ra2(2, pAndO)),
			outcome: OutcomeHeld, reason: ReasonNoIndependentVerification, level: LevelRA0,
		},
		{
			name:    "a single independent replica is held below the threshold",
			in:      mk(OutputMechanical, ra2(2, pAndO), reproduced("verifier-1", "principal-P1", "operator-O1")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1,
		},
		{
			name:    "two independent replicas reach RA-2",
			in:      validInput(),
			outcome: OutcomeAccepted, reason: ReasonQuorumMet, level: LevelRA2, voices: 2,
		},
		{
			name:    "one hundred identities behind one principal count as one voice",
			in:      mk(OutputMechanical, ra2(2, pAndO), sybilSwarm("principal-X", 100)...),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1,
		},
		{
			name: "a shared operator merges voices when operator must differ",
			in: mk(OutputMechanical, ra2(2, pAndO),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-2", "principal-P2", "operator-O1")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1,
		},
		{
			name: "a shared operator is allowed when only principal must differ",
			in: mk(OutputMechanical, ra2(2, onlyP),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-2", "principal-P2", "operator-O1")),
			outcome: OutcomeAccepted, reason: ReasonQuorumMet, level: LevelRA2, voices: 2,
		},
		{
			name: "shared values chain into one voice",
			in: mk(OutputMechanical, ra2(2, pAndO),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-2", "principal-P1", "operator-O2"),
				reproduced("verifier-3", "principal-P3", "operator-O2")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1,
		},
		{
			name: "an identical repeat adds no voice",
			in: mk(OutputMechanical, ra2(2, pAndO),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-1", "principal-P1", "operator-O1")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1,
		},

		// Verifier authority (RQ-044)
		{
			name: "the producer's principal cannot justify the result",
			in: mk(OutputMechanical, ra2(2, pAndO),
				reproduced("verifier-0", "principal-P0", "operator-O9"),
				reproduced("verifier-1", "principal-P1", "operator-O1")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1, excluded: 1,
		},
		{
			name: "the producer's principal is excluded even when principal is not a distinct dimension",
			in: mk(OutputMechanical, ra2(3, []Dimension{DimensionOperator}),
				reproduced("verifier-0", "principal-P0", "operator-O9"),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-2", "principal-P2", "operator-O2")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 2, excluded: 1,
		},
		{
			name: "the producer's executor digest cannot justify the result",
			in: mk(OutputMechanical, ra2(2, pAndO),
				swapExecutor,
				reproduced("verifier-1", "principal-P1", "operator-O1")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1, excluded: 1,
		},
		{
			name: "the producing worker cannot verify itself",
			in: mk(OutputMechanical, ra2(2, pAndO),
				reproduced("worker-0", "principal-P7", "operator-O7"),
				reproduced("verifier-1", "principal-P1", "operator-O1")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1, excluded: 1,
		},
		{
			name: "a verifier sharing a distinct dimension with the producer is excluded",
			in: mk(OutputMechanical, ra2(2, pAndO),
				reproduced("verifier-5", "principal-P5", "operator-O0"),
				reproduced("verifier-1", "principal-P1", "operator-O1")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1, excluded: 1,
		},
		{
			name: "a verifier sharing only an undeclared dimension is not excluded",
			in: mk(OutputMechanical, ra2(2, onlyP),
				reproduced("verifier-5", "principal-P5", "operator-O0"),
				reproduced("verifier-1", "principal-P1", "operator-O1")),
			outcome: OutcomeAccepted, reason: ReasonQuorumMet, level: LevelRA2, voices: 2,
		},
		{
			name: "only producer-excluded verifications leave no independent verification",
			in: mk(OutputMechanical, ra2(1, pAndO),
				reproduced("worker-0", "principal-P7", "operator-O7")),
			outcome: OutcomeHeld, reason: ReasonNoIndependentVerification, level: LevelRA0, excluded: 1,
		},

		// Disagreement and explicit rejection (RQ-044, PAS-19)
		{
			name: "an independent mismatch is held with its evidence",
			in: mk(OutputMechanical, ra2(2, pAndO),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-2", "principal-P2", "operator-O2"),
				differs("verifier-3", "principal-P3", "operator-O3")),
			outcome: OutcomeHeld, reason: ReasonVerificationsDisagree, level: LevelRA0, voices: 2, dissent: 1,
		},
		{
			name: "a validator rejection without authority is held",
			in: mk(OutputMechanical, ra2(2, pAndO),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-2", "principal-P2", "operator-O2"),
				validatorRejected("validator-B", "principal-P3", "operator-O3")),
			outcome: OutcomeHeld, reason: ReasonVerificationsDisagree, level: LevelRA0, voices: 2, dissent: 1,
		},
		{
			name: "a rejection by a named authority is final",
			in: mk(OutputMechanical, withAuthority(pAndO, "validator-A"),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-2", "principal-P2", "operator-O2"),
				validatorRejected("validator-A", "principal-P9", "operator-O9")),
			outcome: OutcomeRejected, reason: ReasonAuthorityRejected, level: LevelRA0, voices: 2, dissent: 1,
		},
		{
			name: "a named authority's mismatch is held, not rejected",
			in: mk(OutputMechanical, withAuthority(pAndO, "validator-A"),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-2", "principal-P2", "operator-O2"),
				differs("validator-A", "principal-P9", "operator-O9")),
			outcome: OutcomeHeld, reason: ReasonVerificationsDisagree, level: LevelRA0, voices: 2, dissent: 1,
		},
		{
			name: "a named authority's rejection counts even when the authority is excluded",
			in: mk(OutputMechanical, withAuthority(pAndO, "validator-A"),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				validatorRejected("validator-A", "principal-P0", "operator-O9")),
			outcome: OutcomeRejected, reason: ReasonAuthorityRejected, level: LevelRA0, voices: 1, dissent: 1, excluded: 1,
		},

		// Semantically open outputs and assurance levels (RQ-045, PAS-14)
		{
			name: "a semantically open output reaches RA-1 with the semantic limit stated",
			in: mk(OutputSemanticallyOpen, ra1(2, pAndO),
				validatorAccepted("verifier-1", "principal-P1", "operator-O1"),
				validatorAccepted("verifier-2", "principal-P2", "operator-O2")),
			outcome: OutcomeAccepted, reason: ReasonQuorumMet, level: LevelRA1, voices: 2,
		},
		{
			name: "semantically open replicas alone do not reach RA-1",
			in: mk(OutputSemanticallyOpen, ra1(2, pAndO),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				reproduced("verifier-2", "principal-P2", "operator-O2")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0,
		},
		{
			name: "a mismatch on a semantically open output is held",
			in: mk(OutputSemanticallyOpen, ra1(2, pAndO),
				validatorAccepted("verifier-1", "principal-P1", "operator-O1"),
				differs("verifier-2", "principal-P2", "operator-O2")),
			outcome: OutcomeHeld, reason: ReasonVerificationsDisagree, level: LevelRA0, voices: 1, dissent: 1,
		},
		{
			name: "mechanical validator acceptance reaches RA-1",
			in: mk(OutputMechanical, ra1(2, pAndO),
				validatorAccepted("verifier-1", "principal-P1", "operator-O1"),
				validatorAccepted("verifier-2", "principal-P2", "operator-O2")),
			outcome: OutcomeAccepted, reason: ReasonQuorumMet, level: LevelRA1, voices: 2,
		},
		{
			name: "validator acceptance alone does not reach RA-2",
			in: mk(OutputMechanical, ra2(2, pAndO),
				validatorAccepted("verifier-1", "principal-P1", "operator-O1"),
				validatorAccepted("verifier-2", "principal-P2", "operator-O2")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0,
		},
		{
			name: "mixed checks do not combine into RA-2",
			in: mk(OutputMechanical, ra2(2, pAndO),
				reproduced("verifier-1", "principal-P1", "operator-O1"),
				validatorAccepted("verifier-2", "principal-P2", "operator-O2")),
			outcome: OutcomeHeld, reason: ReasonQuorumNotMet, level: LevelRA0, voices: 1,
		},
	}
}

func TestDecide(t *testing.T) {
	for _, tc := range decisionCases() {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := tc.in.decide()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dec.Outcome != tc.outcome {
				t.Errorf("outcome = %s, want %s", dec.Outcome, tc.outcome)
			}
			if dec.Reason != tc.reason {
				t.Errorf("reason = %s, want %s", dec.Reason, tc.reason)
			}
			if dec.Level != tc.level {
				t.Errorf("level = %s, want %s", dec.Level, tc.level)
			}
			if len(dec.Voices) != tc.voices {
				t.Errorf("voices = %d, want %d", len(dec.Voices), tc.voices)
			}
			if len(dec.Dissent) != tc.dissent {
				t.Errorf("dissent = %d, want %d", len(dec.Dissent), tc.dissent)
			}
			if len(dec.Excluded) != tc.excluded {
				t.Errorf("excluded = %d, want %d", len(dec.Excluded), tc.excluded)
			}
			checkInvariants(t, tc.in, dec)
		})
	}
}

func TestDecideReportsEvidence(t *testing.T) {
	t.Run("accepted decision lists voices, their dimensions and verifiers", func(t *testing.T) {
		dec, err := validInput().decide()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := []string{"verifier-1", "verifier-2"}; !slices.Equal(dec.Verifiers, want) {
			t.Errorf("verifiers = %v, want %v", dec.Verifiers, want)
		}
		if len(dec.Voices) != 2 {
			t.Fatalf("voices = %d, want 2", len(dec.Voices))
		}
		principal := dec.Voices[0].Dimensions[0]
		if principal.Dimension != DimensionPrincipal || !slices.Equal(principal.Values, []string{"principal-P1"}) {
			t.Errorf("first voice principal = %+v", principal)
		}
	})
	t.Run("merged voice lists every value that joined it", func(t *testing.T) {
		in := mk(OutputMechanical,
			&Policy{Level: LevelRA2, Threshold: 1, DistinctDimensions: []Dimension{DimensionPrincipal, DimensionOperator}},
			reproduced("verifier-1", "principal-P1", "operator-O1"),
			reproduced("verifier-2", "principal-P1", "operator-O2"))
		dec, err := in.decide()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dec.Voices) != 1 {
			t.Fatalf("voices = %d, want 1", len(dec.Voices))
		}
		if got := dec.Voices[0].Dimensions[1].Values; !slices.Equal(got, []string{"operator-O1", "operator-O2"}) {
			t.Errorf("merged operator values = %v", got)
		}
	})
	t.Run("excluded verifier is absent from the verifier list", func(t *testing.T) {
		in := mk(OutputMechanical,
			&Policy{Level: LevelRA2, Threshold: 1, DistinctDimensions: []Dimension{DimensionPrincipal}},
			reproduced("verifier-0", "principal-P0", "operator-O9"),
			reproduced("verifier-1", "principal-P1", "operator-O1"))
		dec, err := in.decide()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := []string{"verifier-1"}; !slices.Equal(dec.Verifiers, want) {
			t.Errorf("verifiers = %v, want %v", dec.Verifiers, want)
		}
		want := []Exclusion{{VerifierID: "verifier-0", Reason: "same principal as the producing worker"}}
		if !reflect.DeepEqual(dec.Excluded, want) {
			t.Errorf("excluded = %+v, want %+v", dec.Excluded, want)
		}
	})
	t.Run("held decision names the disagreeing verifier and its digest", func(t *testing.T) {
		in := mk(OutputMechanical,
			&Policy{Level: LevelRA2, Threshold: 2, DistinctDimensions: []Dimension{DimensionPrincipal}},
			reproduced("verifier-1", "principal-P1", "operator-O1"),
			reproduced("verifier-2", "principal-P2", "operator-O2"),
			differs("verifier-3", "principal-P3", "operator-O3"))
		dec, err := in.decide()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Observation{{VerifierID: "verifier-3", Check: CheckDiffers, ObservedDigest: differentDigest}}
		if !reflect.DeepEqual(dec.Dissent, want) {
			t.Errorf("dissent = %+v, want %+v", dec.Dissent, want)
		}
	})
}

// malformedInputs are each rejected as invalid input. Each mutates one field of
// an otherwise valid input.
func malformedInputs() []struct {
	name   string
	mutate func(in *input)
} {
	return []struct {
		name   string
		mutate func(in *input)
	}{
		{"nil policy", func(in *input) { in.policy = nil }},
		{"empty policy", func(in *input) { in.policy = &Policy{} }},
		{"RA-0 is not a requestable level", func(in *input) { in.policy.Level = LevelRA0 }},
		{"unknown requested level", func(in *input) { in.policy.Level = "RA-9" }},
		{"zero threshold", func(in *input) { in.policy.Threshold = 0 }},
		{"threshold above the limit", func(in *input) { in.policy.Threshold = maxThreshold + 1 }},
		{"no distinct dimension", func(in *input) { in.policy.DistinctDimensions = nil }},
		{"unknown policy dimension", func(in *input) {
			in.policy.DistinctDimensions = append(in.policy.DistinctDimensions, Dimension(99))
		}},
		{"repeated policy dimension", func(in *input) {
			in.policy.DistinctDimensions = append(in.policy.DistinctDimensions, DimensionPrincipal)
		}},
		{"empty reject authority", func(in *input) { in.policy.RejectAuthorities = []string{""} }},
		{"repeated reject authority", func(in *input) { in.policy.RejectAuthorities = []string{"validator-A", "validator-A"} }},
		{"unknown output class", func(in *input) { in.output = 0 }},
		{"semantically open output at RA-2", func(in *input) { in.output = OutputSemanticallyOpen }},
		{"uppercase claim digest", func(in *input) { in.claim.OutputDigest = strings.ToUpper(in.claim.OutputDigest) }},
		{"claim digest that is not hex", func(in *input) { in.claim.OutputDigest = "claimed-output" }},
		{"empty claim task id", func(in *input) { in.claim.TaskID = "" }},
		{"claim without principal", func(in *input) { delete(in.claim.Dimensions, DimensionPrincipal) }},
		{"claim without executor digest", func(in *input) { delete(in.claim.Dimensions, DimensionExecutorDigest) }},
		{"claim without a distinct dimension", func(in *input) { delete(in.claim.Dimensions, DimensionOperator) }},
		{"claim with unknown dimension", func(in *input) { in.claim.Dimensions[Dimension(42)] = "x" }},
		{"claim with a placeholder value", func(in *input) { in.claim.Dimensions[DimensionOperator] = "Unknown" }},
		{"claim with padded value", func(in *input) { in.claim.Dimensions[DimensionPrincipal] = "principal-P0 " }},
		{"claim executor digest that is not a digest", func(in *input) { in.claim.Dimensions[DimensionExecutorDigest] = "executor-E0" }},
		{"verification without principal", func(in *input) { delete(in.verifs[0].Dimensions, DimensionPrincipal) }},
		{"verification without executor digest", func(in *input) { delete(in.verifs[0].Dimensions, DimensionExecutorDigest) }},
		{"verification without a distinct dimension", func(in *input) { delete(in.verifs[0].Dimensions, DimensionOperator) }},
		{"verification with unknown dimension", func(in *input) { in.verifs[0].Dimensions[Dimension(42)] = "x" }},
		{"verification with empty value", func(in *input) { in.verifs[0].Dimensions[DimensionOperator] = "" }},
		{"verification bound to another attempt", func(in *input) { in.verifs[0].AttemptID = "attempt-2" }},
		{"verification without verifier identity", func(in *input) { in.verifs[0].VerifierID = "" }},
		{"verification with unknown check", func(in *input) { in.verifs[0].Check = 0 }},
		{"verification with out-of-range check", func(in *input) { in.verifs[0].Check = 9 }},
		{"verification observed digest that is not hex", func(in *input) { in.verifs[0].ObservedDigest = "not-a-digest" }},
		{"difference reported for the claimed digest", func(in *input) { in.verifs[0].Check = CheckDiffers }},
		{"reproduction of a different digest", func(in *input) { in.verifs[0].ObservedDigest = differentDigest }},
		{"conflicting records for one verifier", func(in *input) {
			in.verifs = append(in.verifs, differs("verifier-1", "principal-P1", "operator-O1"))
		}},
		{"verifications above the limit", func(in *input) {
			in.verifs = nil
			for i := range maxVerifications + 1 {
				in.verifs = append(in.verifs, reproduced(fmt.Sprintf("v-%d", i), fmt.Sprintf("principal-%d", i), "operator-x"))
			}
		}},
	}
}

func TestDecideRejectsMalformedInput(t *testing.T) {
	if _, err := validInput().decide(); err != nil {
		t.Fatalf("valid input is rejected: %v", err)
	}
	for _, tc := range malformedInputs() {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.mutate(&in)
			dec, err := in.decide()
			if !errors.Is(err, ErrRejectedInput) {
				t.Fatalf("err = %v, want ErrRejectedInput", err)
			}
			if !reflect.DeepEqual(dec, Decision{}) {
				t.Errorf("decision returned alongside an error: %+v", dec)
			}
		})
	}
}

// permutations returns every ordering of the indices 0..n-1.
func permutations(n int) [][]int {
	var out [][]int
	var walk func(prefix, rest []int)
	walk = func(prefix, rest []int) {
		if len(rest) == 0 {
			out = append(out, slices.Clone(prefix))
			return
		}
		for i := range rest {
			walk(append(prefix, rest[i]), slices.Concat(rest[:i], rest[i+1:]))
		}
	}
	base := make([]int, n)
	for i := range base {
		base[i] = i
	}
	walk(nil, base)
	return out
}

func TestDecisionIsOrderIndependent(t *testing.T) {
	policy := func() *Policy {
		return &Policy{Level: LevelRA2, Threshold: 2, DistinctDimensions: []Dimension{DimensionPrincipal, DimensionOperator}}
	}
	mixed := []Verification{
		reproduced("verifier-1", "principal-P1", "operator-O1"),
		reproduced("verifier-2", "principal-P2", "operator-O2"),
		reproduced("verifier-3", "principal-P3", "operator-O1"),
		reproduced("verifier-4", "principal-P4", "operator-O4"),
		reproduced("verifier-0", "principal-P0", "operator-O9"),
	}
	withDissent := append(slices.Clone(mixed), differs("verifier-5", "principal-P5", "operator-O5"))
	inputs := []struct {
		name   string
		verifs []Verification
	}{
		{"accepted", mixed},
		{"held by dissent", withDissent},
	}
	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			want, err := Decide(baseClaim(), tc.verifs, OutputMechanical, policy())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, perm := range permutations(len(tc.verifs)) {
				shuffled := make([]Verification, len(tc.verifs))
				for i, j := range perm {
					shuffled[i] = tc.verifs[j]
				}
				got, err := Decide(baseClaim(), shuffled, OutputMechanical, policy())
				if err != nil {
					t.Fatalf("permutation %v: unexpected error: %v", perm, err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("permutation %v changed the decision:\n got %+v\nwant %+v", perm, got, want)
				}
			}
		})
	}
}

func TestRepeatedVerificationDoesNotChangeDecision(t *testing.T) {
	for _, in := range []input{validInput(), mk(OutputMechanical,
		&Policy{Level: LevelRA2, Threshold: 2, DistinctDimensions: []Dimension{DimensionPrincipal}},
		reproduced("verifier-1", "principal-P1", "operator-O1"),
		reproduced("verifier-2", "principal-P2", "operator-O2"),
		differs("verifier-3", "principal-P3", "operator-O3"))} {
		want, err := in.decide()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		repeated := in
		repeated.verifs = slices.Concat(in.verifs, in.verifs[:1], in.verifs[len(in.verifs)-1:])
		got, err := repeated.decide()
		if err != nil {
			t.Fatalf("repeated input: unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("repeated verification changed the decision:\n got %+v\nwant %+v", got, want)
		}
	}
}

func TestPolicyDigestIsCanonical(t *testing.T) {
	digestFor := func(p *Policy) contract.Digest {
		t.Helper()
		in := validInput()
		in.policy = p
		dec, err := in.decide()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return dec.PolicyDigest
	}
	ordered := &Policy{
		Level:              LevelRA2,
		Threshold:          2,
		DistinctDimensions: []Dimension{DimensionPrincipal, DimensionOperator},
		RejectAuthorities:  []string{"validator-A", "validator-B"},
	}
	reordered := &Policy{
		Level:              LevelRA2,
		Threshold:          2,
		DistinctDimensions: []Dimension{DimensionOperator, DimensionPrincipal},
		RejectAuthorities:  []string{"validator-B", "validator-A"},
	}
	want := digestFor(ordered)
	if want.IsZero() {
		t.Fatal("policy digest is zero")
	}
	if got := digestFor(reordered); got != want {
		t.Errorf("reordered policy digest = %s, want %s", got, want)
	}
	changes := []struct {
		name   string
		policy *Policy
	}{
		{"threshold", &Policy{Level: LevelRA2, Threshold: 3, DistinctDimensions: []Dimension{DimensionPrincipal, DimensionOperator}, RejectAuthorities: []string{"validator-A", "validator-B"}}},
		{"level", &Policy{Level: LevelRA1, Threshold: 2, DistinctDimensions: []Dimension{DimensionPrincipal, DimensionOperator}, RejectAuthorities: []string{"validator-A", "validator-B"}}},
		{"distinct dimensions", &Policy{Level: LevelRA2, Threshold: 2, DistinctDimensions: []Dimension{DimensionPrincipal}, RejectAuthorities: []string{"validator-A", "validator-B"}}},
		{"reject authorities", &Policy{Level: LevelRA2, Threshold: 2, DistinctDimensions: []Dimension{DimensionPrincipal, DimensionOperator}, RejectAuthorities: []string{"validator-A"}}},
	}
	for _, tc := range changes {
		if got := digestFor(tc.policy); got == want {
			t.Errorf("changing %s did not change the policy digest", tc.name)
		}
	}
}

func TestDecisionHasNoBooleanVerdict(t *testing.T) {
	rt := reflect.TypeOf(Decision{})
	for i := range rt.NumField() {
		if f := rt.Field(i); f.Type.Kind() == reflect.Bool {
			t.Errorf("Decision.%s is a boolean verdict; the detail must be carried instead", f.Name)
		}
	}
}
