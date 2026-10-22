// Package acceptedresult defines the durable record that a Worker's ResultClaim
// was promoted to an AcceptedResult (RAC-1 in architecture/AUTHORITY_BOUNDARIES.md).
//
// The record states what was accepted, how strongly it was checked, and who
// vouched for it: the claim and output digests, the assurance level that
// resultaccept established, the digest of the policy it was decided under, and
// the verifier identities that justified it. It is evidence of a decision, not a
// proof that the output is correct. What an acceptance does not establish is
// listed on resultaccept.Decision.NotGuaranteed and is not repeated here.
package acceptedresult

import (
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
)

var ErrInvalidRecord = errors.New("invalid accepted result record")

const (
	maxVerifiers    = 1024
	maxVerifierName = 256
)

// Config is the content of one accepted result.
type Config struct {
	TaskID    ids.TaskID
	AttemptID ids.AttemptID
	// ClaimDigest is resultclaim.Claim.Digest of the claim that was promoted.
	ClaimDigest contract.Digest
	// OutputDigest is the output the claim reported and the verifiers confirmed.
	OutputDigest contract.Digest
	// Level is the assurance level resultaccept established. Only RA-1 and RA-2
	// can be justified from verification evidence.
	Level resultaccept.AssuranceLevel
	// PolicyDigest identifies the policy the decision was made under.
	PolicyDigest contract.Digest
	// Verifiers are the verifier identities that justified the result.
	Verifiers  []string
	AcceptedAt time.Time
}

// Record is an immutable accepted result.
type Record struct {
	taskID       ids.TaskID
	attemptID    ids.AttemptID
	claimDigest  contract.Digest
	outputDigest contract.Digest
	level        resultaccept.AssuranceLevel
	policyDigest contract.Digest
	verifiers    []string
	acceptedAt   time.Time
}

// New validates cfg and returns the record. Verifiers are sorted and must not
// repeat, so two records of the same decision compare equal.
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
	case cfg.Level != resultaccept.LevelRA1 && cfg.Level != resultaccept.LevelRA2:
		return Record{}, fmt.Errorf("%w: assurance level %q cannot justify an accepted result", ErrInvalidRecord, cfg.Level)
	case cfg.PolicyDigest.IsZero():
		return Record{}, fmt.Errorf("%w: policy digest is required", ErrInvalidRecord)
	case len(cfg.Verifiers) == 0 || len(cfg.Verifiers) > maxVerifiers:
		return Record{}, fmt.Errorf("%w: %d verifiers, want 1..%d", ErrInvalidRecord, len(cfg.Verifiers), maxVerifiers)
	case cfg.AcceptedAt.IsZero():
		return Record{}, fmt.Errorf("%w: acceptance time is required", ErrInvalidRecord)
	}
	verifiers := slices.Clone(cfg.Verifiers)
	slices.Sort(verifiers)
	for i, name := range verifiers {
		if name == "" || len(name) > maxVerifierName || !utf8.ValidString(name) {
			return Record{}, fmt.Errorf("%w: verifier identity %q is invalid", ErrInvalidRecord, name)
		}
		if i > 0 && verifiers[i-1] == name {
			return Record{}, fmt.Errorf("%w: verifier %q is listed twice", ErrInvalidRecord, name)
		}
	}
	return Record{
		taskID: cfg.TaskID, attemptID: cfg.AttemptID,
		claimDigest: cfg.ClaimDigest, outputDigest: cfg.OutputDigest,
		level: cfg.Level, policyDigest: cfg.PolicyDigest,
		verifiers: verifiers, acceptedAt: cfg.AcceptedAt.UTC(),
	}, nil
}

func (r Record) TaskID() ids.TaskID                 { return r.taskID }
func (r Record) AttemptID() ids.AttemptID           { return r.attemptID }
func (r Record) ClaimDigest() contract.Digest       { return r.claimDigest }
func (r Record) OutputDigest() contract.Digest      { return r.outputDigest }
func (r Record) Level() resultaccept.AssuranceLevel { return r.level }
func (r Record) PolicyDigest() contract.Digest      { return r.policyDigest }
func (r Record) Verifiers() []string                { return slices.Clone(r.verifiers) }
func (r Record) AcceptedAt() time.Time              { return r.acceptedAt }
func (r Record) Valid() bool                        { return r.taskID != "" && r.attemptID != "" && !r.claimDigest.IsZero() }

// Equal reports whether two records describe the same decision.
func (r Record) Equal(other Record) bool {
	return r.taskID == other.taskID && r.attemptID == other.attemptID &&
		r.claimDigest == other.claimDigest && r.outputDigest == other.outputDigest &&
		r.level == other.level && r.policyDigest == other.policyDigest &&
		slices.Equal(r.verifiers, other.verifiers) && r.acceptedAt.Equal(other.acceptedAt)
}
