// Package resultaccept decides whether a Worker's ResultClaim may be promoted to
// an AcceptedResult (see RVR-1 and RAC-1 in architecture/AUTHORITY_BOUNDARIES.md).
//
// The package is a pure function. It performs no I/O, reads no clock, and starts
// no goroutines. A claim is accepted only when verifications from enough
// independent voices agree with it under a Policy that comes from the trusted
// boundary. Two verifications that share a value on a dimension the Policy
// requires to differ count as one voice. Verifications that share a principal or
// executor with the producing Worker justify nothing.
//
// Where the inputs leave room, the package chooses the conservative reading.
// Any disagreement holds the decision, and only a policy-named authority can turn
// a verification into a rejection. Dissent and authority rejection still count
// when they come from an excluded verifier, because exclusion only stops a
// verifier from justifying a result. A byte-identical repeat of a verification is
// collapsed, because repeated evidence cannot add a voice, while conflicting
// records under one verifier identity are refused as invalid input. A
// semantically open output may reach RA-1 but is never reported as RA-2, and
// asking for RA-2 for it is refused as invalid input.
package resultaccept

import (
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ErrRejectedInput wraps every error returned by Decide. It means the input
// could not be evaluated safely. It is never an acceptance.
var ErrRejectedInput = errors.New("resultaccept: input rejected")

// Dimension is an axis of independence (ASSURANCE_MODEL Axis E). Values are
// compared byte for byte, so the trusted boundary must canonicalize them.
type Dimension uint8

const (
	DimensionPrincipal Dimension = iota + 1
	DimensionOperator
	DimensionExecutorDigest
	DimensionModelLineage
	DimensionHardwareClass
	DimensionNetworkRegion
	DimensionVerifierIdentity
)

func (d Dimension) valid() bool {
	return d >= DimensionPrincipal && d <= DimensionVerifierIdentity
}

func (d Dimension) String() string {
	switch d {
	case DimensionPrincipal:
		return "principal"
	case DimensionOperator:
		return "operator"
	case DimensionExecutorDigest:
		return "executor-digest"
	case DimensionModelLineage:
		return "model-lineage"
	case DimensionHardwareClass:
		return "hardware-class"
	case DimensionNetworkRegion:
		return "network-region"
	case DimensionVerifierIdentity:
		return "verifier-identity"
	}
	return fmt.Sprintf("dimension(%d)", uint8(d))
}

// OutputClass is the output property declared by the admitted Task contract. It
// is never read from a claim or a Worker.
type OutputClass uint8

const (
	// OutputMechanical is a deterministic output with a mechanical check.
	OutputMechanical OutputClass = iota + 1
	// OutputSemanticallyOpen is a non-deterministic output, or an output with no
	// mechanical check. Semantic correctness is not established for it.
	OutputSemanticallyOpen
)

// AssuranceLevel names a Result Assurance Profile (ASSURANCE_MODEL RA-0..RA-5).
// Only the levels this package can justify from verification evidence are
// declared. RA-3, RA-4 and RA-5 need recomputation, attestation or proof
// evidence, which is not an input here.
type AssuranceLevel string

const (
	// LevelRA0 means no assurance level beyond best effort was established. Every
	// decision that is not accepted reports this level.
	LevelRA0 AssuranceLevel = "RA-0"
	// LevelRA1 means an application validator accepted the output, checked by
	// independent voices.
	LevelRA1 AssuranceLevel = "RA-1"
	// LevelRA2 means independent voices re-derived the same output digest.
	LevelRA2 AssuranceLevel = "RA-2"
)

// Check is what one verification established about the claimed output.
type Check uint8

const (
	// CheckReproduced means an independent re-execution produced the claimed digest.
	CheckReproduced Check = iota + 1
	// CheckDiffers means an independent re-execution produced a different digest.
	CheckDiffers
	// CheckValidatorAccepted means an application-specific validator accepted the
	// claimed output.
	CheckValidatorAccepted
	// CheckValidatorRejected means an application-specific validator rejected the
	// claimed output.
	CheckValidatorRejected
)

func (c Check) valid() bool {
	return c >= CheckReproduced && c <= CheckValidatorRejected
}

func (c Check) String() string {
	switch c {
	case CheckReproduced:
		return "reproduced"
	case CheckDiffers:
		return "differs"
	case CheckValidatorAccepted:
		return "validator-accepted"
	case CheckValidatorRejected:
		return "validator-rejected"
	}
	return fmt.Sprintf("check(%d)", uint8(c))
}

// Outcome is the verdict of a decision.
type Outcome string

const (
	OutcomeAccepted Outcome = "accepted"
	// OutcomeHeld means the evidence is insufficient or disagrees. It is the
	// outcome for every shortfall, including no verification at all.
	OutcomeHeld Outcome = "held"
	// OutcomeRejected means a policy-named authority explicitly rejected the output.
	OutcomeRejected Outcome = "rejected"
)

// Reason explains an Outcome.
type Reason string

const (
	ReasonQuorumMet                 Reason = "quorum-met"
	ReasonNoIndependentVerification Reason = "no-independent-verification"
	ReasonQuorumNotMet              Reason = "quorum-not-met"
	ReasonVerificationsDisagree     Reason = "verifications-disagree"
	ReasonAuthorityRejected         Reason = "authority-rejected"
)

// Limits that every decision states. They say what acceptance does not establish
// (RQ-024, RQ-045, RQ-046, ASSURANCE_MODEL Axis G and Axis H).
const (
	NotGuaranteedInputConfidentiality = "confidentiality of task input against the producing worker"
	NotGuaranteedDeletion             = "deletion of task input or output copies held by the producing worker after disclosure"
	NotGuaranteedResourceUsage        = "resource usage reported by the producing worker"
	NotGuaranteedSideEffects          = "external side effects performed during execution"
	NotGuaranteedVerifierIntegrity    = "integrity of verifiers beyond the independence dimensions declared by the trusted boundary"
	NotGuaranteedUndeclaredCollusion  = "independence not shown by the declared dimensions, including collusion"
	// NotGuaranteedMechanicalCheckLimit applies to mechanical outputs.
	NotGuaranteedMechanicalCheckLimit = "correctness beyond the mechanical check named by the task contract"
	// NotGuaranteedSemanticCorrectness applies to semantically open outputs.
	NotGuaranteedSemanticCorrectness = "semantic correctness of the output is not established"
)

// baseNotGuaranteed is listed in every decision, in this order.
var baseNotGuaranteed = []string{
	NotGuaranteedInputConfidentiality,
	NotGuaranteedDeletion,
	NotGuaranteedResourceUsage,
	NotGuaranteedSideEffects,
	NotGuaranteedVerifierIntegrity,
	NotGuaranteedUndeclaredCollusion,
}

// Policy is the quorum requirement. It comes from the trusted boundary, never from
// a claim or a Worker. Decide reports its digest.
type Policy struct {
	// Level is the assurance level the Task asks for. It must be LevelRA1 or LevelRA2.
	Level AssuranceLevel
	// Threshold is the number of independent voices that must agree. It is 1..1024.
	Threshold int
	// DistinctDimensions lists the dimensions on which two verifications must differ
	// to count as separate voices. It must not be empty, so a bare count of verifiers
	// never justifies a result.
	DistinctDimensions []Dimension
	// RejectAuthorities lists verifier identities whose explicit validator rejection
	// is final under this policy.
	RejectAuthorities []string
}

// ResultClaim is the Worker's report. Its dimension values must come from the
// trusted boundary's identity records, not from the Worker's own words.
type ResultClaim struct {
	TaskID    string
	AttemptID string
	// WorkerID is the producing Worker identity. It is never a verifier.
	WorkerID string
	// OutputDigest is the lowercase hexadecimal SHA-256 of the claimed output.
	OutputDigest string
	// Dimensions are the producer's values. Principal and executor digest are
	// required, as is every distinct dimension of the policy.
	Dimensions map[Dimension]string
}

// Verification is one verifier's evidence about the claimed output. VerifierID is
// the verifier identity. Each verifier must appear with one record.
type Verification struct {
	VerifierID string
	TaskID     string
	AttemptID  string
	Check      Check
	// ObservedDigest is the output digest the verifier re-derived or validated.
	// It must equal the claimed digest, except for CheckDiffers, which requires a
	// different digest.
	ObservedDigest string
	// Dimensions are the verifier's values. Principal and executor digest are
	// required, as is every distinct dimension of the policy.
	Dimensions map[Dimension]string
}

// Voice is one independent voice: a group of verifications joined by a shared
// value on a distinct dimension. Its members carry the check the requested level
// needs.
type Voice struct {
	VerifierIDs []string
	// Dimensions holds, for each distinct dimension, the sorted values the members
	// present. Values that differ across members are how the voice was merged.
	Dimensions []DimensionValues
	Checks     []Check
}

// DimensionValues lists the values of one dimension, sorted and without repeats.
type DimensionValues struct {
	Dimension Dimension
	Values    []string
}

// Observation is one verification that disagrees with the claimed output.
type Observation struct {
	VerifierID     string
	Check          Check
	ObservedDigest string
}

// Exclusion records a verification that justifies nothing, and why.
type Exclusion struct {
	VerifierID string
	Reason     string
}

// Decision is the auditable result of Decide. It has no boolean verdict. Outcome,
// Level, Reason and the evidence fields carry the detail.
type Decision struct {
	Outcome Outcome
	// Level is the assurance level established. It is LevelRA0 unless Outcome is
	// OutcomeAccepted.
	Level  AssuranceLevel
	Reason Reason
	// PolicyDigest identifies the policy that was applied.
	PolicyDigest contract.Digest
	// Voices are the independent voices that carry the check the requested level
	// needs, in order of their first verifier identity.
	Voices []Voice
	// Verifiers are the verifier identities that were eligible to justify the
	// result, sorted.
	Verifiers []string
	// Dissent lists every verification that disagrees, whether or not it was
	// eligible.
	Dissent []Observation
	// Excluded lists the verifications that justify nothing.
	Excluded []Exclusion
	// NotGuaranteed lists what acceptance does not establish. It is never empty.
	NotGuaranteed []string
}
