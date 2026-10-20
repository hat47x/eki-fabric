package decisionpayload

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

const LeaseSchemaVersion uint64 = 1

var ErrInvalidLease = errors.New("invalid lease payload")

// LeaseConfig is the closed `lease-v1` shape of spec/decisions/v1/decisions.cddl.
//
// The wire format carries Unix milliseconds. IssuedAt and ExpiresAt must
// therefore be millisecond-aligned so that encoding and decoding are lossless
// and the signed bytes are a deterministic function of this value.
//
// PolicyEpoch is intentionally absent. It is Control Plane linearization
// state; the wire identity of the policy snapshot is PolicyRevision, which the
// PolicyStore never rebinds (#78).
type LeaseConfig struct {
	SchemaVersion      uint64
	DecisionKind       authority.DecisionKind
	DecisionID         string
	AuthorityRole      authority.Role
	AuthorityPrincipal string
	IssuedAt           time.Time
	ExpiresAt          time.Time
	PolicyRevision     string
	TaskID             ids.TaskID
	TaskIRDigest       contract.Digest
	AttemptID          ids.AttemptID
	WorkerIdentity     string
	LeaseID            ids.LeaseID
	FenceEpoch         uint64
	RenewalSequence    uint64
}

// Lease is the semantic content of a signed `eki.lease/v1` Decision.
//
// Constructing a Lease does not authorize anything. It is the input to LSE-1
// signing, and, on the relying side, the output of strict decoding that must
// still pass signature, key, and current-state verification.
type Lease struct {
	decisionID         string
	authorityPrincipal string
	issuedAt           time.Time
	expiresAt          time.Time
	policyRevision     string
	taskID             ids.TaskID
	taskIRDigest       contract.Digest
	attemptID          ids.AttemptID
	workerIdentity     string
	leaseID            ids.LeaseID
	fenceEpoch         uint64
	renewalSequence    uint64
}

func NewLease(cfg LeaseConfig) (Lease, error) {
	if cfg.SchemaVersion != LeaseSchemaVersion {
		return Lease{}, fmt.Errorf("%w: schema version %d", ErrInvalidLease, cfg.SchemaVersion)
	}
	if cfg.DecisionKind != authority.DecisionLease {
		return Lease{}, fmt.Errorf("%w: decision kind %q", ErrInvalidLease, cfg.DecisionKind)
	}
	if cfg.AuthorityRole != authority.RoleLease {
		return Lease{}, fmt.Errorf("%w: authority role %q", ErrInvalidLease, cfg.AuthorityRole)
	}
	if err := validateLeaseText("decision ID", cfg.DecisionID, 128); err != nil {
		return Lease{}, err
	}
	if err := validateLeaseText("authority principal", cfg.AuthorityPrincipal, 256); err != nil {
		return Lease{}, err
	}
	if cfg.IssuedAt.IsZero() || cfg.ExpiresAt.IsZero() || !cfg.IssuedAt.Before(cfg.ExpiresAt) {
		return Lease{}, fmt.Errorf("%w: issued/expires window is invalid", ErrInvalidLease)
	}
	if cfg.IssuedAt.UnixMilli() < 0 {
		return Lease{}, fmt.Errorf("%w: issued time precedes the Unix epoch", ErrInvalidLease)
	}
	if !millisecondAligned(cfg.IssuedAt) || !millisecondAligned(cfg.ExpiresAt) {
		return Lease{}, fmt.Errorf("%w: issued/expires time must be millisecond aligned", ErrInvalidLease)
	}
	if err := validateLeaseText("policy revision", cfg.PolicyRevision, 128); err != nil {
		return Lease{}, err
	}
	if _, err := ids.ParseTaskID(cfg.TaskID.String()); err != nil {
		return Lease{}, fmt.Errorf("%w: task ID: %v", ErrInvalidLease, err)
	}
	if cfg.TaskIRDigest.IsZero() {
		return Lease{}, fmt.Errorf("%w: TaskIR digest is required", ErrInvalidLease)
	}
	if _, err := ids.ParseAttemptID(cfg.AttemptID.String()); err != nil {
		return Lease{}, fmt.Errorf("%w: attempt ID: %v", ErrInvalidLease, err)
	}
	if err := validateLeaseText("worker identity", cfg.WorkerIdentity, 256); err != nil {
		return Lease{}, err
	}
	if _, err := ids.ParseLeaseID(cfg.LeaseID.String()); err != nil {
		return Lease{}, fmt.Errorf("%w: lease ID: %v", ErrInvalidLease, err)
	}
	if cfg.FenceEpoch == 0 {
		return Lease{}, fmt.Errorf("%w: fence epoch must be positive", ErrInvalidLease)
	}
	return Lease{
		decisionID:         cfg.DecisionID,
		authorityPrincipal: cfg.AuthorityPrincipal,
		issuedAt:           cfg.IssuedAt.UTC(),
		expiresAt:          cfg.ExpiresAt.UTC(),
		policyRevision:     cfg.PolicyRevision,
		taskID:             cfg.TaskID,
		taskIRDigest:       cfg.TaskIRDigest,
		attemptID:          cfg.AttemptID,
		workerIdentity:     cfg.WorkerIdentity,
		leaseID:            cfg.LeaseID,
		fenceEpoch:         cfg.FenceEpoch,
		renewalSequence:    cfg.RenewalSequence,
	}, nil
}

func millisecondAligned(t time.Time) bool {
	return t.Nanosecond()%int(time.Millisecond) == 0
}

func validateLeaseText(name, value string, maxBytes int) error {
	if err := validateText(name, value, maxBytes); err != nil {
		return fmt.Errorf("%w: invalid %s", ErrInvalidLease, name)
	}
	return nil
}

func (l Lease) SchemaVersion() uint64                { return LeaseSchemaVersion }
func (l Lease) DecisionKind() authority.DecisionKind { return authority.DecisionLease }
func (l Lease) DecisionID() string                   { return l.decisionID }
func (l Lease) AuthorityRole() authority.Role        { return authority.RoleLease }
func (l Lease) AuthorityPrincipal() string           { return l.authorityPrincipal }
func (l Lease) IssuedAt() time.Time                  { return l.issuedAt }
func (l Lease) ExpiresAt() time.Time                 { return l.expiresAt }
func (l Lease) PolicyRevision() string               { return l.policyRevision }
func (l Lease) TaskID() ids.TaskID                   { return l.taskID }
func (l Lease) TaskIRDigest() contract.Digest        { return l.taskIRDigest }
func (l Lease) AttemptID() ids.AttemptID             { return l.attemptID }
func (l Lease) WorkerIdentity() string               { return l.workerIdentity }
func (l Lease) LeaseID() ids.LeaseID                 { return l.leaseID }
func (l Lease) FenceEpoch() uint64                   { return l.fenceEpoch }
func (l Lease) RenewalSequence() uint64              { return l.renewalSequence }

// Equal reports whether two Leases carry the same signed semantic content.
func (l Lease) Equal(other Lease) bool {
	return l.decisionID == other.decisionID &&
		l.authorityPrincipal == other.authorityPrincipal &&
		l.issuedAt.Equal(other.issuedAt) &&
		l.expiresAt.Equal(other.expiresAt) &&
		l.policyRevision == other.policyRevision &&
		l.taskID == other.taskID &&
		l.taskIRDigest == other.taskIRDigest &&
		l.attemptID == other.attemptID &&
		l.workerIdentity == other.workerIdentity &&
		l.leaseID == other.leaseID &&
		l.fenceEpoch == other.fenceEpoch &&
		l.renewalSequence == other.renewalSequence
}

func (l Lease) Valid() bool {
	return l.decisionID != "" &&
		l.authorityPrincipal != "" &&
		!l.issuedAt.IsZero() &&
		l.issuedAt.Before(l.expiresAt) &&
		l.policyRevision != "" &&
		l.taskID != "" &&
		!l.taskIRDigest.IsZero() &&
		l.attemptID != "" &&
		l.workerIdentity != "" &&
		l.leaseID != "" &&
		l.fenceEpoch > 0
}
