package statestore

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/workorderemission"
)

var (
	ErrWorkOrderEmissionConflict = errors.New("WorkOrder emission binding conflicts with durable state")
	ErrWorkOrderEmissionAborted  = errors.New("WorkOrder was aborted before Authority emission")
)

// WorkOrderEmissionCommit is the durable linearization point of one WOR-1
// WorkOrder. The store re-checks, in the same write serialization, that the
// Lease is EMITTED with exactly the referenced Lease Decision, that its
// FenceEpoch is still the latest for the Task, that the Attempt still holds it
// while ASSIGNED, that the Task is schedulable, and that the Policy Epoch the
// Lease was fenced under is still current and active.
type WorkOrderEmissionCommit struct {
	LeaseID                 ids.LeaseID
	ExpectedTaskRevision    uint64
	ExpectedAttemptRevision uint64
	ExpectedPolicyEpoch     uint64
	ExpectedPolicyRevision  string
	ExpectedFenceEpoch      uint64
	// LeaseDecisionID and LeasePayloadDigest are the Lease reference signed
	// into the WorkOrder. They must equal the EMITTED Lease record.
	LeaseDecisionID    string
	LeasePayloadDigest contract.Digest
	WorkerIdentity     string
	DecisionID         string
	WorkOrderID        string
	TrustDomain        string
	AuthorityPrincipal string
	KeyID              decisiontrust.KeyID
	Algorithm          decisiontrust.Algorithm
	PayloadDigest      contract.Digest
	EnvelopeDigest     contract.Digest
	SignedEnvelope     []byte
	ExpiresAt          time.Time
	CommittedAt        time.Time
}

func (c WorkOrderEmissionCommit) Validate() error {
	if _, err := ids.ParseLeaseID(c.LeaseID.String()); err != nil {
		return fmt.Errorf("WorkOrder emission: Lease ID: %w", err)
	}
	if c.ExpectedTaskRevision == 0 || c.ExpectedAttemptRevision == 0 ||
		c.ExpectedPolicyEpoch == 0 || c.ExpectedFenceEpoch == 0 {
		return errors.New("WorkOrder emission: expected Task/Attempt/Policy/Fence values must be positive")
	}
	if c.LeaseDecisionID == "" || len(c.LeaseDecisionID) > 128 || !utf8.ValidString(c.LeaseDecisionID) {
		return errors.New("WorkOrder emission: invalid Lease Decision ID")
	}
	if c.LeasePayloadDigest.IsZero() {
		return errors.New("WorkOrder emission: Lease payload digest is required")
	}
	if c.ExpiresAt.IsZero() || c.CommittedAt.IsZero() || !c.CommittedAt.Before(c.ExpiresAt) {
		return errors.New("WorkOrder emission: committed time must precede WorkOrder expiry")
	}
	if _, err := c.Record(); err != nil {
		return fmt.Errorf("WorkOrder emission: %w", err)
	}
	return nil
}

// Record builds the EMITTED record this commit stores.
func (c WorkOrderEmissionCommit) Record() (workorderemission.Record, error) {
	return workorderemission.NewEmitted(c.emittedConfig())
}

func (c WorkOrderEmissionCommit) emittedConfig() workorderemission.EmittedConfig {
	return workorderemission.EmittedConfig{
		LeaseID:            c.LeaseID,
		DecisionID:         c.DecisionID,
		WorkOrderID:        c.WorkOrderID,
		WorkerIdentity:     c.WorkerIdentity,
		TrustDomain:        c.TrustDomain,
		AuthorityPrincipal: c.AuthorityPrincipal,
		KeyID:              c.KeyID,
		Algorithm:          c.Algorithm,
		PayloadDigest:      c.PayloadDigest,
		EnvelopeDigest:     c.EnvelopeDigest,
		SignedEnvelope:     c.SignedEnvelope,
		PolicyRevision:     c.ExpectedPolicyRevision,
		PolicyEpoch:        c.ExpectedPolicyEpoch,
		FenceEpoch:         c.ExpectedFenceEpoch,
		ExpiresAt:          c.ExpiresAt,
		CommittedAt:        c.CommittedAt,
	}
}

// Matches reports whether an existing EMITTED record is this exact commit.
func (c WorkOrderEmissionCommit) Matches(record workorderemission.Record) bool {
	if record.Status() != workorderemission.StatusEmitted {
		return false
	}
	return record.LeaseID() == c.LeaseID &&
		record.DecisionID() == c.DecisionID &&
		record.WorkOrderID() == c.WorkOrderID &&
		record.WorkerIdentity() == c.WorkerIdentity &&
		record.TrustDomain() == c.TrustDomain &&
		record.AuthorityPrincipal() == c.AuthorityPrincipal &&
		record.KeyID() == c.KeyID &&
		record.Algorithm() == c.Algorithm &&
		record.PayloadDigest() == c.PayloadDigest &&
		record.EnvelopeDigest() == c.EnvelopeDigest &&
		record.PolicyEpoch() == c.ExpectedPolicyEpoch &&
		record.PolicyRevision() == c.ExpectedPolicyRevision &&
		record.FenceEpoch() == c.ExpectedFenceEpoch &&
		string(record.SignedEnvelope()) == string(c.SignedEnvelope)
}

// LeaseEmissionMatches reports whether the stored Lease emission is the
// EMITTED Lease Decision this commit references.
func (c WorkOrderEmissionCommit) LeaseEmissionMatches(lease leaseemission.Record) bool {
	return lease.Status() == leaseemission.StatusEmitted &&
		lease.LeaseID() == c.LeaseID &&
		lease.DecisionID() == c.LeaseDecisionID &&
		lease.PayloadDigest() == c.LeasePayloadDigest &&
		lease.PolicyEpoch() == c.ExpectedPolicyEpoch &&
		lease.PolicyRevision() == c.ExpectedPolicyRevision
}

// WorkOrderEmissionAbort durably records that no WorkOrder will be signed for
// a Lease. It changes no Task, Attempt, Lease, or fence state.
type WorkOrderEmissionAbort struct {
	LeaseID     ids.LeaseID
	AbortReason workorderemission.AbortReason
	Reason      string
	CommittedAt time.Time
}

func (a WorkOrderEmissionAbort) Validate() error {
	if _, err := workorderemission.NewAborted(workorderemission.AbortedConfig{
		LeaseID: a.LeaseID, AbortReason: a.AbortReason, Reason: a.Reason, CommittedAt: a.CommittedAt,
	}); err != nil {
		return fmt.Errorf("WorkOrder abort: %w", err)
	}
	return nil
}

type WorkOrderEmissionResult struct {
	Record   workorderemission.Record
	Replayed bool
}

// WorkOrderEmissionStore owns the durable transition from a private signed
// WorkOrder candidate to externally replayable WOR-1 Authority.
//
// Implementations must not expose newly signed bytes through this interface
// before CommitWorkOrderEmission succeeds, must keep records immutable, and
// must keep (Trust Domain, WOR-1 principal, Decision ID) unique among EMITTED
// records. AbortWorkOrderEmission must refuse to overwrite an EMITTED record
// and must not modify any other state.
type WorkOrderEmissionStore interface {
	CommitWorkOrderEmission(ctx context.Context, commit WorkOrderEmissionCommit) (WorkOrderEmissionResult, error)
	AbortWorkOrderEmission(ctx context.Context, abort WorkOrderEmissionAbort) (WorkOrderEmissionResult, error)
	LoadWorkOrderEmission(ctx context.Context, id ids.LeaseID) (workorderemission.Record, error)
}
