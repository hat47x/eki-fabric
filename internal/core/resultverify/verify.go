// Package resultverify normalizes what a verifier observed into the
// Verification that Result Acceptance evaluates (RVR-1 in
// architecture/AUTHORITY_BOUNDARIES.md).
//
// A verifier reports what it saw: the digest of the output it re-derived, or of
// the bytes it validated, or of the finalized Artifact it read. It does not
// decide how its observation relates to the Worker's claim. This package makes
// that comparison against the completed ResultClaim stored by the Control
// Plane, so a verifier cannot mark a mismatching output as reproduced, and a
// Worker cannot influence the result by what it claims about the check.
//
// The package is a pure function. It performs no I/O and reads no clock.
// Where the comparison leaves room, it chooses the reading that cannot lead to
// an acceptance.
package resultverify

import (
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var (
	// ErrInvalidObservation means the observation is malformed. It is refused,
	// never counted.
	ErrInvalidObservation = errors.New("invalid verifier observation")
	// ErrBindingMismatch means the observation is about another Task, Attempt,
	// or TaskIR than the claim. It is refused, never counted: evidence about
	// other work says nothing about this claim.
	ErrBindingMismatch = errors.New("verifier observation is not bound to the claim")
	// ErrNotCompleted means the claim has no output to verify.
	ErrNotCompleted = errors.New("only a completed claim can be verified")
)

// Method is how the verifier examined the output.
type Method string

const (
	// MethodReexecution: the verifier ran the same TaskIR again and reports the
	// digest of the output it produced.
	MethodReexecution Method = "reexecution"
	// MethodValidator: an application validator examined output bytes and
	// reports their digest and its verdict on them.
	MethodValidator Method = "validator"
	// MethodArtifactIntegrity: the trusted Artifact boundary read the finalized
	// output the claim refers to and reports the digest of the stored bytes.
	MethodArtifactIntegrity Method = "artifact-integrity"
)

// Verdict is a validator's verdict on the bytes it examined.
type Verdict string

const (
	VerdictNone     Verdict = ""
	VerdictAccepted Verdict = "accepted"
	VerdictRejected Verdict = "rejected"
)

// Observation is one verifier's report. Dimensions come from the trusted
// identity records of the verifier, not from the verifier's own words.
type Observation struct {
	VerifierID     string
	Method         Method
	TaskID         ids.TaskID
	AttemptID      ids.AttemptID
	TaskIRDigest   contract.Digest
	ObservedDigest contract.Digest
	// Verdict is set only for MethodValidator.
	Verdict    Verdict
	Dimensions map[resultaccept.Dimension]string
}

// Subject is what a completed claim asserts, read from durable state.
type Subject struct {
	TaskID       ids.TaskID
	AttemptID    ids.AttemptID
	TaskIRDigest contract.Digest
	OutputDigest contract.Digest
}

// SubjectOf returns the Subject of a completed claim.
func SubjectOf(claim resultclaim.Claim) (Subject, error) {
	if !claim.Valid() || claim.Status() != resultclaim.StatusCompleted || claim.OutputDigest().IsZero() {
		return Subject{}, ErrNotCompleted
	}
	return Subject{
		TaskID:       claim.TaskID(),
		AttemptID:    claim.AttemptID(),
		TaskIRDigest: claim.TaskIRDigest(),
		OutputDigest: claim.OutputDigest(),
	}, nil
}

// Result is one normalized observation.
type Result struct {
	// Verification is the evidence for Result Acceptance. It is nil when the
	// observation is consistent with the claim but is not evidence that the
	// output is correct (a matching artifact-integrity check).
	Verification *resultaccept.Verification
	// Matches reports whether the observed digest equals the claimed one.
	Matches bool
}

// Normalize compares one observation with the claim.
//
//   - Re-execution: the same digest is reproduced; a different one differs.
//   - Validator: a verdict on the claimed digest is passed on. A validator that
//     examined other bytes is reported as differing, not as a verdict, because
//     its verdict is about an output the Worker did not claim.
//   - Artifact integrity: stored bytes that do not hash to the claimed digest
//     mean the claim misreports its output. This is reported as a validator
//     rejection of the claimed output, which is final when the policy names the
//     integrity verifier as a reject authority (V5). Matching bytes add no
//     voice: they show the claim names the stored bytes, not that the bytes
//     are correct.
//
// 仮決め: the artifact-integrity mapping and the treatment of a validator that
// examined other bytes are provisional. No requirement fixes them yet; both
// are chosen so that a mismatch can never contribute to an acceptance.
func Normalize(subject Subject, obs Observation) (Result, error) {
	if subject.TaskID == "" || subject.AttemptID == "" || subject.TaskIRDigest.IsZero() || subject.OutputDigest.IsZero() {
		return Result{}, fmt.Errorf("%w: claim subject is incomplete", ErrNotCompleted)
	}
	if obs.VerifierID == "" {
		return Result{}, fmt.Errorf("%w: verifier identity is required", ErrInvalidObservation)
	}
	if obs.ObservedDigest.IsZero() {
		return Result{}, fmt.Errorf("%w: verifier %q reported no digest", ErrInvalidObservation, obs.VerifierID)
	}
	if obs.TaskID != subject.TaskID || obs.AttemptID != subject.AttemptID || obs.TaskIRDigest != subject.TaskIRDigest {
		return Result{}, fmt.Errorf("%w: verifier %q", ErrBindingMismatch, obs.VerifierID)
	}
	if obs.Method == MethodValidator {
		if obs.Verdict != VerdictAccepted && obs.Verdict != VerdictRejected {
			return Result{}, fmt.Errorf("%w: validator %q has no verdict", ErrInvalidObservation, obs.VerifierID)
		}
	} else if obs.Verdict != VerdictNone {
		return Result{}, fmt.Errorf("%w: verifier %q is not a validator but reports a verdict", ErrInvalidObservation, obs.VerifierID)
	}

	matches := obs.ObservedDigest == subject.OutputDigest
	v := resultaccept.Verification{
		VerifierID:     obs.VerifierID,
		TaskID:         subject.TaskID.String(),
		AttemptID:      subject.AttemptID.String(),
		ObservedDigest: obs.ObservedDigest.Hex(),
		Dimensions:     cloneDimensions(obs.Dimensions),
	}
	switch obs.Method {
	case MethodReexecution:
		v.Check = resultaccept.CheckReproduced
		if !matches {
			v.Check = resultaccept.CheckDiffers
		}
	case MethodValidator:
		switch {
		case !matches:
			v.Check = resultaccept.CheckDiffers
		case obs.Verdict == VerdictAccepted:
			v.Check = resultaccept.CheckValidatorAccepted
		default:
			v.Check = resultaccept.CheckValidatorRejected
		}
	case MethodArtifactIntegrity:
		if matches {
			return Result{Matches: true}, nil
		}
		v.Check = resultaccept.CheckValidatorRejected
		v.ObservedDigest = subject.OutputDigest.Hex()
	default:
		return Result{}, fmt.Errorf("%w: verifier %q has unknown method %q", ErrInvalidObservation, obs.VerifierID, obs.Method)
	}
	return Result{Verification: &v, Matches: matches}, nil
}

// NormalizeAll normalizes every observation. Any refused observation refuses
// the whole set, so a caller cannot drop inconvenient evidence by mixing it
// with malformed input.
func NormalizeAll(subject Subject, observations []Observation) ([]resultaccept.Verification, error) {
	out := make([]resultaccept.Verification, 0, len(observations))
	for _, obs := range observations {
		r, err := Normalize(subject, obs)
		if err != nil {
			return nil, err
		}
		if r.Verification != nil {
			out = append(out, *r.Verification)
		}
	}
	return out, nil
}

func cloneDimensions(in map[resultaccept.Dimension]string) map[resultaccept.Dimension]string {
	if in == nil {
		return nil
	}
	out := make(map[resultaccept.Dimension]string, len(in))
	for d, value := range in {
		out[d] = value
	}
	return out
}
