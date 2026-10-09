package statestore

import (
	"context"
	"errors"

	"github.com/hat47x/eki-fabric/internal/core/policystate"
)

var (
	ErrPolicyUnavailable = errors.New("policy state is not initialized")
	ErrPolicyChanged     = errors.New("policy state changed before lease reservation")
	ErrPolicyRevoked     = errors.New("policy revision is revoked or inactive")
	ErrPolicyReactivation = errors.New("revoked policy revision cannot be reactivated")
)

// PolicyStore owns the durable, monotonic policy snapshot used by LSE-1.
//
// It is intentionally separate from the Admission Store. Sharing one physical
// SQLite database does not grant ADM-2 the ability to mutate Policy Authority.
type PolicyStore interface {
	LoadCurrentPolicy(ctx context.Context) (policystate.State, error)
	CompareAndSwapPolicy(ctx context.Context, expectedEpoch uint64, next policystate.State) error
}
