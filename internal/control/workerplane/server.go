// Package workerplane adapts the ResultClaim acceptance boundaries to the
// Worker Plane messages.
//
// A Connect service handler calls Server.SubmitClaim after authenticating the
// Worker. The handler owns authentication and framing; this package maps the
// authenticated principal and the claim to claimplane, then maps the result to
// an acknowledgement.
package workerplane

import (
	"context"
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

var ErrInvalidConfig = errors.New("invalid Worker Plane service configuration")

type Config struct {
	Claims *claimplane.Service
}

type Server struct {
	claims *claimplane.Service
}

var _ workerplane.ClaimClient = (*Server)(nil)

func New(cfg Config) (*Server, error) {
	if cfg.Claims == nil {
		return nil, fmt.Errorf("%w: claim plane is required", ErrInvalidConfig)
	}
	return &Server{claims: cfg.Claims}, nil
}

// SubmitClaim authenticates nothing itself: the principal arrives from the
// transport, and claimplane rejects a claim that names another Worker.
func (s *Server) SubmitClaim(ctx context.Context, principal workerplane.Principal, claim resultclaim.Claim) (workerplane.ClaimAck, error) {
	if s == nil || s.claims == nil {
		return workerplane.ClaimAck{}, fmt.Errorf("%w: service is not initialized", ErrInvalidConfig)
	}
	result, err := s.claims.Submit(ctx, claimplane.Principal{
		WorkerIdentity: principal.WorkerIdentity,
		TrustDomain:    principal.TrustDomain,
	}, claim)
	if err != nil {
		return workerplane.ClaimAck{}, err
	}
	return ackFor(result), nil
}

func ackFor(result claimaccept.Result) workerplane.ClaimAck {
	return workerplane.ClaimAck{
		Decision:             string(result.Decision.Kind),
		AttemptPhase:         string(result.Attempt.Phase),
		VerificationRequired: result.VerificationRequired,
	}
}
