// Package rejectedresult defines the durable record that Result Acceptance
// (RAC-1) rejected a Worker's ResultClaim.
//
// A rejection closes the Attempt as result_rejected. Without a record, the only
// trace left is the close reason, and the evidence chain of #57 cannot say who
// rejected the result, which verifications disagreed with the claim, or under
// which policy. The record keeps what resultaccept.Decide reported: the claim
// and output digests, the policy digest, the reason, every dissenting
// verification with the digest it observed, the verifiers that were eligible,
// and the verifiers that were excluded and why. It is evidence of a decision,
// not a proof that the output is wrong.
package rejectedresult

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
)

var ErrInvalidRecord = errors.New("invalid rejected result record")

const (
	maxEntries = 1024
	maxText    = 512
)

// Dissent is one verification that disagreed with the claim.
type Dissent struct {
	VerifierID string
	Check      resultaccept.Check
	// ObservedDigest is the lowercase hexadecimal digest the verifier reported.
	ObservedDigest string
}

// Exclusion is one verification that could not justify the claim, and why.
type Exclusion struct {
	VerifierID string
	Reason     string
}

// Config is the content of one rejection.
type Config struct {
	TaskID    ids.TaskID
	AttemptID ids.AttemptID
	// ClaimDigest is resultclaim.Claim.Digest of the rejected claim.
	ClaimDigest contract.Digest
	// OutputDigest is the output the claim reported.
	OutputDigest contract.Digest
	// PolicyDigest identifies the policy the decision was made under.
	PolicyDigest contract.Digest
	// Reason is resultaccept.Decision.Reason. Only an authority rejection can
	// reject a claim today.
	Reason resultaccept.Reason
	// Dissent lists every verification that disagreed. A rejection always has
	// at least one.
	Dissent []Dissent
	// Verifiers are the eligible verifier identities.
	Verifiers []string
	// Excluded are the verifications that justified nothing.
	Excluded   []Exclusion
	RejectedAt time.Time
}

// Record is an immutable rejection.
type Record struct {
	taskID       ids.TaskID
	attemptID    ids.AttemptID
	claimDigest  contract.Digest
	outputDigest contract.Digest
	policyDigest contract.Digest
	reason       resultaccept.Reason
	dissent      []Dissent
	verifiers    []string
	excluded     []Exclusion
	rejectedAt   time.Time
}

// New validates cfg and returns the record. Lists are sorted by verifier
// identity and must not repeat a verifier, so two records of the same decision
// compare equal.
func New(cfg Config) (Record, error) {
	switch {
	case cfg.TaskID == "":
		return Record{}, fmt.Errorf("%w: TaskID is required", ErrInvalidRecord)
	case cfg.AttemptID == "":
		return Record{}, fmt.Errorf("%w: AttemptID is required", ErrInvalidRecord)
	case cfg.ClaimDigest.IsZero():
		return Record{}, fmt.Errorf("%w: claim digest is required", ErrInvalidRecord)
	case cfg.OutputDigest.IsZero():
		return Record{}, fmt.Errorf("%w: output digest is required", ErrInvalidRecord)
	case cfg.PolicyDigest.IsZero():
		return Record{}, fmt.Errorf("%w: policy digest is required", ErrInvalidRecord)
	case cfg.Reason != resultaccept.ReasonAuthorityRejected:
		return Record{}, fmt.Errorf("%w: reason %q does not reject a claim", ErrInvalidRecord, cfg.Reason)
	case len(cfg.Dissent) == 0:
		return Record{}, fmt.Errorf("%w: a rejection needs at least one dissenting verification", ErrInvalidRecord)
	case len(cfg.Dissent) > maxEntries || len(cfg.Verifiers) > maxEntries || len(cfg.Excluded) > maxEntries:
		return Record{}, fmt.Errorf("%w: more than %d entries", ErrInvalidRecord, maxEntries)
	case cfg.RejectedAt.IsZero():
		return Record{}, fmt.Errorf("%w: rejection time is required", ErrInvalidRecord)
	}

	dissent := slices.Clone(cfg.Dissent)
	slices.SortFunc(dissent, func(a, b Dissent) int { return strings.Compare(a.VerifierID, b.VerifierID) })
	for i, d := range dissent {
		if !validText(d.VerifierID) {
			return Record{}, fmt.Errorf("%w: dissenting verifier %q is invalid", ErrInvalidRecord, d.VerifierID)
		}
		if i > 0 && dissent[i-1].VerifierID == d.VerifierID {
			return Record{}, fmt.Errorf("%w: verifier %q dissents twice", ErrInvalidRecord, d.VerifierID)
		}
		if d.Check != resultaccept.CheckDiffers && d.Check != resultaccept.CheckValidatorRejected {
			return Record{}, fmt.Errorf("%w: verifier %q check %s is not a dissent", ErrInvalidRecord, d.VerifierID, d.Check)
		}
		if parsed, err := contract.ParseSHA256(d.ObservedDigest); err != nil || parsed.Hex() != d.ObservedDigest {
			return Record{}, fmt.Errorf("%w: verifier %q observed digest is not canonical", ErrInvalidRecord, d.VerifierID)
		}
	}
	verifiers := slices.Clone(cfg.Verifiers)
	slices.Sort(verifiers)
	for i, v := range verifiers {
		if !validText(v) || (i > 0 && verifiers[i-1] == v) {
			return Record{}, fmt.Errorf("%w: verifier %q is invalid or repeated", ErrInvalidRecord, v)
		}
	}
	excluded := slices.Clone(cfg.Excluded)
	slices.SortFunc(excluded, func(a, b Exclusion) int { return strings.Compare(a.VerifierID, b.VerifierID) })
	for i, e := range excluded {
		if !validText(e.VerifierID) || !validText(e.Reason) || (i > 0 && excluded[i-1].VerifierID == e.VerifierID) {
			return Record{}, fmt.Errorf("%w: exclusion of %q is invalid or repeated", ErrInvalidRecord, e.VerifierID)
		}
	}
	return Record{
		taskID: cfg.TaskID, attemptID: cfg.AttemptID,
		claimDigest: cfg.ClaimDigest, outputDigest: cfg.OutputDigest, policyDigest: cfg.PolicyDigest,
		reason: cfg.Reason, dissent: dissent, verifiers: verifiers, excluded: excluded,
		rejectedAt: cfg.RejectedAt.UTC(),
	}, nil
}

// FromDecision builds the record of a rejected decision.
func FromDecision(taskID ids.TaskID, attemptID ids.AttemptID, claimDigest, outputDigest contract.Digest,
	decision resultaccept.Decision, at time.Time) (Record, error) {
	if decision.Outcome != resultaccept.OutcomeRejected {
		return Record{}, fmt.Errorf("%w: decision outcome is %q", ErrInvalidRecord, decision.Outcome)
	}
	cfg := Config{
		TaskID: taskID, AttemptID: attemptID, ClaimDigest: claimDigest, OutputDigest: outputDigest,
		PolicyDigest: decision.PolicyDigest, Reason: decision.Reason,
		Verifiers: decision.Verifiers, RejectedAt: at,
	}
	for _, d := range decision.Dissent {
		cfg.Dissent = append(cfg.Dissent, Dissent{VerifierID: d.VerifierID, Check: d.Check, ObservedDigest: d.ObservedDigest})
	}
	for _, e := range decision.Excluded {
		cfg.Excluded = append(cfg.Excluded, Exclusion{VerifierID: e.VerifierID, Reason: e.Reason})
	}
	return New(cfg)
}

func validText(s string) bool { return s != "" && len(s) <= maxText && utf8.ValidString(s) }

func (r Record) TaskID() ids.TaskID            { return r.taskID }
func (r Record) AttemptID() ids.AttemptID      { return r.attemptID }
func (r Record) ClaimDigest() contract.Digest  { return r.claimDigest }
func (r Record) OutputDigest() contract.Digest { return r.outputDigest }
func (r Record) PolicyDigest() contract.Digest { return r.policyDigest }
func (r Record) Reason() resultaccept.Reason   { return r.reason }
func (r Record) Dissent() []Dissent            { return slices.Clone(r.dissent) }
func (r Record) Verifiers() []string           { return slices.Clone(r.verifiers) }
func (r Record) Excluded() []Exclusion         { return slices.Clone(r.excluded) }
func (r Record) RejectedAt() time.Time         { return r.rejectedAt }
func (r Record) Valid() bool                   { return r.taskID != "" && r.attemptID != "" && !r.claimDigest.IsZero() }

// Equal reports whether two records describe the same decision.
func (r Record) Equal(other Record) bool {
	return r.taskID == other.taskID && r.attemptID == other.attemptID &&
		r.claimDigest == other.claimDigest && r.outputDigest == other.outputDigest &&
		r.policyDigest == other.policyDigest && r.reason == other.reason &&
		slices.Equal(r.dissent, other.dissent) && slices.Equal(r.verifiers, other.verifiers) &&
		slices.Equal(r.excluded, other.excluded) && r.rejectedAt.Equal(other.rejectedAt)
}
