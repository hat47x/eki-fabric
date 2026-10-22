// Package workorderissue is the WOR-1 signing boundary of the Control Plane.
//
// An Issuer turns one EMITTED Lease into one signed `eki.work-order/v1`
// Decision, or durably refuses to. It is modelled on leaseissue.Emitter:
//
//   - The Lease, its Placement Approval reference, its Lease Decision
//     reference, the Worker, the Task, the Attempt, and the policy revision are
//     read from durable state, never from the request.
//   - Just before the durable commit it re-checks policy epoch, signing key,
//     Trust Bundle, Task, Attempt, and the Lease fence.
//   - The linearization point of new WorkOrder Authority is the EMITTED commit,
//     not the signature. A signature produced before a failed commit is dropped.
//   - EMITTED replays the exact stored bytes and is never re-signed.
//   - A lost premise records ABORTED. ABORTED releases no signed bytes, changes
//     no Task / Attempt / Lease state, and never rewinds FenceEpoch.
//   - A transient failure (unreadable state, unavailable signer, stale Trust
//     Bundle) records nothing, so a retry signs again from scratch.
package workorderissue

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workorderemission"
	"github.com/hat47x/eki-fabric/internal/spi/cosedecoder"
	"github.com/hat47x/eki-fabric/internal/spi/decisiondecoder"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var (
	ErrInvalidConfig  = errors.New("invalid WOR-1 WorkOrder issuer configuration")
	ErrInvalidRequest = errors.New("invalid WOR-1 WorkOrder issue request")
	// ErrWorkOrderAborted means no WorkOrder exists for this Lease and none will
	// be produced. Running the Task again needs a new Placement and Lease.
	ErrWorkOrderAborted = errors.New("WorkOrder aborted before Authority emission")
	// ErrLeaseNotEmitted means the Lease has no EMITTED signed Lease Decision
	// (still RESERVED, or ABORTED). Nothing is recorded.
	ErrLeaseNotEmitted = errors.New("Lease has no EMITTED signed Lease Decision")
	// ErrUnfencedLease means the Lease predates the durable Policy Epoch fence.
	ErrUnfencedLease = errors.New("Lease is not policy fenced")
	// ErrRequestConflict means a WorkOrder is already EMITTED for this Lease
	// with a different Worker, Executor Approval, limits, or sandbox. The
	// stored bytes are not returned for a request they do not answer.
	ErrRequestConflict = errors.New("an EMITTED WorkOrder for this Lease binds a different request")
	// ErrSignedEnvelopeMismatch means the produced envelope did not decode to
	// exactly the payload, key, and algorithm that were signed.
	ErrSignedEnvelopeMismatch = errors.New("signed WorkOrder envelope does not match the signed payload")
	ErrStoreInvariant         = errors.New("State Store returned a WorkOrder record outside the requested binding")
)

// IssueState is the durable state WOR-1 reads and commits.
type IssueState interface {
	LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error)
	LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error)
	LoadCurrentPolicy(ctx context.Context) (policystate.State, error)
	LoadLease(ctx context.Context, id ids.LeaseID) (leasestate.LeaseRecord, error)
	LoadLeaseEmission(ctx context.Context, id ids.LeaseID) (leaseemission.Record, error)
	statestore.WorkOrderEmissionStore
}

// Signer signs a COSE Signature1 structure with the current primary key of
// the requested scope. decisiontrust.ReferenceSoftwareSigner satisfies it.
type Signer interface {
	SignCOSESigStructure(req decisiontrust.COSESigningRequest, now time.Time) (decisiontrust.SignatureResult, error)
}

// SigningKeySource reports the current primary signing key of a scope.
// decisiontrust.Registry satisfies it.
type SigningKeySource interface {
	ResolveSigningKey(req decisiontrust.SigningRequest, now time.Time) (decisiontrust.KeyRecord, error)
}

type TrustBundleSource interface {
	CurrentTrustBundle(ctx context.Context) (decisiontrust.TrustBundleMetadata, error)
}

type Clock interface{ Now() time.Time }

type Config struct {
	State       IssueState
	Signer      Signer
	Keys        SigningKeySource
	Verifier    *decisiontrust.VerifierBoundary
	Payload     decisiondecoder.WorkOrderCodec
	Envelope    cosedecoder.SigningCodec
	TrustBundle TrustBundleSource
	Clock       Clock
	// Nonce is the randomness source for WorkOrder nonces. nil uses
	// crypto/rand.
	Nonce io.Reader

	// TrustDomain and AuthorityPrincipal name this WOR-1 instance. They are
	// configuration, never request input.
	TrustDomain        string
	AuthorityPrincipal string
	Algorithm          decisiontrust.Algorithm
	KeyPolicyRevision  string
	// MaxLifetime bounds a WorkOrder. The signed expiry is the earlier of
	// now+MaxLifetime and the Lease expiry.
	MaxLifetime time.Duration
}

type Issuer struct {
	cfg Config
}

func New(cfg Config) (*Issuer, error) {
	switch {
	case cfg.State == nil:
		return nil, fmt.Errorf("%w: State is required", ErrInvalidConfig)
	case cfg.Signer == nil:
		return nil, fmt.Errorf("%w: WOR-1 signer is required", ErrInvalidConfig)
	case cfg.Keys == nil:
		return nil, fmt.Errorf("%w: signing key source is required", ErrInvalidConfig)
	case cfg.Verifier == nil:
		return nil, fmt.Errorf("%w: signed-decision verifier is required", ErrInvalidConfig)
	case cfg.Payload == nil:
		return nil, fmt.Errorf("%w: WorkOrder payload codec is required", ErrInvalidConfig)
	case cfg.Envelope == nil:
		return nil, fmt.Errorf("%w: COSE signing codec is required", ErrInvalidConfig)
	case cfg.TrustBundle == nil:
		return nil, fmt.Errorf("%w: Trust Bundle source is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: trusted Clock is required", ErrInvalidConfig)
	case cfg.KeyPolicyRevision == "":
		return nil, fmt.Errorf("%w: signing key policy revision is required", ErrInvalidConfig)
	case cfg.MaxLifetime < time.Millisecond:
		return nil, fmt.Errorf("%w: maximum WorkOrder lifetime must be at least 1ms", ErrInvalidConfig)
	}
	if err := cfg.signingRequest().Validate(); err != nil {
		return nil, fmt.Errorf("%w: WOR-1 signing scope: %v", ErrInvalidConfig, err)
	}
	if cfg.Nonce == nil {
		cfg.Nonce = rand.Reader
	}
	return &Issuer{cfg: cfg}, nil
}

func (c Config) signingRequest() decisiontrust.SigningRequest {
	return decisiontrust.SigningRequest{
		TrustDomain:        c.TrustDomain,
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: c.AuthorityPrincipal,
		DecisionKind:       authority.DecisionWorkOrder,
		Algorithm:          c.Algorithm,
	}
}

// ExecutorApproval names the Executor Approval the WorkOrder binds.
//
// 仮決め: there is no EXE-1 issuer or Control Plane verifier yet, so WOR-1
// binds the reference the trusted Control Plane caller supplies. The Worker
// still checks it against its own Executor evidence (workorderaccept).
type ExecutorApproval struct {
	DecisionID     string
	PayloadDigest  contract.Digest
	ExecutorDigest contract.Digest
}

// IssueRequest names an EMITTED Lease and the execution terms to bind. Task,
// Attempt, Worker binding, Placement and Lease references, policy revision,
// and fence come from durable state.
//
// 仮決め: v1 issues WorkOrders without Artifact Grants. There is no DAT-1
// issuer to confirm a grant is current, so WOR-1 refuses to bind one rather
// than sign an unverifiable reference.
type IssueRequest struct {
	LeaseID ids.LeaseID
	// WorkerIdentity is the Worker the caller intends to dispatch to. It must
	// equal the Lease Worker; a mismatch records ABORTED.
	WorkerIdentity   string
	ExecutorApproval ExecutorApproval
	Limits           decisionpayload.ResourceLimits
	Sandbox          decisionpayload.SandboxPolicy
}

type IssueResult struct {
	// Record is the durable EMITTED or ABORTED record.
	Record workorderemission.Record
	// SignedWorkOrder is the exact committed COSE_Sign1 bytes. It is empty
	// unless Record is EMITTED. Only these bytes may be delivered to a Worker.
	SignedWorkOrder []byte
	// Replayed means the record already existed; nothing was signed.
	Replayed bool
}

// WorkOrderDecisionID is the deterministic `eki.work-order/v1` Decision ID for
// a Lease. One Lease yields at most one WorkOrder Decision.
func WorkOrderDecisionID(id ids.LeaseID) string { return "work-order-decision:" + id.String() }

// WorkOrderID is the deterministic WorkOrder ID for a Lease.
func WorkOrderID(id ids.LeaseID) string { return "work-order:" + id.String() }

// Issue signs a WorkOrder for an EMITTED Lease and durably commits it as
// EMITTED, or records ABORTED when the Authority premise is gone.
func (i *Issuer) Issue(ctx context.Context, req IssueRequest) (IssueResult, error) {
	if i == nil || i.cfg.State == nil {
		return IssueResult{}, ErrInvalidConfig
	}
	if ctx == nil {
		return IssueResult{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return IssueResult{}, err
	}
	if err := validateRequest(req); err != nil {
		return IssueResult{}, err
	}

	if result, done, err := i.existing(ctx, req); done {
		return result, err
	}

	lease, err := i.cfg.State.LoadLease(ctx, req.LeaseID)
	if err != nil {
		return IssueResult{}, fmt.Errorf("load Lease %s: %w", req.LeaseID, err)
	}
	if err := lease.Validate(); err != nil {
		return IssueResult{}, fmt.Errorf("%w: %v", ErrStoreInvariant, err)
	}
	if !lease.PolicyFenced() {
		return IssueResult{}, fmt.Errorf("%w: Lease %s", ErrUnfencedLease, lease.ID)
	}
	leaseDecision, err := i.cfg.State.LoadLeaseEmission(ctx, lease.ID)
	switch {
	case errors.Is(err, statestore.ErrNotFound):
		return IssueResult{}, fmt.Errorf("%w: Lease %s is still RESERVED", ErrLeaseNotEmitted, lease.ID)
	case err != nil:
		return IssueResult{}, fmt.Errorf("load Lease emission %s: %w", lease.ID, err)
	case leaseDecision.Status() != leaseemission.StatusEmitted:
		return IssueResult{}, fmt.Errorf("%w: Lease %s is %s", ErrLeaseNotEmitted, lease.ID, leaseDecision.Status())
	case leaseDecision.PolicyEpoch() != lease.PolicyEpoch || leaseDecision.PolicyRevision() != lease.PolicyRevision:
		return IssueResult{}, fmt.Errorf("%w: Lease emission policy differs from the Lease reservation", ErrStoreInvariant)
	}

	now, err := i.now()
	if err != nil {
		return IssueResult{}, err
	}
	if req.WorkerIdentity != lease.WorkerIdentity {
		return i.abort(ctx, req, workorderemission.AbortWorkerMismatch, fmt.Sprintf(
			"requested Worker %q differs from Lease Worker %q", req.WorkerIdentity, lease.WorkerIdentity))
	}
	snapshot, v, err := i.checkCurrent(ctx, lease, now)
	if err != nil {
		return IssueResult{}, err
	}
	if v.abort {
		return i.abort(ctx, req, v.reason, v.detail)
	}

	nonce := make([]byte, decisionpayload.MaxWorkOrderNonceBytes)
	if _, err := io.ReadFull(i.cfg.Nonce, nonce); err != nil {
		return IssueResult{}, fmt.Errorf("generate WorkOrder nonce: %w", err)
	}
	order, err := i.buildPayload(lease, leaseDecision, req, nonce, now)
	if err != nil {
		if errors.Is(err, errNoWindow) {
			return i.abort(ctx, req, workorderemission.AbortLeaseExpired, err.Error())
		}
		return IssueResult{}, err
	}
	payload, err := i.cfg.Payload.EncodeWorkOrder(order)
	if err != nil {
		return IssueResult{}, fmt.Errorf("encode WorkOrder payload: %w", err)
	}
	decoded, err := i.cfg.Payload.DecodeWorkOrder(payload)
	if err != nil || !decoded.Equal(order) {
		return IssueResult{}, fmt.Errorf("%w: canonical WorkOrder payload does not round-trip", ErrSignedEnvelopeMismatch)
	}

	keyReq := i.cfg.signingRequest()
	key, err := i.cfg.Keys.ResolveSigningKey(keyReq, now)
	if err != nil {
		return i.signingFailure(ctx, req, err)
	}
	protected, err := i.cfg.Envelope.EncodeProtectedHeader(key.Algorithm(), key.ID())
	if err != nil {
		return IssueResult{}, fmt.Errorf("encode WorkOrder protected header: %w", err)
	}
	sigStructure, err := i.cfg.Envelope.EncodeSignature1(protected, payload)
	if err != nil {
		return IssueResult{}, fmt.Errorf("encode WorkOrder Signature1: %w", err)
	}
	signature, err := i.cfg.Signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key:            keyReq,
		PolicyRevision: i.cfg.KeyPolicyRevision,
		SigStructure:   sigStructure,
	}, now)
	if err != nil {
		return i.signingFailure(ctx, req, err)
	}
	if signature.KeyID != key.ID() || signature.Algorithm != key.Algorithm() {
		return i.signingFailure(ctx, req, decisiontrust.ErrSigningAuthorizationChanged)
	}
	envelope, err := i.cfg.Envelope.EncodeSign1(protected, payload, signature.Signature)
	if err != nil {
		return IssueResult{}, fmt.Errorf("encode signed WorkOrder envelope: %w", err)
	}

	// The signed bytes are still private. Re-verify them as a relying party
	// would, against the current Trust Bundle and key state.
	if err := i.verifyEnvelope(ctx, envelope, payload, order, key, now); err != nil {
		return i.verificationFailure(ctx, req, err)
	}

	// Last checks before the linearization point. Key and Trust Bundle state
	// live outside the State Store, so this narrows rather than closes their
	// race; the Worker verifier remains the final key enforcement.
	commitAt, err := i.now()
	if err != nil {
		return IssueResult{}, err
	}
	current, err := i.cfg.Keys.ResolveSigningKey(keyReq, commitAt)
	if err != nil {
		return i.signingFailure(ctx, req, err)
	}
	if current.ID() != key.ID() || current.Revision() != key.Revision() {
		return i.signingFailure(ctx, req, decisiontrust.ErrSigningAuthorizationChanged)
	}
	// Re-verify at commit time: Trust Bundle freshness, key state, and the
	// WorkOrder window are judged again against the current Trust Bundle.
	if err := i.verifyEnvelope(ctx, envelope, payload, order, key, commitAt); err != nil {
		return i.verificationFailure(ctx, req, err)
	}
	snapshot, v, err = i.checkCurrent(ctx, lease, commitAt)
	if err != nil {
		return IssueResult{}, err
	}
	if v.abort {
		return i.abort(ctx, req, v.reason, v.detail)
	}
	if !commitAt.Before(order.ExpiresAt()) {
		return i.abort(ctx, req, workorderemission.AbortLeaseExpired, "WorkOrder window closed before commit")
	}

	commit := statestore.WorkOrderEmissionCommit{
		LeaseID:                 lease.ID,
		ExpectedTaskRevision:    snapshot.taskRevision,
		ExpectedAttemptRevision: snapshot.attemptRevision,
		ExpectedPolicyEpoch:     lease.PolicyEpoch,
		ExpectedPolicyRevision:  lease.PolicyRevision,
		ExpectedFenceEpoch:      lease.FenceEpoch,
		LeaseDecisionID:         leaseDecision.DecisionID(),
		LeasePayloadDigest:      leaseDecision.PayloadDigest(),
		WorkerIdentity:          lease.WorkerIdentity,
		DecisionID:              order.DecisionID(),
		WorkOrderID:             order.WorkOrderID(),
		TrustDomain:             i.cfg.TrustDomain,
		AuthorityPrincipal:      i.cfg.AuthorityPrincipal,
		KeyID:                   key.ID(),
		Algorithm:               key.Algorithm(),
		PayloadDigest:           contract.SHA256(payload),
		EnvelopeDigest:          contract.SHA256(envelope),
		SignedEnvelope:          envelope,
		ExpiresAt:               order.ExpiresAt(),
		CommittedAt:             commitAt,
	}
	committed, err := i.cfg.State.CommitWorkOrderEmission(ctx, commit)
	if err == nil {
		if !commit.Matches(committed.Record) {
			return IssueResult{}, ErrStoreInvariant
		}
		return IssueResult{
			Record:          committed.Record,
			SignedWorkOrder: committed.Record.SignedEnvelope(),
			Replayed:        committed.Replayed,
		}, nil
	}
	return i.commitFailure(ctx, req, lease, err)
}

func validateRequest(req IssueRequest) error {
	if _, err := ids.ParseLeaseID(req.LeaseID.String()); err != nil {
		return fmt.Errorf("%w: Lease ID: %v", ErrInvalidRequest, err)
	}
	if req.WorkerIdentity == "" || len(req.WorkerIdentity) > 256 || !utf8.ValidString(req.WorkerIdentity) {
		return fmt.Errorf("%w: Worker identity is required", ErrInvalidRequest)
	}
	if req.ExecutorApproval.DecisionID == "" || len(req.ExecutorApproval.DecisionID) > 128 ||
		!utf8.ValidString(req.ExecutorApproval.DecisionID) {
		return fmt.Errorf("%w: Executor Approval Decision ID is required", ErrInvalidRequest)
	}
	if req.ExecutorApproval.PayloadDigest.IsZero() || req.ExecutorApproval.ExecutorDigest.IsZero() {
		return fmt.Errorf("%w: Executor Approval digests are required", ErrInvalidRequest)
	}
	return nil
}

// existing returns the durable outcome if one is already recorded. An
// EMITTED WorkOrder is returned only to a request it answers.
func (i *Issuer) existing(ctx context.Context, req IssueRequest) (IssueResult, bool, error) {
	record, err := i.cfg.State.LoadWorkOrderEmission(ctx, req.LeaseID)
	switch {
	case errors.Is(err, statestore.ErrNotFound):
		return IssueResult{}, false, nil
	case err != nil:
		return IssueResult{}, true, fmt.Errorf("load WorkOrder emission %s: %w", req.LeaseID, err)
	}
	switch record.Status() {
	case workorderemission.StatusEmitted:
		if err := i.sameRequest(record, req); err != nil {
			return IssueResult{}, true, err
		}
		return IssueResult{Record: record, SignedWorkOrder: record.SignedEnvelope(), Replayed: true}, true, nil
	case workorderemission.StatusAborted:
		return IssueResult{Record: record, Replayed: true}, true,
			fmt.Errorf("%w: %s: %s", ErrWorkOrderAborted, record.AbortReason(), record.Reason())
	default:
		return IssueResult{}, true, ErrStoreInvariant
	}
}

func (i *Issuer) sameRequest(record workorderemission.Record, req IssueRequest) error {
	envelope, err := i.cfg.Envelope.DecodeSign1(record.SignedEnvelope())
	if err != nil {
		return fmt.Errorf("%w: stored WorkOrder does not decode: %v", ErrStoreInvariant, err)
	}
	order, err := i.cfg.Payload.DecodeWorkOrder(envelope.Payload())
	if err != nil {
		return fmt.Errorf("%w: stored WorkOrder payload does not decode: %v", ErrStoreInvariant, err)
	}
	if order.WorkerIdentity() != req.WorkerIdentity ||
		order.ExecutorApprovalRef().DecisionID() != req.ExecutorApproval.DecisionID ||
		order.ExecutorApprovalRef().PayloadDigest() != req.ExecutorApproval.PayloadDigest ||
		order.ExecutorDigest() != req.ExecutorApproval.ExecutorDigest ||
		order.Limits() != req.Limits ||
		!sameSandbox(order.Sandbox(), req.Sandbox) {
		return ErrRequestConflict
	}
	return nil
}

func sameSandbox(a, b decisionpayload.SandboxPolicy) bool {
	if a.Profile != b.Profile || a.NetworkProfile != b.NetworkProfile || (a.GPU == nil) != (b.GPU == nil) {
		return false
	}
	return a.GPU == nil || *a.GPU == *b.GPU
}

func (i *Issuer) now() (time.Time, error) {
	now := i.cfg.Clock.Now().UTC()
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("%w: trusted Clock returned zero time", ErrInvalidConfig)
	}
	return now, nil
}

type currentSnapshot struct {
	taskRevision    uint64
	attemptRevision uint64
}

type verdict struct {
	abort  bool
	reason workorderemission.AbortReason
	detail string
}

func premiseLost(reason workorderemission.AbortReason, detail string) verdict {
	return verdict{abort: true, reason: reason, detail: detail}
}

// checkCurrent re-reads Attempt, Task, and PolicyState for one Lease. A lost
// premise is a verdict to ABORT. A read failure is an error and records
// nothing. The Lease fence is re-checked by the store at commit: while this
// Lease is unexpired and its Attempt holds it, no newer fence can exist.
func (i *Issuer) checkCurrent(ctx context.Context, lease leasestate.LeaseRecord, now time.Time) (currentSnapshot, verdict, error) {
	if !now.Before(lease.ExpiresAt) {
		return currentSnapshot{}, premiseLost(workorderemission.AbortLeaseExpired, "Lease expired before WorkOrder emission"), nil
	}
	if now.Before(lease.IssuedAt) {
		return currentSnapshot{}, verdict{}, fmt.Errorf("%w: trusted Clock precedes Lease issue time", ErrStoreInvariant)
	}

	attempt, err := i.cfg.State.LoadAttempt(ctx, lease.AttemptID)
	if err != nil {
		return currentSnapshot{}, verdict{}, fmt.Errorf("load Lease Attempt %s: %w", lease.AttemptID, err)
	}
	snapshot := currentSnapshot{attemptRevision: attempt.Revision}
	if attempt.Phase != attemptstate.AttemptPhaseAssigned ||
		attempt.LeaseID != lease.ID ||
		attempt.TaskID != lease.TaskID ||
		attempt.TaskIRDigest != lease.TaskIRDigest ||
		attempt.WorkerIdentity != lease.WorkerIdentity {
		// 仮決め: only an ASSIGNED Attempt that still holds this Lease may
		// receive a first WorkOrder. Any later or closed phase is treated as a
		// superseded Lease (fail closed).
		return snapshot, premiseLost(workorderemission.AbortLeaseSuperseded, fmt.Sprintf(
			"Attempt %s phase %q no longer holds Lease %s as ASSIGNED", attempt.ID, attempt.Phase, lease.ID)), nil
	}

	task, err := i.cfg.State.LoadTask(ctx, lease.TaskID)
	if err != nil {
		return snapshot, verdict{}, fmt.Errorf("load Lease Task %s: %w", lease.TaskID, err)
	}
	snapshot.taskRevision = task.Revision
	switch {
	case task.CancellationRequested:
		return snapshot, premiseLost(workorderemission.AbortTaskCancelled, "Task cancelled before WorkOrder emission"), nil
	case task.TaskIRDigest != lease.TaskIRDigest:
		return snapshot, premiseLost(workorderemission.AbortTaskNotSchedulable, "Task TaskIR no longer matches the Lease"), nil
	case task.Phase != taskstate.TaskPhaseReady && task.Phase != taskstate.TaskPhaseActive:
		return snapshot, premiseLost(workorderemission.AbortTaskNotSchedulable, fmt.Sprintf("Task phase %q cannot receive a WorkOrder", task.Phase)), nil
	}

	policy, err := i.cfg.State.LoadCurrentPolicy(ctx)
	if err != nil {
		return snapshot, verdict{}, fmt.Errorf("read current durable policy state: %w", err)
	}
	if policy.Epoch != lease.PolicyEpoch ||
		policy.Revision != lease.PolicyRevision ||
		policy.Status != policystate.StatusActive {
		// #160 / #78: a newer Policy Epoch ends new Authority for a Lease fenced
		// under an older one, even if the old revision was not revoked.
		return snapshot, premiseLost(workorderemission.AbortPolicyRevoked, fmt.Sprintf(
			"policy %s epoch %d is no longer current (current %s epoch %d %s)",
			lease.PolicyRevision, lease.PolicyEpoch, policy.Revision, policy.Epoch, policy.Status,
		)), nil
	}
	return snapshot, verdict{}, nil
}

var errNoWindow = errors.New("no positive millisecond WorkOrder window remains")

func (i *Issuer) buildPayload(
	lease leasestate.LeaseRecord,
	leaseDecision leaseemission.Record,
	req IssueRequest,
	nonce []byte,
	now time.Time,
) (decisionpayload.WorkOrder, error) {
	// The wire carries milliseconds. Round the window inward so the signed
	// WorkOrder never claims time outside the Lease or the local maximum.
	issuedAt := now.UTC().Truncate(time.Millisecond)
	if issuedAt.Before(now) {
		issuedAt = issuedAt.Add(time.Millisecond)
	}
	expiresAt := now.Add(i.cfg.MaxLifetime)
	if lease.ExpiresAt.Before(expiresAt) {
		expiresAt = lease.ExpiresAt
	}
	expiresAt = expiresAt.UTC().Truncate(time.Millisecond)
	if !issuedAt.Before(expiresAt) {
		return decisionpayload.WorkOrder{}, errNoWindow
	}
	ref := func(kind authority.DecisionKind, id string, digest contract.Digest) (decisionpayload.DecisionRef, error) {
		return decisionpayload.NewDecisionRef(decisionpayload.DecisionRefConfig{Kind: kind, DecisionID: id, PayloadDigest: digest})
	}
	placementRef, err := ref(authority.DecisionPlacementApproval, lease.PlacementDecisionID, lease.PlacementPayloadDigest)
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: Placement reference: %v", ErrStoreInvariant, err)
	}
	leaseRef, err := ref(authority.DecisionLease, leaseDecision.DecisionID(), leaseDecision.PayloadDigest())
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: Lease reference: %v", ErrStoreInvariant, err)
	}
	executorRef, err := ref(authority.DecisionExecutorApproval, req.ExecutorApproval.DecisionID, req.ExecutorApproval.PayloadDigest)
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: Executor Approval reference: %v", ErrInvalidRequest, err)
	}
	order, err := decisionpayload.NewWorkOrder(decisionpayload.WorkOrderConfig{
		SchemaVersion:      decisionpayload.WorkOrderSchemaVersion,
		DecisionKind:       authority.DecisionWorkOrder,
		DecisionID:         WorkOrderDecisionID(lease.ID),
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: i.cfg.AuthorityPrincipal,
		IssuedAt:           issuedAt,
		ExpiresAt:          expiresAt,
		PolicyRevision:     lease.PolicyRevision,
		WorkOrderID:        WorkOrderID(lease.ID),
		TaskID:             lease.TaskID,
		TaskIRDigest:       lease.TaskIRDigest,
		AttemptID:          lease.AttemptID,
		WorkerIdentity:     lease.WorkerIdentity,
		PlacementRef:       placementRef,
		LeaseRef:           leaseRef,
		ExecutorApproval:   executorRef,
		ExecutorDigest:     req.ExecutorApproval.ExecutorDigest,
		Limits:             req.Limits,
		Sandbox:            req.Sandbox,
		ExternalEffectMode: decisionpayload.WorkOrderExternalEffectNone,
		Nonce:              nonce,
	})
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return order, nil
}

func (i *Issuer) verifyEnvelope(
	ctx context.Context,
	envelope, payload []byte,
	want decisionpayload.WorkOrder,
	key decisiontrust.KeyRecord,
	now time.Time,
) error {
	decoded, err := i.cfg.Envelope.DecodeSign1(envelope)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignedEnvelopeMismatch, err)
	}
	if !bytes.Equal(decoded.Payload(), payload) ||
		decoded.KeyID() != key.ID() ||
		decoded.Algorithm() != key.Algorithm() {
		return ErrSignedEnvelopeMismatch
	}
	got, err := i.cfg.Payload.DecodeWorkOrder(decoded.Payload())
	if err != nil || !got.Equal(want) {
		return ErrSignedEnvelopeMismatch
	}
	sigStructure, err := i.cfg.Envelope.EncodeSignature1(decoded.ProtectedHeader(), decoded.Payload())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignedEnvelopeMismatch, err)
	}
	bundle, err := i.cfg.TrustBundle.CurrentTrustBundle(ctx)
	if err != nil {
		return fmt.Errorf("read current Trust Bundle: %w", err)
	}
	_, err = i.cfg.Verifier.VerifyCryptographicSignature(decisiontrust.CryptographicVerificationRequest{
		PreCrypto: decisiontrust.PreCryptoVerificationRequest{
			Key: decisiontrust.VerificationRequest{
				KeyID:              decoded.KeyID(),
				TrustDomain:        i.cfg.TrustDomain,
				AuthorityRole:      authority.RoleWorkOrder,
				AuthorityPrincipal: i.cfg.AuthorityPrincipal,
				DecisionKind:       authority.DecisionWorkOrder,
				ProtectedAlgorithm: decoded.Algorithm(),
				IssuedAt:           want.IssuedAt(),
			},
			ExpiresAt:   want.ExpiresAt(),
			Now:         now,
			TrustBundle: bundle,
		},
		SigStructure: sigStructure,
		Signature:    decoded.Signature(),
	})
	return err
}

// signingFailure separates a lost signing authorization (ABORT) from an
// unavailable signer (record nothing and retry).
func (i *Issuer) signingFailure(ctx context.Context, req IssueRequest, cause error) (IssueResult, error) {
	switch {
	case errors.Is(cause, decisiontrust.ErrNoSigningKey),
		errors.Is(cause, decisiontrust.ErrSigningAuthorizationChanged),
		errors.Is(cause, decisiontrust.ErrSigningPolicyMismatch):
		return i.abort(ctx, req, workorderemission.AbortKeyUnauthorized,
			"WOR-1 signing key is no longer authorized: "+cause.Error())
	default:
		return IssueResult{}, fmt.Errorf("sign WorkOrder for Lease %s: %w", req.LeaseID, cause)
	}
}

// verificationFailure decides what a failed self-verification means. A stale
// or unreadable Trust Bundle is transient. A key the relying side would
// reject, or a window that has closed, ends this WorkOrder.
func (i *Issuer) verificationFailure(ctx context.Context, req IssueRequest, cause error) (IssueResult, error) {
	reason, ok := decisiontrust.FailureReason(cause)
	if !ok {
		return IssueResult{}, fmt.Errorf("verify signed WorkOrder for Lease %s: %w", req.LeaseID, cause)
	}
	switch reason {
	case decisiontrust.ReasonUnknownKey,
		decisiontrust.ReasonKeyNotActiveForVerify,
		decisiontrust.ReasonSubjectMismatch,
		decisiontrust.ReasonAuthorityRoleMismatch,
		decisiontrust.ReasonKeyAlgorithmMismatch,
		decisiontrust.ReasonAlgorithmNotAllowed:
		return i.abort(ctx, req, workorderemission.AbortKeyUnauthorized,
			"signed WorkOrder would not verify under the current key state: "+string(reason))
	case decisiontrust.ReasonExpired, decisiontrust.ReasonStaleDecision:
		return i.abort(ctx, req, workorderemission.AbortLeaseExpired,
			"signed WorkOrder window is not acceptable: "+string(reason))
	default:
		// Includes ReasonTrustBundleInvalid / ReasonTrustBundleStale.
		return IssueResult{}, fmt.Errorf("verify signed WorkOrder for Lease %s: %w", req.LeaseID, cause)
	}
}

// commitFailure handles a refused EMITTED commit. The private signed bytes
// are dropped in every branch here.
func (i *Issuer) commitFailure(ctx context.Context, req IssueRequest, lease leasestate.LeaseRecord, cause error) (IssueResult, error) {
	switch {
	case errors.Is(cause, statestore.ErrWorkOrderEmissionConflict),
		errors.Is(cause, statestore.ErrWorkOrderEmissionAborted):
		// Another issuer linearized first. Return its durable outcome.
		if result, done, err := i.existing(ctx, req); done {
			return result, err
		}
		return IssueResult{}, fmt.Errorf("commit WorkOrder for Lease %s: %w", lease.ID, cause)
	case errors.Is(cause, statestore.ErrPolicyChanged),
		errors.Is(cause, statestore.ErrConflict):
		// The store saw drift after our last read. Re-evaluate and abort if
		// the premise is gone; otherwise record nothing.
		now, err := i.now()
		if err != nil {
			return IssueResult{}, err
		}
		_, v, err := i.checkCurrent(ctx, lease, now)
		if err != nil {
			return IssueResult{}, errors.Join(cause, err)
		}
		if v.abort {
			return i.abort(ctx, req, v.reason, v.detail)
		}
		return IssueResult{}, fmt.Errorf("commit WorkOrder for Lease %s: %w", lease.ID, cause)
	default:
		return IssueResult{}, fmt.Errorf("commit WorkOrder for Lease %s: %w", lease.ID, cause)
	}
}

// abort durably records ABORTED. It changes no other state. If an EMITTED
// commit won the race, that durable outcome is returned instead.
func (i *Issuer) abort(ctx context.Context, req IssueRequest, reason workorderemission.AbortReason, detail string) (IssueResult, error) {
	at, err := i.now()
	if err != nil {
		return IssueResult{}, err
	}
	aborted, err := i.cfg.State.AbortWorkOrderEmission(ctx, statestore.WorkOrderEmissionAbort{
		LeaseID:     req.LeaseID,
		AbortReason: reason,
		Reason:      truncateUTF8(detail, 512),
		CommittedAt: at,
	})
	if err != nil {
		if result, done, existingErr := i.existing(ctx, req); done {
			return result, existingErr
		}
		return IssueResult{}, fmt.Errorf("abort WorkOrder for Lease %s (%s): %w", req.LeaseID, detail, err)
	}
	return IssueResult{Record: aborted.Record, Replayed: aborted.Replayed},
		fmt.Errorf("%w: %s: %s", ErrWorkOrderAborted, aborted.Record.AbortReason(), aborted.Record.Reason())
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
