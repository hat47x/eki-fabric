// Package claimplane is the Control Plane entry point for a Worker's
// ResultClaim.
//
// The transport authenticates the Worker and passes the resulting principal.
// This boundary checks that the authenticated principal is the Worker the claim
// names, before handing the claim to claimaccept. Without that check, any
// authenticated Worker could submit a claim for another Worker's Attempt by
// copying that Worker's identity into the claim.
package claimplane

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var (
	ErrInvalidConfig    = errors.New("invalid claim plane configuration")
	ErrInvalidPrincipal = errors.New("invalid authenticated Worker principal")
	ErrNotClaimOwner    = errors.New("the claim names another Worker")
)

// Principal is the authenticated Worker from the transport. TrustDomain may be
// empty in a single-domain deployment; WorkerIdentity may not.
type Principal struct {
	WorkerIdentity string
	TrustDomain    string
}

// Acceptor is claimaccept.Service in production.
type Acceptor interface {
	Accept(ctx context.Context, claim resultclaim.Claim) (claimaccept.Result, error)
}

type Service struct {
	acceptor Acceptor
}

func New(acceptor Acceptor) (*Service, error) {
	if acceptor == nil {
		return nil, fmt.Errorf("%w: acceptor is required", ErrInvalidConfig)
	}
	return &Service{acceptor: acceptor}, nil
}

// Submit accepts a claim only when the authenticated Worker is the Worker the
// claim names. The claim's content is not trusted further than claimaccept
// already allows: this boundary adds identity, not correctness.
func (s *Service) Submit(ctx context.Context, principal Principal, claim resultclaim.Claim) (claimaccept.Result, error) {
	if s == nil || s.acceptor == nil {
		return claimaccept.Result{}, fmt.Errorf("%w: service is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return claimaccept.Result{}, fmt.Errorf("%w: context is required", ErrInvalidConfig)
	}
	if !validIdentity(principal.WorkerIdentity) {
		return claimaccept.Result{}, fmt.Errorf("%w: Worker identity is required", ErrInvalidPrincipal)
	}
	if principal.TrustDomain != "" && !validIdentity(principal.TrustDomain) {
		return claimaccept.Result{}, fmt.Errorf("%w: Trust Domain is invalid", ErrInvalidPrincipal)
	}
	if !claim.Valid() {
		return claimaccept.Result{}, resultclaim.ErrInvalidClaim
	}
	if claim.WorkerIdentity() != principal.WorkerIdentity {
		return claimaccept.Result{}, fmt.Errorf("%w: authenticated %q, claim names %q",
			ErrNotClaimOwner, principal.WorkerIdentity, claim.WorkerIdentity())
	}
	return s.acceptor.Accept(ctx, claim)
}

func validIdentity(value string) bool {
	return len(value) > 0 && len(value) <= 256 && utf8.ValidString(value)
}
