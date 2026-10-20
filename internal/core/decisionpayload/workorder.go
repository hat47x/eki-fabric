package decisionpayload

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

const (
	WorkOrderSchemaVersion = 1

	MaxWorkOrderReadGrants  = 64
	MaxWorkOrderWriteGrants = 16
	MinWorkOrderNonceBytes  = 16
	MaxWorkOrderNonceBytes  = 32

	// WorkOrderExternalEffectNone is the only external effect mode in v1.
	WorkOrderExternalEffectNone uint64 = 0
)

var ErrInvalidWorkOrder = errors.New("invalid work order payload")

// DecisionRefConfig names another signed Decision and the digest of its
// deterministic payload. The reference is a pointer, not a copy of authority:
// the relying side must still load and verify the referenced Decision.
type DecisionRefConfig struct {
	Kind          authority.DecisionKind
	DecisionID    string
	PayloadDigest contract.Digest
}

// DecisionRef is a typed reference to another P2 Decision payload.
type DecisionRef struct {
	kind          authority.DecisionKind
	decisionID    string
	payloadDigest contract.Digest
}

func NewDecisionRef(cfg DecisionRefConfig) (DecisionRef, error) {
	if !cfg.Kind.Valid() {
		return DecisionRef{}, fmt.Errorf("%w: unknown decision kind %q", ErrInvalidWorkOrder, cfg.Kind)
	}
	if err := validateWorkOrderText("reference decision ID", cfg.DecisionID, 128); err != nil {
		return DecisionRef{}, err
	}
	if cfg.PayloadDigest.IsZero() {
		return DecisionRef{}, fmt.Errorf("%w: reference payload digest is required", ErrInvalidWorkOrder)
	}
	return DecisionRef{kind: cfg.Kind, decisionID: cfg.DecisionID, payloadDigest: cfg.PayloadDigest}, nil
}

func (r DecisionRef) Kind() authority.DecisionKind { return r.kind }
func (r DecisionRef) DecisionID() string           { return r.decisionID }
func (r DecisionRef) PayloadDigest() contract.Digest {
	return r.payloadDigest
}

func (r DecisionRef) Valid() bool {
	return r.kind.Valid() && r.decisionID != "" && !r.payloadDigest.IsZero()
}

func (r DecisionRef) Equal(other DecisionRef) bool {
	return r.kind == other.kind && r.decisionID == other.decisionID && r.payloadDigest == other.payloadDigest
}

// ResourceLimits are maxima for one Attempt. Zero means that the resource is
// not granted at all; the issuing side must set CPUMillis and WallTimeMillis
// to a positive value.
type ResourceLimits struct {
	CPUMillis          uint64
	MemoryBytes        uint64
	TemporaryDiskBytes uint64
	WallTimeMillis     uint64
	OutputBytesMaximum uint64
	NetworkBytesMax    uint64
}

// GPULimit reserves GPU devices and VRAM. It is optional in a SandboxPolicy.
type GPULimit struct {
	DeviceCount      uint32
	MinimumVRAMBytes uint64
}

// SandboxPolicy names the immutable sandbox profile the Worker must apply.
type SandboxPolicy struct {
	Profile        string
	NetworkProfile string
	GPU            *GPULimit
}

// WorkOrderConfig is the closed `work-order-v1` shape of
// spec/decisions/v1/decisions.cddl.
type WorkOrderConfig struct {
	SchemaVersion      uint64
	DecisionKind       authority.DecisionKind
	DecisionID         string
	AuthorityRole      authority.Role
	AuthorityPrincipal string
	IssuedAt           time.Time
	ExpiresAt          time.Time
	PolicyRevision     string

	WorkOrderID        string
	TaskID             ids.TaskID
	TaskIRDigest       contract.Digest
	AttemptID          ids.AttemptID
	WorkerIdentity     string
	PlacementRef       DecisionRef
	LeaseRef           DecisionRef
	ExecutorApproval   DecisionRef
	ExecutorDigest     contract.Digest
	ReadGrants         []DecisionRef
	WriteGrants        []DecisionRef
	Limits             ResourceLimits
	Sandbox            SandboxPolicy
	ExternalEffectMode uint64
	Nonce              []byte
}

// WorkOrder is the semantic content of a signed `eki.work-order/v1` Decision.
//
// Constructing a WorkOrder authorizes nothing. The Worker must first verify the
// COSE signature and then confirm that the referenced Placement Approval, Lease,
// and Executor Approval are the current ones for this Task and Attempt.
type WorkOrder struct {
	decisionID         string
	authorityPrincipal string
	issuedAt           time.Time
	expiresAt          time.Time
	policyRevision     string

	workOrderID    string
	taskID         ids.TaskID
	taskIRDigest   contract.Digest
	attemptID      ids.AttemptID
	workerIdentity string
	placementRef   DecisionRef
	leaseRef       DecisionRef
	executorRef    DecisionRef
	executorDigest contract.Digest
	readGrants     []DecisionRef
	writeGrants    []DecisionRef
	limits         ResourceLimits
	sandbox        SandboxPolicy
	effectMode     uint64
	nonce          []byte
}

func NewWorkOrder(cfg WorkOrderConfig) (WorkOrder, error) {
	if cfg.SchemaVersion != WorkOrderSchemaVersion {
		return WorkOrder{}, fmt.Errorf("%w: schema version %d", ErrInvalidWorkOrder, cfg.SchemaVersion)
	}
	if cfg.DecisionKind != authority.DecisionWorkOrder {
		return WorkOrder{}, fmt.Errorf("%w: decision kind %q", ErrInvalidWorkOrder, cfg.DecisionKind)
	}
	if cfg.AuthorityRole != authority.RoleWorkOrder {
		return WorkOrder{}, fmt.Errorf("%w: authority role %q", ErrInvalidWorkOrder, cfg.AuthorityRole)
	}
	if err := validateWorkOrderText("decision ID", cfg.DecisionID, 128); err != nil {
		return WorkOrder{}, err
	}
	if err := validateWorkOrderText("authority principal", cfg.AuthorityPrincipal, 256); err != nil {
		return WorkOrder{}, err
	}
	if cfg.IssuedAt.IsZero() || cfg.ExpiresAt.IsZero() || !cfg.IssuedAt.Before(cfg.ExpiresAt) {
		return WorkOrder{}, fmt.Errorf("%w: issued/expires window is invalid", ErrInvalidWorkOrder)
	}
	if cfg.IssuedAt.UnixMilli() < 0 {
		return WorkOrder{}, fmt.Errorf("%w: issued time precedes the Unix epoch", ErrInvalidWorkOrder)
	}
	if !millisecondAligned(cfg.IssuedAt) || !millisecondAligned(cfg.ExpiresAt) {
		return WorkOrder{}, fmt.Errorf("%w: issued/expires time must be millisecond aligned", ErrInvalidWorkOrder)
	}
	if err := validateWorkOrderText("policy revision", cfg.PolicyRevision, 128); err != nil {
		return WorkOrder{}, err
	}
	if err := validateWorkOrderText("work order ID", cfg.WorkOrderID, 128); err != nil {
		return WorkOrder{}, err
	}
	if _, err := ids.ParseTaskID(cfg.TaskID.String()); err != nil {
		return WorkOrder{}, fmt.Errorf("%w: task ID: %v", ErrInvalidWorkOrder, err)
	}
	if cfg.TaskIRDigest.IsZero() {
		return WorkOrder{}, fmt.Errorf("%w: TaskIR digest is required", ErrInvalidWorkOrder)
	}
	if _, err := ids.ParseAttemptID(cfg.AttemptID.String()); err != nil {
		return WorkOrder{}, fmt.Errorf("%w: attempt ID: %v", ErrInvalidWorkOrder, err)
	}
	if err := validateWorkOrderText("worker identity", cfg.WorkerIdentity, 256); err != nil {
		return WorkOrder{}, err
	}
	if err := validateRefKind("placement approval", cfg.PlacementRef, authority.DecisionPlacementApproval); err != nil {
		return WorkOrder{}, err
	}
	if err := validateRefKind("lease", cfg.LeaseRef, authority.DecisionLease); err != nil {
		return WorkOrder{}, err
	}
	if err := validateRefKind("executor approval", cfg.ExecutorApproval, authority.DecisionExecutorApproval); err != nil {
		return WorkOrder{}, err
	}
	if cfg.ExecutorDigest.IsZero() {
		return WorkOrder{}, fmt.Errorf("%w: executor artifact digest is required", ErrInvalidWorkOrder)
	}
	readGrants, err := copyGrants("read grant", cfg.ReadGrants, authority.DecisionArtifactReadGrant, MaxWorkOrderReadGrants)
	if err != nil {
		return WorkOrder{}, err
	}
	writeGrants, err := copyGrants("write grant", cfg.WriteGrants, authority.DecisionArtifactWriteGrant, MaxWorkOrderWriteGrants)
	if err != nil {
		return WorkOrder{}, err
	}
	if cfg.Limits.CPUMillis == 0 {
		return WorkOrder{}, fmt.Errorf("%w: CPU millis must be positive", ErrInvalidWorkOrder)
	}
	if cfg.Limits.WallTimeMillis == 0 {
		return WorkOrder{}, fmt.Errorf("%w: wall time millis must be positive", ErrInvalidWorkOrder)
	}
	if err := validateSandbox(cfg.Sandbox); err != nil {
		return WorkOrder{}, err
	}
	if cfg.ExternalEffectMode != WorkOrderExternalEffectNone {
		return WorkOrder{}, fmt.Errorf("%w: external effect mode %d is not supported by v1", ErrInvalidWorkOrder, cfg.ExternalEffectMode)
	}
	if len(cfg.Nonce) < MinWorkOrderNonceBytes || len(cfg.Nonce) > MaxWorkOrderNonceBytes {
		return WorkOrder{}, fmt.Errorf("%w: nonce must contain %d..%d bytes", ErrInvalidWorkOrder, MinWorkOrderNonceBytes, MaxWorkOrderNonceBytes)
	}
	return WorkOrder{
		decisionID:         cfg.DecisionID,
		authorityPrincipal: cfg.AuthorityPrincipal,
		issuedAt:           cfg.IssuedAt.UTC(),
		expiresAt:          cfg.ExpiresAt.UTC(),
		policyRevision:     cfg.PolicyRevision,
		workOrderID:        cfg.WorkOrderID,
		taskID:             cfg.TaskID,
		taskIRDigest:       cfg.TaskIRDigest,
		attemptID:          cfg.AttemptID,
		workerIdentity:     cfg.WorkerIdentity,
		placementRef:       cfg.PlacementRef,
		leaseRef:           cfg.LeaseRef,
		executorRef:        cfg.ExecutorApproval,
		executorDigest:     cfg.ExecutorDigest,
		readGrants:         readGrants,
		writeGrants:        writeGrants,
		limits:             cfg.Limits,
		sandbox:            cfg.Sandbox,
		effectMode:         cfg.ExternalEffectMode,
		nonce:              append([]byte(nil), cfg.Nonce...),
	}, nil
}

func validateRefKind(name string, ref DecisionRef, want authority.DecisionKind) error {
	if !ref.Valid() {
		return fmt.Errorf("%w: %s reference is required", ErrInvalidWorkOrder, name)
	}
	if ref.Kind() != want {
		return fmt.Errorf("%w: %s reference has kind %q, want %q", ErrInvalidWorkOrder, name, ref.Kind(), want)
	}
	return nil
}

func copyGrants(name string, grants []DecisionRef, want authority.DecisionKind, maximum int) ([]DecisionRef, error) {
	if len(grants) > maximum {
		return nil, fmt.Errorf("%w: at most %d %ss are allowed", ErrInvalidWorkOrder, maximum, name)
	}
	out := make([]DecisionRef, 0, len(grants))
	seen := make(map[string]struct{}, len(grants))
	for i, grant := range grants {
		if err := validateRefKind(fmt.Sprintf("%s[%d]", name, i), grant, want); err != nil {
			return nil, err
		}
		key := string(grant.Kind()) + "\x00" + grant.DecisionID()
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: duplicate %s %q", ErrInvalidWorkOrder, name, grant.DecisionID())
		}
		seen[key] = struct{}{}
		out = append(out, grant)
	}
	return out, nil
}

func validateSandbox(policy SandboxPolicy) error {
	if err := validateWorkOrderText("sandbox profile", policy.Profile, 128); err != nil {
		return err
	}
	if policy.NetworkProfile != "" {
		if err := validateWorkOrderText("network profile", policy.NetworkProfile, 128); err != nil {
			return err
		}
	}
	if policy.GPU != nil {
		if policy.GPU.DeviceCount == 0 {
			return fmt.Errorf("%w: GPU device count must be positive when a GPU limit is present", ErrInvalidWorkOrder)
		}
		if policy.GPU.MinimumVRAMBytes == 0 {
			return fmt.Errorf("%w: GPU VRAM reservation must be positive when a GPU limit is present", ErrInvalidWorkOrder)
		}
	}
	return nil
}

func (w WorkOrder) SchemaVersion() uint64                { return WorkOrderSchemaVersion }
func (w WorkOrder) DecisionKind() authority.DecisionKind { return authority.DecisionWorkOrder }
func (w WorkOrder) DecisionID() string                   { return w.decisionID }
func (w WorkOrder) AuthorityRole() authority.Role        { return authority.RoleWorkOrder }
func (w WorkOrder) AuthorityPrincipal() string           { return w.authorityPrincipal }
func (w WorkOrder) IssuedAt() time.Time                  { return w.issuedAt }
func (w WorkOrder) ExpiresAt() time.Time                 { return w.expiresAt }
func (w WorkOrder) PolicyRevision() string               { return w.policyRevision }
func (w WorkOrder) WorkOrderID() string                  { return w.workOrderID }
func (w WorkOrder) TaskID() ids.TaskID                   { return w.taskID }
func (w WorkOrder) TaskIRDigest() contract.Digest        { return w.taskIRDigest }
func (w WorkOrder) AttemptID() ids.AttemptID             { return w.attemptID }
func (w WorkOrder) WorkerIdentity() string               { return w.workerIdentity }
func (w WorkOrder) PlacementRef() DecisionRef            { return w.placementRef }
func (w WorkOrder) LeaseRef() DecisionRef                { return w.leaseRef }
func (w WorkOrder) ExecutorApprovalRef() DecisionRef     { return w.executorRef }
func (w WorkOrder) ExecutorDigest() contract.Digest      { return w.executorDigest }
func (w WorkOrder) ReadGrants() []DecisionRef            { return append([]DecisionRef(nil), w.readGrants...) }
func (w WorkOrder) WriteGrants() []DecisionRef           { return append([]DecisionRef(nil), w.writeGrants...) }
func (w WorkOrder) Limits() ResourceLimits               { return w.limits }
func (w WorkOrder) Sandbox() SandboxPolicy               { return w.sandbox }
func (w WorkOrder) ExternalEffectMode() uint64           { return w.effectMode }
func (w WorkOrder) Nonce() []byte                        { return append([]byte(nil), w.nonce...) }

func (w WorkOrder) Valid() bool {
	if w.decisionID == "" || w.authorityPrincipal == "" ||
		w.issuedAt.IsZero() || !w.issuedAt.Before(w.expiresAt) ||
		w.policyRevision == "" || w.workOrderID == "" ||
		w.taskID == "" || w.taskIRDigest.IsZero() || w.attemptID == "" || w.workerIdentity == "" {
		return false
	}
	if !w.placementRef.Valid() || w.placementRef.Kind() != authority.DecisionPlacementApproval {
		return false
	}
	if !w.leaseRef.Valid() || w.leaseRef.Kind() != authority.DecisionLease {
		return false
	}
	if !w.executorRef.Valid() || w.executorRef.Kind() != authority.DecisionExecutorApproval {
		return false
	}
	if w.executorDigest.IsZero() ||
		w.limits.CPUMillis == 0 || w.limits.WallTimeMillis == 0 ||
		w.sandbox.Profile == "" ||
		w.effectMode != WorkOrderExternalEffectNone ||
		len(w.nonce) < MinWorkOrderNonceBytes || len(w.nonce) > MaxWorkOrderNonceBytes {
		return false
	}
	for _, grant := range w.readGrants {
		if !grant.Valid() || grant.Kind() != authority.DecisionArtifactReadGrant {
			return false
		}
	}
	for _, grant := range w.writeGrants {
		if !grant.Valid() || grant.Kind() != authority.DecisionArtifactWriteGrant {
			return false
		}
	}
	return true
}

// Equal reports whether two WorkOrders carry the same signed semantic content.
func (w WorkOrder) Equal(other WorkOrder) bool {
	if w.decisionID != other.decisionID ||
		w.authorityPrincipal != other.authorityPrincipal ||
		!w.issuedAt.Equal(other.issuedAt) ||
		!w.expiresAt.Equal(other.expiresAt) ||
		w.policyRevision != other.policyRevision ||
		w.workOrderID != other.workOrderID ||
		w.taskID != other.taskID ||
		w.taskIRDigest != other.taskIRDigest ||
		w.attemptID != other.attemptID ||
		w.workerIdentity != other.workerIdentity ||
		!w.placementRef.Equal(other.placementRef) ||
		!w.leaseRef.Equal(other.leaseRef) ||
		!w.executorRef.Equal(other.executorRef) ||
		w.executorDigest != other.executorDigest ||
		w.limits != other.limits ||
		w.effectMode != other.effectMode ||
		len(w.nonce) != len(other.nonce) {
		return false
	}
	for i := range w.nonce {
		if w.nonce[i] != other.nonce[i] {
			return false
		}
	}
	if w.sandbox.Profile != other.sandbox.Profile || w.sandbox.NetworkProfile != other.sandbox.NetworkProfile {
		return false
	}
	if (w.sandbox.GPU == nil) != (other.sandbox.GPU == nil) {
		return false
	}
	if w.sandbox.GPU != nil && *w.sandbox.GPU != *other.sandbox.GPU {
		return false
	}
	if len(w.readGrants) != len(other.readGrants) || len(w.writeGrants) != len(other.writeGrants) {
		return false
	}
	for i := range w.readGrants {
		if !w.readGrants[i].Equal(other.readGrants[i]) {
			return false
		}
	}
	for i := range w.writeGrants {
		if !w.writeGrants[i].Equal(other.writeGrants[i]) {
			return false
		}
	}
	return true
}

func validateWorkOrderText(name, value string, maxBytes int) error {
	if err := validateText(name, value, maxBytes); err != nil {
		return fmt.Errorf("%w: invalid %s", ErrInvalidWorkOrder, name)
	}
	return nil
}
