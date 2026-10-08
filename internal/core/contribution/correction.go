package contribution

import (
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// CorrectionKind says how a record has been marked after the fact.
type CorrectionKind string

const (
	// CorrectionRevoked marks a record whose accepted result was later rejected
	// or withdrawn. The record stays in place and is not deleted.
	CorrectionRevoked CorrectionKind = "revoked"
	// CorrectionSuperseded marks a record replaced by a corrected one for the
	// same work.
	CorrectionSuperseded CorrectionKind = "superseded"
)

// Correction is a marker that points to an existing record by its digest. It
// never alters or removes the record. Its fields are unexported, so a valid
// Correction comes only from Revoke or Supersede. The zero value is rejected by
// Digest.
type Correction struct {
	kind         CorrectionKind
	target       contract.Digest
	successor    contract.Digest
	taskDigest   contract.Digest
	resultDigest contract.Digest
}

// Revoke marks original as revoked. The marker carries the original's digest and
// work binding.
func Revoke(original Record) (Correction, error) {
	target, err := original.Digest()
	if err != nil {
		return Correction{}, err
	}
	c := Correction{
		kind:         CorrectionRevoked,
		target:       target,
		taskDigest:   original.taskDigest,
		resultDigest: original.resultDigest,
	}
	if err := c.validate(); err != nil {
		return Correction{}, err
	}
	return c, nil
}

// Supersede marks original as superseded by successor. The two records must
// describe the same work, meaning the same provider, resource class, task, and
// result. The metric, quantity, and assurance may differ, since correcting them
// is the reason for superseding.
func Supersede(original, successor Record) (Correction, error) {
	target, err := original.Digest()
	if err != nil {
		return Correction{}, err
	}
	next, err := successor.Digest()
	if err != nil {
		return Correction{}, err
	}
	if target == next {
		return Correction{}, fmt.Errorf("%w: record cannot supersede itself", ErrInvalidCorrection)
	}
	if original.providerID != successor.providerID ||
		original.resourceClass != successor.resourceClass ||
		original.taskDigest != successor.taskDigest ||
		original.resultDigest != successor.resultDigest {
		return Correction{}, fmt.Errorf("%w: successor describes different work", ErrInvalidCorrection)
	}
	c := Correction{
		kind:         CorrectionSuperseded,
		target:       target,
		successor:    next,
		taskDigest:   original.taskDigest,
		resultDigest: original.resultDigest,
	}
	if err := c.validate(); err != nil {
		return Correction{}, err
	}
	return c, nil
}

// Kind returns how the record was marked.
func (c Correction) Kind() CorrectionKind { return c.kind }

// Target returns the digest of the record being marked.
func (c Correction) Target() contract.Digest { return c.target }

// Successor returns the digest of the replacing record. It is zero for a
// revocation.
func (c Correction) Successor() contract.Digest { return c.successor }

// TaskDigest returns the task digest of the marked record.
func (c Correction) TaskDigest() contract.Digest { return c.taskDigest }

// ResultDigest returns the accepted-result digest of the marked record.
func (c Correction) ResultDigest() contract.Digest { return c.resultDigest }

// Digest returns the content digest of the correction marker.
func (c Correction) Digest() (contract.Digest, error) {
	if err := c.validate(); err != nil {
		return contract.Digest{}, err
	}
	return contract.SHA256(c.canonical()), nil
}

func (c Correction) validate() error {
	switch c.kind {
	case CorrectionRevoked:
		if !c.successor.IsZero() {
			return fmt.Errorf("%w: revocation names a successor", ErrInvalidCorrection)
		}
	case CorrectionSuperseded:
		if c.successor.IsZero() || c.successor == c.target {
			return fmt.Errorf("%w: supersession needs a distinct successor", ErrInvalidCorrection)
		}
	default:
		return fmt.Errorf("%w: unknown kind", ErrInvalidCorrection)
	}
	if c.target.IsZero() || c.taskDigest.IsZero() || c.resultDigest.IsZero() {
		return fmt.Errorf("%w: incomplete reference", ErrInvalidCorrection)
	}
	return nil
}
