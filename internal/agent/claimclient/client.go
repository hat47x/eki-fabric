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

	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

var ErrInvalidConfig = errors.New("invalid claim client configuration")

type Client struct {
	api       workerplane.ClaimClient
	principal workerplane.Principal
}

var _ interface {
	Send(ctx context.Context, claim resultclaim.Claim) error
} = (*Client)(nil)

func New(api workerplane.ClaimClient, principal workerplane.Principal) (*Client, error) {
	switch {
	case api == nil:
		return nil, fmt.Errorf("%w: Worker Plane client is required", ErrInvalidConfig)
	case principal.WorkerIdentity == "":
		return nil, fmt.Errorf("%w: Worker identity is required", ErrInvalidConfig)
	}
	return &Client{api: api, principal: principal}, nil
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
	_, err := c.api.SubmitClaim(ctx, c.principal, claim)
	return err
}
