// Package workorderemission defines durable WOR-1 WorkOrder emission state.
//
// A signed WorkOrder becomes replayable worker-facing authority only after an
// Emitted record is durably committed. A signature produced before this record
// exists is private, discardable intermediate data, exactly as for LSE-1
// (package leaseemission).
//
// The record is keyed by Lease ID: one EMITTED Lease yields at most one
// WorkOrder Decision. An Aborted record means no WorkOrder will ever be signed
// for that Lease. It does not close the Attempt and never rewinds FenceEpoch.
package workorderemission

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
)

type Status string

const (
	StatusEmitted Status = "emitted"
	StatusAborted Status = "aborted"
)

// AbortReason is the stable machine-readable reason WOR-1 refused to sign.
type AbortReason string

const (
	AbortPolicyRevoked      AbortReason = "policy_revoked"
	AbortKeyUnauthorized    AbortReason = "key_unauthorized"
	AbortLeaseExpired       AbortReason = "lease_expired"
	AbortLeaseSuperseded    AbortReason = "lease_superseded"
	AbortTaskCancelled      AbortReason = "task_cancelled"
	AbortTaskNotSchedulable AbortReason = "task_not_schedulable"
	AbortWorkerMismatch     AbortReason = "worker_mismatch"
)

func (r AbortReason) Valid() bool {
	switch r {
	case AbortPolicyRevoked, AbortKeyUnauthorized, AbortLeaseExpired, AbortLeaseSuperseded,
		AbortTaskCancelled, AbortTaskNotSchedulable, AbortWorkerMismatch:
		return true
	default:
		return false
	}
}

var ErrInvalidRecord = errors.New("invalid WorkOrder emission record")

type EmittedConfig struct {
	LeaseID            ids.LeaseID
	DecisionID         string
	WorkOrderID        string
	WorkerIdentity     string
	TrustDomain        string
	AuthorityPrincipal string
	KeyID              decisiontrust.KeyID
	Algorithm          decisiontrust.Algorithm
	PayloadDigest      contract.Digest
	EnvelopeDigest     contract.Digest
	SignedEnvelope     []byte
	PolicyRevision     string
	PolicyEpoch        uint64
	FenceEpoch         uint64
	ExpiresAt          time.Time
	CommittedAt        time.Time
}

type AbortedConfig struct {
	LeaseID     ids.LeaseID
	AbortReason AbortReason
	Reason      string
	CommittedAt time.Time
}

type Record struct {
	leaseID            ids.LeaseID
	status             Status
	decisionID         string
	workOrderID        string
	workerIdentity     string
	trustDomain        string
	authorityPrincipal string
	keyID              decisiontrust.KeyID
	algorithm          decisiontrust.Algorithm
	payloadDigest      contract.Digest
	envelopeDigest     contract.Digest
	signedEnvelope     []byte
	policyRevision     string
	policyEpoch        uint64
	fenceEpoch         uint64
	expiresAt          time.Time
	abortReason        AbortReason
	reason             string
	committedAt        time.Time
}

func NewEmitted(cfg EmittedConfig) (Record, error) {
	if _, err := ids.ParseLeaseID(cfg.LeaseID.String()); err != nil {
		return Record{}, fmt.Errorf("%w: Lease ID: %v", ErrInvalidRecord, err)
	}
	// The signed-binding rules are shared with LSE-1 so both Authorities store
	// the same shape of exact COSE bytes.
	if err := leaseemission.ValidateSignedBinding(
		cfg.DecisionID,
		cfg.TrustDomain,
		cfg.AuthorityPrincipal,
		cfg.KeyID,
		cfg.Algorithm,
		cfg.PayloadDigest,
		cfg.EnvelopeDigest,
		cfg.SignedEnvelope,
	); err != nil {
		return Record{}, fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	if err := validateText("WorkOrder ID", cfg.WorkOrderID, 128); err != nil {
		return Record{}, err
	}
	if err := validateText("Worker identity", cfg.WorkerIdentity, 256); err != nil {
		return Record{}, err
	}
	if err := validateText("policy revision", cfg.PolicyRevision, 128); err != nil {
		return Record{}, err
	}
	if cfg.PolicyEpoch == 0 {
		return Record{}, fmt.Errorf("%w: policy epoch must be positive", ErrInvalidRecord)
	}
	if cfg.FenceEpoch == 0 {
		return Record{}, fmt.Errorf("%w: fence epoch must be positive", ErrInvalidRecord)
	}
	if cfg.CommittedAt.IsZero() || cfg.ExpiresAt.IsZero() || !cfg.CommittedAt.Before(cfg.ExpiresAt) {
		return Record{}, fmt.Errorf("%w: committed time must precede WorkOrder expiry", ErrInvalidRecord)
	}
	return Record{
		leaseID:            cfg.LeaseID,
		status:             StatusEmitted,
		decisionID:         cfg.DecisionID,
		workOrderID:        cfg.WorkOrderID,
		workerIdentity:     cfg.WorkerIdentity,
		trustDomain:        cfg.TrustDomain,
		authorityPrincipal: cfg.AuthorityPrincipal,
		keyID:              cfg.KeyID,
		algorithm:          cfg.Algorithm,
		payloadDigest:      cfg.PayloadDigest,
		envelopeDigest:     cfg.EnvelopeDigest,
		signedEnvelope:     append([]byte(nil), cfg.SignedEnvelope...),
		policyRevision:     cfg.PolicyRevision,
		policyEpoch:        cfg.PolicyEpoch,
		fenceEpoch:         cfg.FenceEpoch,
		expiresAt:          cfg.ExpiresAt.UTC(),
		committedAt:        cfg.CommittedAt.UTC(),
	}, nil
}

func NewAborted(cfg AbortedConfig) (Record, error) {
	if _, err := ids.ParseLeaseID(cfg.LeaseID.String()); err != nil {
		return Record{}, fmt.Errorf("%w: Lease ID: %v", ErrInvalidRecord, err)
	}
	if !cfg.AbortReason.Valid() {
		return Record{}, fmt.Errorf("%w: unknown abort reason %q", ErrInvalidRecord, cfg.AbortReason)
	}
	if err := validateText("abort reason", cfg.Reason, 512); err != nil {
		return Record{}, err
	}
	if cfg.CommittedAt.IsZero() {
		return Record{}, fmt.Errorf("%w: committed time is required", ErrInvalidRecord)
	}
	return Record{
		leaseID:     cfg.LeaseID,
		status:      StatusAborted,
		abortReason: cfg.AbortReason,
		reason:      cfg.Reason,
		committedAt: cfg.CommittedAt.UTC(),
	}, nil
}

func (r Record) LeaseID() ids.LeaseID               { return r.leaseID }
func (r Record) Status() Status                     { return r.status }
func (r Record) DecisionID() string                 { return r.decisionID }
func (r Record) WorkOrderID() string                { return r.workOrderID }
func (r Record) WorkerIdentity() string             { return r.workerIdentity }
func (r Record) TrustDomain() string                { return r.trustDomain }
func (r Record) AuthorityPrincipal() string         { return r.authorityPrincipal }
func (r Record) KeyID() decisiontrust.KeyID         { return r.keyID }
func (r Record) Algorithm() decisiontrust.Algorithm { return r.algorithm }
func (r Record) PayloadDigest() contract.Digest     { return r.payloadDigest }
func (r Record) EnvelopeDigest() contract.Digest    { return r.envelopeDigest }
func (r Record) PolicyRevision() string             { return r.policyRevision }
func (r Record) PolicyEpoch() uint64                { return r.policyEpoch }
func (r Record) FenceEpoch() uint64                 { return r.fenceEpoch }
func (r Record) ExpiresAt() time.Time               { return r.expiresAt }
func (r Record) AbortReason() AbortReason           { return r.abortReason }
func (r Record) Reason() string                     { return r.reason }
func (r Record) CommittedAt() time.Time             { return r.committedAt }
func (r Record) SignedEnvelope() []byte             { return append([]byte(nil), r.signedEnvelope...) }
func (r Record) IsZero() bool                       { return r.status == "" && r.leaseID == "" }

func (r Record) Validate() error {
	switch r.status {
	case StatusEmitted:
		_, err := NewEmitted(EmittedConfig{
			LeaseID:            r.leaseID,
			DecisionID:         r.decisionID,
			WorkOrderID:        r.workOrderID,
			WorkerIdentity:     r.workerIdentity,
			TrustDomain:        r.trustDomain,
			AuthorityPrincipal: r.authorityPrincipal,
			KeyID:              r.keyID,
			Algorithm:          r.algorithm,
			PayloadDigest:      r.payloadDigest,
			EnvelopeDigest:     r.envelopeDigest,
			SignedEnvelope:     r.signedEnvelope,
			PolicyRevision:     r.policyRevision,
			PolicyEpoch:        r.policyEpoch,
			FenceEpoch:         r.fenceEpoch,
			ExpiresAt:          r.expiresAt,
			CommittedAt:        r.committedAt,
		})
		return err
	case StatusAborted:
		_, err := NewAborted(AbortedConfig{
			LeaseID: r.leaseID, AbortReason: r.abortReason, Reason: r.reason, CommittedAt: r.committedAt,
		})
		return err
	default:
		return fmt.Errorf("%w: unknown status %q", ErrInvalidRecord, r.status)
	}
}

func validateText(name, value string, maxBytes int) error {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s must be 1..%d valid UTF-8 bytes", ErrInvalidRecord, name, maxBytes)
	}
	return nil
}
