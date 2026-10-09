// Package leaseemission defines durable LSE-1 authority-emission state.
//
// A signed Lease becomes replayable worker-facing authority only after an
// Emitted record is durably committed. A signature produced before this
// record exists is still private, discardable intermediate data.
package leaseemission

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

type Status string

const (
	StatusEmitted Status = "emitted"
	StatusAborted Status = "aborted"
)

var ErrInvalidRecord = errors.New("invalid Lease emission record")

type EmittedConfig struct {
	LeaseID            ids.LeaseID
	DecisionID         string
	TrustDomain        string
	AuthorityPrincipal string
	KeyID              decisiontrust.KeyID
	Algorithm          decisiontrust.Algorithm
	PayloadDigest      contract.Digest
	EnvelopeDigest     contract.Digest
	SignedEnvelope     []byte
	PolicyRevision     string
	PolicyEpoch        uint64
	CommittedAt        time.Time
}

type AbortedConfig struct {
	LeaseID     ids.LeaseID
	CloseReason string
	Reason      string
	CommittedAt time.Time
}

type Record struct {
	leaseID            ids.LeaseID
	status             Status
	decisionID         string
	trustDomain        string
	authorityPrincipal string
	keyID              decisiontrust.KeyID
	algorithm          decisiontrust.Algorithm
	payloadDigest      contract.Digest
	envelopeDigest     contract.Digest
	signedEnvelope     []byte
	policyRevision     string
	policyEpoch        uint64
	closeReason        string
	reason             string
	committedAt        time.Time
}

func NewEmitted(cfg EmittedConfig) (Record, error) {
	if _, err := ids.ParseLeaseID(cfg.LeaseID.String()); err != nil {
		return Record{}, fmt.Errorf("%w: Lease ID: %v", ErrInvalidRecord, err)
	}
	if err := validateText("Decision ID", cfg.DecisionID, 128); err != nil {
		return Record{}, err
	}
	if err := validateText("Trust Domain", cfg.TrustDomain, 256); err != nil {
		return Record{}, err
	}
	if err := validateText("Authority principal", cfg.AuthorityPrincipal, 256); err != nil {
		return Record{}, err
	}
	if err := validateText("key ID", string(cfg.KeyID), decisiontrust.MaxKeyIDBytes); err != nil {
		return Record{}, err
	}
	if _, ok := cfg.Algorithm.COSEIdentifier(); !ok {
		return Record{}, fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidRecord, cfg.Algorithm)
	}
	if cfg.PayloadDigest.IsZero() {
		return Record{}, fmt.Errorf("%w: payload digest is required", ErrInvalidRecord)
	}
	if cfg.EnvelopeDigest.IsZero() {
		return Record{}, fmt.Errorf("%w: envelope digest is required", ErrInvalidRecord)
	}
	if len(cfg.SignedEnvelope) == 0 || len(cfg.SignedEnvelope) > decisiontrust.MaxCOSESign1Bytes {
		return Record{}, fmt.Errorf(
			"%w: signed envelope must contain 1..%d bytes",
			ErrInvalidRecord,
			decisiontrust.MaxCOSESign1Bytes,
		)
	}
	if contract.SHA256(cfg.SignedEnvelope) != cfg.EnvelopeDigest {
		return Record{}, fmt.Errorf("%w: signed envelope digest mismatch", ErrInvalidRecord)
	}
	if err := validateText("policy revision", cfg.PolicyRevision, 128); err != nil {
		return Record{}, err
	}
	if cfg.PolicyEpoch == 0 {
		return Record{}, fmt.Errorf("%w: policy epoch must be positive", ErrInvalidRecord)
	}
	if cfg.CommittedAt.IsZero() {
		return Record{}, fmt.Errorf("%w: committed time is required", ErrInvalidRecord)
	}
	return Record{
		leaseID:            cfg.LeaseID,
		status:             StatusEmitted,
		decisionID:         cfg.DecisionID,
		trustDomain:        cfg.TrustDomain,
		authorityPrincipal: cfg.AuthorityPrincipal,
		keyID:              cfg.KeyID,
		algorithm:          cfg.Algorithm,
		payloadDigest:      cfg.PayloadDigest,
		envelopeDigest:     cfg.EnvelopeDigest,
		signedEnvelope:     append([]byte(nil), cfg.SignedEnvelope...),
		policyRevision:     cfg.PolicyRevision,
		policyEpoch:        cfg.PolicyEpoch,
		committedAt:        cfg.CommittedAt.UTC(),
	}, nil
}

func NewAborted(cfg AbortedConfig) (Record, error) {
	if _, err := ids.ParseLeaseID(cfg.LeaseID.String()); err != nil {
		return Record{}, fmt.Errorf("%w: Lease ID: %v", ErrInvalidRecord, err)
	}
	if err := validateText("abort close reason", cfg.CloseReason, 64); err != nil {
		return Record{}, err
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
		closeReason: cfg.CloseReason,
		reason:      cfg.Reason,
		committedAt: cfg.CommittedAt.UTC(),
	}, nil
}

func (r Record) LeaseID() ids.LeaseID               { return r.leaseID }
func (r Record) Status() Status                     { return r.status }
func (r Record) DecisionID() string                 { return r.decisionID }
func (r Record) TrustDomain() string                { return r.trustDomain }
func (r Record) AuthorityPrincipal() string         { return r.authorityPrincipal }
func (r Record) KeyID() decisiontrust.KeyID         { return r.keyID }
func (r Record) Algorithm() decisiontrust.Algorithm { return r.algorithm }
func (r Record) PayloadDigest() contract.Digest     { return r.payloadDigest }
func (r Record) EnvelopeDigest() contract.Digest    { return r.envelopeDigest }
func (r Record) PolicyRevision() string             { return r.policyRevision }
func (r Record) PolicyEpoch() uint64                { return r.policyEpoch }
func (r Record) CloseReason() string                { return r.closeReason }
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
			TrustDomain:        r.trustDomain,
			AuthorityPrincipal: r.authorityPrincipal,
			KeyID:              r.keyID,
			Algorithm:          r.algorithm,
			PayloadDigest:      r.payloadDigest,
			EnvelopeDigest:     r.envelopeDigest,
			SignedEnvelope:     r.signedEnvelope,
			PolicyRevision:     r.policyRevision,
			PolicyEpoch:        r.policyEpoch,
			CommittedAt:        r.committedAt,
		})
		return err
	case StatusAborted:
		_, err := NewAborted(AbortedConfig{
			LeaseID: r.leaseID, CloseReason: r.closeReason, Reason: r.reason, CommittedAt: r.committedAt,
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
