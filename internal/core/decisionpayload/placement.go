package decisionpayload

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

const PlacementApprovalSchemaVersion uint64 = 1

var ErrInvalidPlacementApproval = errors.New("invalid placement approval payload")

type EvidenceRefConfig struct {
	Kind     string
	Identity string
	Digest   contract.Digest
}

type EvidenceRef struct {
	kind     string
	identity string
	digest   contract.Digest
}

func (e EvidenceRef) Kind() string            { return e.kind }
func (e EvidenceRef) Identity() string        { return e.identity }
func (e EvidenceRef) Digest() contract.Digest { return e.digest }

type PlacementApprovalConfig struct {
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
	Evidence           []EvidenceRefConfig
	PayloadDigest      contract.Digest
}

type PlacementApproval struct {
	decisionID         string
	authorityPrincipal string
	issuedAt           time.Time
	expiresAt          time.Time
	policyRevision     string
	taskID             ids.TaskID
	taskIRDigest       contract.Digest
	attemptID          ids.AttemptID
	workerIdentity     string
	evidence           []EvidenceRef
	payloadDigest      contract.Digest
}

func NewPlacementApproval(cfg PlacementApprovalConfig) (PlacementApproval, error) {
	if cfg.SchemaVersion != PlacementApprovalSchemaVersion {
		return PlacementApproval{}, fmt.Errorf("%w: schema version %d", ErrInvalidPlacementApproval, cfg.SchemaVersion)
	}
	if cfg.DecisionKind != authority.DecisionPlacementApproval {
		return PlacementApproval{}, fmt.Errorf("%w: decision kind %q", ErrInvalidPlacementApproval, cfg.DecisionKind)
	}
	if cfg.AuthorityRole != authority.RolePlacementAuthorization {
		return PlacementApproval{}, fmt.Errorf("%w: authority role %q", ErrInvalidPlacementApproval, cfg.AuthorityRole)
	}
	if err := validateText("decision ID", cfg.DecisionID, 128); err != nil {
		return PlacementApproval{}, err
	}
	if err := validateText("authority principal", cfg.AuthorityPrincipal, 256); err != nil {
		return PlacementApproval{}, err
	}
	if cfg.IssuedAt.IsZero() || cfg.ExpiresAt.IsZero() || !cfg.IssuedAt.Before(cfg.ExpiresAt) {
		return PlacementApproval{}, fmt.Errorf("%w: issued/expires window is invalid", ErrInvalidPlacementApproval)
	}
	if err := validateText("policy revision", cfg.PolicyRevision, 128); err != nil {
		return PlacementApproval{}, err
	}
	if _, err := ids.ParseTaskID(cfg.TaskID.String()); err != nil {
		return PlacementApproval{}, fmt.Errorf("%w: task ID: %v", ErrInvalidPlacementApproval, err)
	}
	if cfg.TaskIRDigest.IsZero() {
		return PlacementApproval{}, fmt.Errorf("%w: TaskIR digest is required", ErrInvalidPlacementApproval)
	}
	if _, err := ids.ParseAttemptID(cfg.AttemptID.String()); err != nil {
		return PlacementApproval{}, fmt.Errorf("%w: attempt ID: %v", ErrInvalidPlacementApproval, err)
	}
	if err := validateText("worker identity", cfg.WorkerIdentity, 256); err != nil {
		return PlacementApproval{}, err
	}
	// PayloadDigest is deliberately not required here: it is not a wire
	// field. It is derived from the encoded payload bytes, so only the codec
	// layer can assign it. A producer constructs the value without it, and a
	// relying party only accepts a value the decoder derived from real bytes
	// (Valid requires it).
	if len(cfg.Evidence) > 16 {
		return PlacementApproval{}, fmt.Errorf("%w: evidence count %d exceeds 16", ErrInvalidPlacementApproval, len(cfg.Evidence))
	}

	evidence := make([]EvidenceRef, 0, len(cfg.Evidence))
	for i, ref := range cfg.Evidence {
		if err := validateText("evidence kind", ref.Kind, 128); err != nil {
			return PlacementApproval{}, fmt.Errorf("%w: evidence[%d]: %v", ErrInvalidPlacementApproval, i, err)
		}
		if err := validateText("evidence identity", ref.Identity, 256); err != nil {
			return PlacementApproval{}, fmt.Errorf("%w: evidence[%d]: %v", ErrInvalidPlacementApproval, i, err)
		}
		if ref.Digest.IsZero() {
			return PlacementApproval{}, fmt.Errorf("%w: evidence[%d] digest is required", ErrInvalidPlacementApproval, i)
		}
		evidence = append(evidence, EvidenceRef{kind: ref.Kind, identity: ref.Identity, digest: ref.Digest})
	}

	return PlacementApproval{
		decisionID:         cfg.DecisionID,
		authorityPrincipal: cfg.AuthorityPrincipal,
		issuedAt:           cfg.IssuedAt.UTC(),
		expiresAt:          cfg.ExpiresAt.UTC(),
		policyRevision:     cfg.PolicyRevision,
		taskID:             cfg.TaskID,
		taskIRDigest:       cfg.TaskIRDigest,
		attemptID:          cfg.AttemptID,
		workerIdentity:     cfg.WorkerIdentity,
		evidence:           evidence,
		payloadDigest:      cfg.PayloadDigest,
	}, nil
}

func validateText(name, value string, maxBytes int) error {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return fmt.Errorf("%w: invalid %s", ErrInvalidPlacementApproval, name)
	}
	return nil
}

func (p PlacementApproval) SchemaVersion() uint64 { return PlacementApprovalSchemaVersion }
func (p PlacementApproval) DecisionKind() authority.DecisionKind {
	return authority.DecisionPlacementApproval
}
func (p PlacementApproval) DecisionID() string { return p.decisionID }
func (p PlacementApproval) AuthorityRole() authority.Role {
	return authority.RolePlacementAuthorization
}
func (p PlacementApproval) AuthorityPrincipal() string     { return p.authorityPrincipal }
func (p PlacementApproval) IssuedAt() time.Time            { return p.issuedAt }
func (p PlacementApproval) ExpiresAt() time.Time           { return p.expiresAt }
func (p PlacementApproval) PolicyRevision() string         { return p.policyRevision }
func (p PlacementApproval) TaskID() ids.TaskID             { return p.taskID }
func (p PlacementApproval) TaskIRDigest() contract.Digest  { return p.taskIRDigest }
func (p PlacementApproval) AttemptID() ids.AttemptID       { return p.attemptID }
func (p PlacementApproval) WorkerIdentity() string         { return p.workerIdentity }
func (p PlacementApproval) PayloadDigest() contract.Digest { return p.payloadDigest }
func (p PlacementApproval) Evidence() []EvidenceRef {
	return append([]EvidenceRef(nil), p.evidence...)
}

// Encodable reports whether every wire field is present, so a codec can
// produce canonical payload bytes. Unlike Valid, it does not require the
// payload digest, which only the codec layer can derive from those bytes.
func (p PlacementApproval) Encodable() bool {
	return p.decisionID != "" &&
		p.authorityPrincipal != "" &&
		!p.issuedAt.IsZero() &&
		p.issuedAt.Before(p.expiresAt) &&
		p.policyRevision != "" &&
		p.taskID != "" &&
		!p.taskIRDigest.IsZero() &&
		p.attemptID != "" &&
		p.workerIdentity != ""
}

func (p PlacementApproval) Valid() bool {
	return p.Encodable() && !p.payloadDigest.IsZero()
}

// Equal reports whether two values bind the same wire fields. The payload
// digest is not part of the payload bytes, so it takes no part in the
// comparison; a decoded value and the producer value it round-trips from are
// equal.
func (p PlacementApproval) Equal(other PlacementApproval) bool {
	if len(p.evidence) != len(other.evidence) {
		return false
	}
	for i := range p.evidence {
		if p.evidence[i] != other.evidence[i] {
			return false
		}
	}
	return p.decisionID == other.decisionID &&
		p.authorityPrincipal == other.authorityPrincipal &&
		p.issuedAt.Equal(other.issuedAt) &&
		p.expiresAt.Equal(other.expiresAt) &&
		p.policyRevision == other.policyRevision &&
		p.taskID == other.taskID &&
		p.taskIRDigest == other.taskIRDigest &&
		p.attemptID == other.attemptID &&
		p.workerIdentity == other.workerIdentity
}
