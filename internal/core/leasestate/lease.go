// Package leasestate defines durable lease reservation state.
//
// A LeaseRecord is internal authority state. It is not the signed P2 Lease
// Decision a Worker can rely on. Signing and worker-side verification are
// later boundaries.
package leasestate

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var ErrInvalidLeaseRecord = errors.New("invalid lease record")

type Config struct {
	ID                     ids.LeaseID
	TaskID                 ids.TaskID
	TaskIRDigest           contract.Digest
	AttemptID              ids.AttemptID
	WorkerIdentity         string
	PlacementAuthorityPrincipal string
	PlacementDecisionID           string
	PlacementPayloadDigest        contract.Digest
	PolicyRevision         string
	FenceEpoch             uint64
	RenewalSequence        uint64
	IssuedAt               time.Time
	ExpiresAt              time.Time
}

type LeaseRecord struct {
	ID                          ids.LeaseID
	TaskID                      ids.TaskID
	TaskIRDigest                contract.Digest
	AttemptID                   ids.AttemptID
	WorkerIdentity              string
	PlacementAuthorityPrincipal string
	PlacementDecisionID         string
	PlacementPayloadDigest      contract.Digest
	PolicyRevision         string
	FenceEpoch             uint64
	RenewalSequence        uint64
	IssuedAt               time.Time
	ExpiresAt              time.Time
}

func New(cfg Config) (LeaseRecord, error) {
	if _, err := ids.ParseLeaseID(cfg.ID.String()); err != nil {
		return LeaseRecord{}, fmt.Errorf("%w: lease ID: %v", ErrInvalidLeaseRecord, err)
	}
	if _, err := ids.ParseTaskID(cfg.TaskID.String()); err != nil {
		return LeaseRecord{}, fmt.Errorf("%w: task ID: %v", ErrInvalidLeaseRecord, err)
	}
	if cfg.TaskIRDigest.IsZero() {
		return LeaseRecord{}, fmt.Errorf("%w: TaskIR digest is required", ErrInvalidLeaseRecord)
	}
	if _, err := ids.ParseAttemptID(cfg.AttemptID.String()); err != nil {
		return LeaseRecord{}, fmt.Errorf("%w: attempt ID: %v", ErrInvalidLeaseRecord, err)
	}
	if err := validateText("worker identity", cfg.WorkerIdentity, 256); err != nil {
		return LeaseRecord{}, err
	}
	if err := validateText("placement authority principal", cfg.PlacementAuthorityPrincipal, 256); err != nil {
		return LeaseRecord{}, err
	}
	if err := validateText("placement decision ID", cfg.PlacementDecisionID, 128); err != nil {
		return LeaseRecord{}, err
	}
	if cfg.PlacementPayloadDigest.IsZero() {
		return LeaseRecord{}, fmt.Errorf("%w: placement payload digest is required", ErrInvalidLeaseRecord)
	}
	if err := validateText("policy revision", cfg.PolicyRevision, 128); err != nil {
		return LeaseRecord{}, err
	}
	if cfg.FenceEpoch == 0 {
		return LeaseRecord{}, fmt.Errorf("%w: fence epoch must be positive", ErrInvalidLeaseRecord)
	}
	if cfg.IssuedAt.IsZero() || cfg.ExpiresAt.IsZero() || !cfg.IssuedAt.Before(cfg.ExpiresAt) {
		return LeaseRecord{}, fmt.Errorf("%w: issued/expires window is invalid", ErrInvalidLeaseRecord)
	}
	return LeaseRecord{
		ID:                     cfg.ID,
		TaskID:                 cfg.TaskID,
		TaskIRDigest:           cfg.TaskIRDigest,
		AttemptID:              cfg.AttemptID,
		WorkerIdentity:         cfg.WorkerIdentity,
		PlacementAuthorityPrincipal: cfg.PlacementAuthorityPrincipal,
		PlacementDecisionID:           cfg.PlacementDecisionID,
		PlacementPayloadDigest:        cfg.PlacementPayloadDigest,
		PolicyRevision:         cfg.PolicyRevision,
		FenceEpoch:             cfg.FenceEpoch,
		RenewalSequence:        cfg.RenewalSequence,
		IssuedAt:               cfg.IssuedAt.UTC(),
		ExpiresAt:              cfg.ExpiresAt.UTC(),
	}, nil
}

func (r LeaseRecord) Validate() error {
	_, err := New(Config{
		ID:                     r.ID,
		TaskID:                 r.TaskID,
		TaskIRDigest:           r.TaskIRDigest,
		AttemptID:              r.AttemptID,
		WorkerIdentity:         r.WorkerIdentity,
		PlacementAuthorityPrincipal: r.PlacementAuthorityPrincipal,
		PlacementDecisionID:           r.PlacementDecisionID,
		PlacementPayloadDigest:        r.PlacementPayloadDigest,
		PolicyRevision:         r.PolicyRevision,
		FenceEpoch:             r.FenceEpoch,
		RenewalSequence:        r.RenewalSequence,
		IssuedAt:               r.IssuedAt,
		ExpiresAt:              r.ExpiresAt,
	})
	return err
}

func (r LeaseRecord) ActiveAt(now time.Time) bool {
	if now.IsZero() {
		return false
	}
	now = now.UTC()
	return !now.Before(r.IssuedAt) && now.Before(r.ExpiresAt)
}

func validateText(name, value string, maxBytes int) error {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s must be 1..%d valid UTF-8 bytes", ErrInvalidLeaseRecord, name, maxBytes)
	}
	return nil
}
