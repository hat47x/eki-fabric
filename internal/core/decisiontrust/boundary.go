package decisiontrust

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

var (
	ErrDecisionRoleMismatch = errors.New("signed-decision kind and authority role mismatch")
	ErrAlgorithmMismatch    = errors.New("signed-decision algorithm does not match key record")
	ErrAuthorityMismatch    = errors.New("signed-decision authority identity mismatch")
	ErrTrustDomainMismatch  = errors.New("signed-decision trust domain mismatch")
)

type SigningRequest struct {
	TrustDomain        string
	AuthorityRole      authority.Role
	AuthorityPrincipal string
	DecisionKind       authority.DecisionKind
	Algorithm          Algorithm
}

func (r SigningRequest) Validate() error {
	if err := authority.ValidateRoleForDecision(r.AuthorityRole, r.DecisionKind); err != nil {
		return fmt.Errorf("%w: %v", ErrDecisionRoleMismatch, err)
	}
	return Scope{
		TrustDomain:        r.TrustDomain,
		AuthorityRole:      r.AuthorityRole,
		AuthorityPrincipal: r.AuthorityPrincipal,
		Algorithm:          r.Algorithm,
	}.Validate()
}

func (r SigningRequest) scope() Scope {
	return Scope{
		TrustDomain:        r.TrustDomain,
		AuthorityRole:      r.AuthorityRole,
		AuthorityPrincipal: r.AuthorityPrincipal,
		Algorithm:          r.Algorithm,
	}
}

type VerificationRequest struct {
	KeyID               KeyID
	TrustDomain         string
	AuthorityRole       authority.Role
	AuthorityPrincipal  string
	DecisionKind        authority.DecisionKind
	ProtectedAlgorithm  Algorithm
	IssuedAt            time.Time
}

func (r VerificationRequest) Validate() error {
	if err := validateText("key ID", string(r.KeyID), 128); err != nil {
		return err
	}
	if err := validateText("trust domain", r.TrustDomain, 256); err != nil {
		return err
	}
	if err := validateText("authority principal", r.AuthorityPrincipal, 256); err != nil {
		return err
	}
	if r.IssuedAt.IsZero() {
		return fmt.Errorf("%w: issued time is required", ErrInvalidKeyRecord)
	}
	switch r.ProtectedAlgorithm {
	case AlgorithmEd25519, AlgorithmES256:
	default:
		return fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidKeyRecord, r.ProtectedAlgorithm)
	}
	if err := authority.ValidateRoleForDecision(r.AuthorityRole, r.DecisionKind); err != nil {
		return fmt.Errorf("%w: %v", ErrDecisionRoleMismatch, err)
	}
	return nil
}

func (r *Registry) ResolveSigningKey(req SigningRequest, now time.Time) (KeyRecord, error) {
	if err := req.Validate(); err != nil {
		return KeyRecord{}, err
	}
	return r.ResolveForSigning(req.scope(), now)
}

func (r *Registry) ResolveVerificationKey(req VerificationRequest) (KeyRecord, error) {
	if err := req.Validate(); err != nil {
		return KeyRecord{}, err
	}
	record, err := r.ResolveForVerification(req.KeyID, req.IssuedAt)
	if err != nil {
		return KeyRecord{}, err
	}
	if record.TrustDomain() != req.TrustDomain {
		return KeyRecord{}, fmt.Errorf("%w: key %q", ErrTrustDomainMismatch, req.KeyID)
	}
	if record.AuthorityRole() != req.AuthorityRole ||
		record.AuthorityPrincipal() != req.AuthorityPrincipal {
		return KeyRecord{}, fmt.Errorf("%w: key %q", ErrAuthorityMismatch, req.KeyID)
	}
	if record.Algorithm() != req.ProtectedAlgorithm {
		return KeyRecord{}, fmt.Errorf(
			"%w: protected=%q key=%q",
			ErrAlgorithmMismatch,
			req.ProtectedAlgorithm,
			record.Algorithm(),
		)
	}
	return record, nil
}
