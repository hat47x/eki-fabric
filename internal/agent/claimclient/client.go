// Package claimclient sends queued ResultClaims through the Worker Plane.
//
// The Agent knows its own authenticated principal. The client binds every
// claim to that principal, so a queued claim cannot be delivered on behalf of
// another Worker.
package claimclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

var (
	ErrInvalidConfig          = errors.New("invalid claim client configuration")
	ErrInvalidAcknowledgement = errors.New("invalid result claim acknowledgement")
)

type Client struct {
	api         workerplane.ClaimClient
	principal   workerplane.Principal
	sendTimeout time.Duration
}

const DefaultSendTimeout = 30 * time.Second

var _ interface {
	Send(ctx context.Context, claim resultclaim.Claim) error
} = (*Client)(nil)

func New(api workerplane.ClaimClient, principal workerplane.Principal) (*Client, error) {
	return NewWithTimeout(api, principal, DefaultSendTimeout)
}

// NewWithTimeout sets the maximum duration of one transport call. The
// transport must honor context cancellation; no goroutine is detached to
// abandon a non-cooperative implementation.
func NewWithTimeout(api workerplane.ClaimClient, principal workerplane.Principal, timeout time.Duration) (*Client, error) {
	switch {
	case api == nil:
		return nil, fmt.Errorf("%w: Worker Plane client is required", ErrInvalidConfig)
	case principal.WorkerIdentity == "":
		return nil, fmt.Errorf("%w: Worker identity is required", ErrInvalidConfig)
	case timeout <= 0:
		return nil, fmt.Errorf("%w: send timeout must be positive", ErrInvalidConfig)
	}
	return &Client{api: api, principal: principal, sendTimeout: timeout}, nil
}

// Send satisfies the claim queue's Sender. A transport error is returned
// unchanged; the queue keeps the claim and retries later.
func (c *Client) Send(ctx context.Context, claim resultclaim.Claim) error {
	if c == nil || c.api == nil {
		return fmt.Errorf("%w: client is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidConfig)
	}
	if claim.WorkerIdentity() != c.principal.WorkerIdentity {
		return fmt.Errorf("%w: claim names %q, this Agent is %q", ErrInvalidConfig, claim.WorkerIdentity(), c.principal.WorkerIdentity)
	}
	callCtx, cancel := context.WithTimeout(ctx, c.sendTimeout)
	defer cancel()
	if err := callCtx.Err(); err != nil {
		return err
	}
	ack, err := c.api.SubmitClaim(callCtx, c.principal, claim)
	if err != nil {
		return err
	}
	return validateAcknowledgement(ack)
}

// A nil transport error alone is not a receipt. Unknown/empty fields or an
// inconsistent verification flag leave delivery unconfirmed, so the queue
// retains the claim. This validates the message shape, not result correctness
// or authentication/binding of the transport's response to this request.
func validateAcknowledgement(ack workerplane.ClaimAck) error {
	switch resultclaim.Kind(ack.Decision) {
	case resultclaim.KindNew, resultclaim.KindDuplicate, resultclaim.KindStale, resultclaim.KindLate:
	default:
		return fmt.Errorf("%w: unknown decision %q", ErrInvalidAcknowledgement, ack.Decision)
	}
	phase := attemptstate.AttemptPhase(ack.AttemptPhase)
	switch phase {
	case attemptstate.AttemptPhaseCreated, attemptstate.AttemptPhasePlacing,
		attemptstate.AttemptPhaseAssigned, attemptstate.AttemptPhaseDispatched,
		attemptstate.AttemptPhaseClaimed, attemptstate.AttemptPhaseVerifying,
		attemptstate.AttemptPhaseClosed:
	default:
		return fmt.Errorf("%w: unknown Attempt phase %q", ErrInvalidAcknowledgement, ack.AttemptPhase)
	}
	if ack.VerificationRequired != (phase == attemptstate.AttemptPhaseVerifying) {
		return fmt.Errorf("%w: verification flag differs from Attempt phase", ErrInvalidAcknowledgement)
	}
	return nil
}
