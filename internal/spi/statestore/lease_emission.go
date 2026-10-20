package statestore

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
)

var (
	ErrLeaseEmissionConflict = errors.New("Lease emission binding conflicts with durable state")
	ErrLeaseEmissionAborted  = errors.New("Lease reservation was aborted before Authority emission")
)

type LeaseEmissionCommit struct {
	LeaseID                 ids.LeaseID
	ExpectedTaskRevision    uint64
	ExpectedAttemptRevision uint64
	ExpectedPolicyEpoch     uint64
	DecisionID              string
	TrustDomain             string
	AuthorityPrincipal      string
	KeyID                   decisiontrust.KeyID
	Algorithm               decisiontrust.Algorithm
	PayloadDigest           contract.Digest
	EnvelopeDigest          contract.Digest
	SignedEnvelope          []byte
	CommittedAt             time.Time
}

func (c LeaseEmissionCommit) Validate() error {
	if _, err := ids.ParseLeaseID(c.LeaseID.String()); err != nil {
		return fmt.Errorf("Lease emission: Lease ID: %w", err)
	}
	if c.ExpectedTaskRevision == 0 || c.ExpectedAttemptRevision == 0 || c.ExpectedPolicyEpoch == 0 {
		return errors.New("Lease emission: expected Task/Attempt/Policy revisions must be positive")
	}
	if c.DecisionID == "" || len(c.DecisionID) > 128 || !utf8.ValidString(c.DecisionID) {
		return errors.New("Lease emission: invalid Decision ID")
	}
	if c.CommittedAt.IsZero() {
		return errors.New("Lease emission: committed time is required")
	}
	if err := leaseemission.ValidateSignedBinding(
		c.DecisionID,
		c.TrustDomain,
		c.AuthorityPrincipal,
		c.KeyID,
		c.Algorithm,
		c.PayloadDigest,
		c.EnvelopeDigest,
		c.SignedEnvelope,
	); err != nil {
		return fmt.Errorf("Lease emission: %w", err)
	}
	return nil
}

func (c LeaseEmissionCommit) Matches(record leaseemission.Record) bool {
	if record.Status() != leaseemission.StatusEmitted {
		return false
	}
	return record.LeaseID() == c.LeaseID &&
		record.DecisionID() == c.DecisionID &&
		record.TrustDomain() == c.TrustDomain &&
		record.AuthorityPrincipal() == c.AuthorityPrincipal &&
		record.KeyID() == c.KeyID &&
		record.Algorithm() == c.Algorithm &&
		record.PayloadDigest() == c.PayloadDigest &&
		record.EnvelopeDigest() == c.EnvelopeDigest &&
		string(record.SignedEnvelope()) == string(c.SignedEnvelope)
}

type LeaseEmissionAbort struct {
	LeaseID                 ids.LeaseID
	ExpectedAttemptRevision uint64
	CloseReason             attemptstate.CloseReason
	Reason                  string
	CommittedAt             time.Time
}

func (a LeaseEmissionAbort) Validate() error {
	if _, err := ids.ParseLeaseID(a.LeaseID.String()); err != nil {
		return fmt.Errorf("Lease abort: Lease ID: %w", err)
	}
	if a.ExpectedAttemptRevision == 0 {
		return errors.New("Lease abort: expected Attempt revision must be positive")
	}
	switch a.CloseReason {
	case attemptstate.CloseReasonWorkerLost,
		attemptstate.CloseReasonLeaseExpired,
		attemptstate.CloseReasonReclaimed,
		attemptstate.CloseReasonCancelled,
		attemptstate.CloseReasonSuperseded,
		attemptstate.CloseReasonDeadline,
		attemptstate.CloseReasonBudgetExhausted,
		attemptstate.CloseReasonPolicyRevoked,
		attemptstate.CloseReasonStaleAttempt:
	default:
		return fmt.Errorf("Lease abort: close reason %q is not valid before Authority emission", a.CloseReason)
	}
	record, err := leaseemission.NewAborted(leaseemission.AbortedConfig{
		LeaseID:     a.LeaseID,
		CloseReason: string(a.CloseReason),
		Reason:      a.Reason,
		CommittedAt: a.CommittedAt,
	})
	if err != nil || record.IsZero() {
		return fmt.Errorf("Lease abort: %w", err)
	}
	return nil
}

type LeaseEmissionResult struct {
	Record   leaseemission.Record
	Replayed bool
}

// LeaseEmissionStore owns the durable transition from a private signed Lease
// candidate to externally replayable Authority.
//
// Implementations must not expose newly signed bytes through this interface
// before CommitLeaseEmission succeeds.
type LeaseEmissionStore interface {
	CommitLeaseEmission(ctx context.Context, commit LeaseEmissionCommit) (LeaseEmissionResult, error)
	AbortLeaseEmission(ctx context.Context, abort LeaseEmissionAbort) (LeaseEmissionResult, error)
	LoadLeaseEmission(ctx context.Context, id ids.LeaseID) (leaseemission.Record, error)
}
