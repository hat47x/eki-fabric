package leaseissue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	"github.com/hat47x/eki-fabric/internal/spi/cosedecoder"
	"github.com/hat47x/eki-fabric/internal/spi/decisiondecoder"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var (
	// ErrLeaseAborted means the reservation is durably ABORTED. No signed
	// Lease exists for it and none will be produced; the Attempt is closed and
	// re-placement needs a new Placement Decision, Lease, and FenceEpoch.
	ErrLeaseAborted = errors.New("Lease reservation aborted before Authority emission")
	// ErrUnfencedLease means the reservation predates the durable Policy
	// Epoch fence and can never become signed Lease Authority.
	ErrUnfencedLease = errors.New("Lease reservation is not policy fenced")
	// ErrSignedEnvelopeMismatch means the produced envelope did not decode to
	// exactly the payload, key, and algorithm that were signed.
	ErrSignedEnvelopeMismatch = errors.New("signed Lease envelope does not match the signed payload")
)

// EmissionState is the durable state LSE-1 reads and commits while turning a
// RESERVED Lease into an EMITTED or ABORTED record.
type EmissionState interface {
	LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error)
	LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error)
	LoadCurrentPolicy(ctx context.Context) (policystate.State, error)
	LoadLease(ctx context.Context, id ids.LeaseID) (leasestate.LeaseRecord, error)
	statestore.LeaseEmissionStore
}

// LeaseSigner signs a COSE Signature1 structure with the current primary
// key of the requested scope. decisiontrust.ReferenceSoftwareSigner satisfies
// it and re-checks key state before and after producing the signature.
type LeaseSigner interface {
	SignCOSESigStructure(req decisiontrust.COSESigningRequest, now time.Time) (decisiontrust.SignatureResult, error)
}

// SigningKeySource reports the current primary signing key of a scope.
// decisiontrust.Registry satisfies it.
type SigningKeySource interface {
	ResolveSigningKey(req decisiontrust.SigningRequest, now time.Time) (decisiontrust.KeyRecord, error)
}

type EmitterConfig struct {
	State       EmissionState
	Signer      LeaseSigner
	Keys        SigningKeySource
	Verifier    *decisiontrust.VerifierBoundary
	Payload     decisiondecoder.LeaseCodec
	Envelope    cosedecoder.SigningCodec
	TrustBundle TrustBundleSource
	Clock       Clock

	// TrustDomain and AuthorityPrincipal name this LSE-1 instance. They are
	// configuration, never request input.
	TrustDomain        string
	AuthorityPrincipal string
	Algorithm          decisiontrust.Algorithm
	// KeyPolicyRevision is the key-management policy revision the LSE-1
	// signing key was registered under (decisiontrust.KeyConfig).
	KeyPolicyRevision string
}

// Emitter is the LSE-1 signing boundary. It turns one durable RESERVED Lease
// into worker-facing signed Authority, or durably refuses to.
type Emitter struct {
	cfg EmitterConfig
}

func NewEmitter(cfg EmitterConfig) (*Emitter, error) {
	switch {
	case cfg.State == nil:
		return nil, fmt.Errorf("%w: State is required", ErrInvalidConfig)
	case cfg.Signer == nil:
		return nil, fmt.Errorf("%w: LSE-1 signer is required", ErrInvalidConfig)
	case cfg.Keys == nil:
		return nil, fmt.Errorf("%w: signing key source is required", ErrInvalidConfig)
	case cfg.Verifier == nil:
		return nil, fmt.Errorf("%w: signed-decision verifier is required", ErrInvalidConfig)
	case cfg.Payload == nil:
		return nil, fmt.Errorf("%w: Lease payload codec is required", ErrInvalidConfig)
	case cfg.Envelope == nil:
		return nil, fmt.Errorf("%w: COSE signing codec is required", ErrInvalidConfig)
	case cfg.TrustBundle == nil:
		return nil, fmt.Errorf("%w: Trust Bundle source is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: trusted Clock is required", ErrInvalidConfig)
	case cfg.KeyPolicyRevision == "":
		return nil, fmt.Errorf("%w: signing key policy revision is required", ErrInvalidConfig)
	}
	if err := cfg.signingRequest().Validate(); err != nil {
		return nil, fmt.Errorf("%w: LSE-1 signing scope: %v", ErrInvalidConfig, err)
	}
	return &Emitter{cfg: cfg}, nil
}

func (c EmitterConfig) signingRequest() decisiontrust.SigningRequest {
	return decisiontrust.SigningRequest{
		TrustDomain:        c.TrustDomain,
		AuthorityRole:      authority.RoleLease,
		AuthorityPrincipal: c.AuthorityPrincipal,
		DecisionKind:       authority.DecisionLease,
		Algorithm:          c.Algorithm,
	}
}

type EmitRequest struct {
	LeaseID ids.LeaseID
}

type EmitResult struct {
	// Record is the durable EMITTED or ABORTED record.
	Record leaseemission.Record
	// SignedLease is the exact committed COSE_Sign1 bytes. It is empty unless
	// Record is EMITTED. Only these bytes may be delivered to a Worker.
	SignedLease []byte
	// Replayed means the record already existed; nothing was signed.
	Replayed bool
}

// LeaseDecisionID is the deterministic `eki.lease/v1` Decision ID of a Lease
// reservation. A retry after a crash re-signs the same Decision, so it can
// never mint a second Lease Decision for one reservation.
func LeaseDecisionID(id ids.LeaseID) string {
	return "lease-decision:" + id.String()
}

// Emit signs a RESERVED Lease and durably commits it as EMITTED, or records
// ABORTED when the Authority premise is gone.
//
// The linearization point of new Lease Authority is the EMITTED commit, not
// the signature. A signature produced before a failed commit is discarded.
//
// Outcomes:
//   - EMITTED already exists: the stored bytes are returned unchanged and
//     nothing is signed again.
//   - The premise is lost (policy revoked or replaced, Task cancelled or not
//     schedulable, Lease expired, signing key no longer authorized or
//     verifiable): ABORTED is committed, the Attempt is closed, and the
//     error wraps ErrLeaseAborted. FenceEpoch is not rewound.
//   - Transient failure (state, signer, or Trust Bundle unavailable or stale):
//     an error is returned and the reservation stays RESERVED, so a retry
//     signs the same canonical payload.
func (e *Emitter) Emit(ctx context.Context, req EmitRequest) (EmitResult, error) {
	if e == nil || e.cfg.State == nil {
		return EmitResult{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return EmitResult{}, err
	}
	if _, err := ids.ParseLeaseID(req.LeaseID.String()); err != nil {
		return EmitResult{}, fmt.Errorf("%w: Lease ID: %v", ErrInvalidRequest, err)
	}

	if result, done, err := e.existing(ctx, req.LeaseID); done {
		return result, err
	}

	lease, err := e.cfg.State.LoadLease(ctx, req.LeaseID)
	if err != nil {
		return EmitResult{}, fmt.Errorf("load Lease reservation %s: %w", req.LeaseID, err)
	}
	if err := lease.Validate(); err != nil {
		return EmitResult{}, fmt.Errorf("%w: %v", ErrStoreInvariant, err)
	}
	if !lease.PolicyFenced() {
		return EmitResult{}, fmt.Errorf("%w: Lease %s", ErrUnfencedLease, lease.ID)
	}

	now, err := e.now()
	if err != nil {
		return EmitResult{}, err
	}
	snapshot, verdict, err := e.checkCurrent(ctx, lease, now)
	if err != nil {
		return EmitResult{}, err
	}
	if verdict.abort {
		return e.abort(ctx, lease, verdict)
	}

	payloadValue, err := e.buildPayload(lease)
	if err != nil {
		return e.abort(ctx, lease, premiseLost(attemptstate.CloseReasonLeaseExpired, "no positive millisecond Lease window: "+err.Error()))
	}
	payload, err := e.cfg.Payload.EncodeLease(payloadValue)
	if err != nil {
		return EmitResult{}, fmt.Errorf("encode Lease payload: %w", err)
	}
	decoded, err := e.cfg.Payload.DecodeLease(payload)
	if err != nil || !decoded.Equal(payloadValue) {
		return EmitResult{}, fmt.Errorf("%w: canonical Lease payload does not round-trip", ErrSignedEnvelopeMismatch)
	}

	keyReq := e.cfg.signingRequest()
	key, err := e.cfg.Keys.ResolveSigningKey(keyReq, now)
	if err != nil {
		return e.signingFailure(ctx, lease, err)
	}
	protected, err := e.cfg.Envelope.EncodeProtectedHeader(key.Algorithm(), key.ID())
	if err != nil {
		return EmitResult{}, fmt.Errorf("encode Lease protected header: %w", err)
	}
	sigStructure, err := e.cfg.Envelope.EncodeSignature1(protected, payload)
	if err != nil {
		return EmitResult{}, fmt.Errorf("encode Lease Signature1: %w", err)
	}
	signature, err := e.cfg.Signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key:            keyReq,
		PolicyRevision: e.cfg.KeyPolicyRevision,
		SigStructure:   sigStructure,
	}, now)
	if err != nil {
		return e.signingFailure(ctx, lease, err)
	}
	if signature.KeyID != key.ID() || signature.Algorithm != key.Algorithm() {
		return e.signingFailure(ctx, lease, decisiontrust.ErrSigningAuthorizationChanged)
	}
	envelope, err := e.cfg.Envelope.EncodeSign1(protected, payload, signature.Signature)
	if err != nil {
		return EmitResult{}, fmt.Errorf("encode signed Lease envelope: %w", err)
	}

	// The signed bytes are still private. Re-verify them as a relying party
	// would, against the current Trust Bundle and key state.
	if err := e.verifyEnvelope(ctx, envelope, payload, payloadValue, key, now); err != nil {
		return e.verificationFailure(ctx, lease, err)
	}

	// Last checks before the linearization point. Key and Trust Bundle state
	// live outside the State Store, so this narrows rather than closes their
	// race; the Worker verifier remains the final key enforcement.
	commitAt, err := e.now()
	if err != nil {
		return EmitResult{}, err
	}
	current, err := e.cfg.Keys.ResolveSigningKey(keyReq, commitAt)
	if err != nil {
		return e.signingFailure(ctx, lease, err)
	}
	if current.ID() != key.ID() || current.Revision() != key.Revision() {
		return e.signingFailure(ctx, lease, decisiontrust.ErrSigningAuthorizationChanged)
	}
	snapshot, verdict, err = e.checkCurrent(ctx, lease, commitAt)
	if err != nil {
		return EmitResult{}, err
	}
	if verdict.abort {
		return e.abort(ctx, lease, verdict)
	}

	commit := statestore.LeaseEmissionCommit{
		LeaseID:                 lease.ID,
		ExpectedTaskRevision:    snapshot.taskRevision,
		ExpectedAttemptRevision: snapshot.attemptRevision,
		ExpectedPolicyEpoch:     lease.PolicyEpoch,
		DecisionID:              payloadValue.DecisionID(),
		TrustDomain:             e.cfg.TrustDomain,
		AuthorityPrincipal:      e.cfg.AuthorityPrincipal,
		KeyID:                   key.ID(),
		Algorithm:               key.Algorithm(),
		PayloadDigest:           contract.SHA256(payload),
		EnvelopeDigest:          contract.SHA256(envelope),
		SignedEnvelope:          envelope,
		CommittedAt:             commitAt,
	}
	committed, err := e.cfg.State.CommitLeaseEmission(ctx, commit)
	if err == nil {
		if !commit.Matches(committed.Record) {
			return EmitResult{}, ErrStoreInvariant
		}
		return EmitResult{
			Record:      committed.Record,
			SignedLease: committed.Record.SignedEnvelope(),
			Replayed:    committed.Replayed,
		}, nil
	}
	return e.commitFailure(ctx, lease, err)
}

// existing returns the durable outcome if one is already recorded.
func (e *Emitter) existing(ctx context.Context, id ids.LeaseID) (EmitResult, bool, error) {
	record, err := e.cfg.State.LoadLeaseEmission(ctx, id)
	switch {
	case errors.Is(err, statestore.ErrNotFound):
		return EmitResult{}, false, nil
	case err != nil:
		return EmitResult{}, true, fmt.Errorf("load Lease emission %s: %w", id, err)
	}
	switch record.Status() {
	case leaseemission.StatusEmitted:
		return EmitResult{Record: record, SignedLease: record.SignedEnvelope(), Replayed: true}, true, nil
	case leaseemission.StatusAborted:
		return EmitResult{Record: record, Replayed: true}, true,
			fmt.Errorf("%w: %s: %s", ErrLeaseAborted, record.CloseReason(), record.Reason())
	default:
		return EmitResult{}, true, ErrStoreInvariant
	}
}

func (e *Emitter) now() (time.Time, error) {
	now := e.cfg.Clock.Now().UTC()
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
	abort       bool
	closeReason attemptstate.CloseReason
	reason      string
}

func premiseLost(reason attemptstate.CloseReason, detail string) verdict {
	return verdict{abort: true, closeReason: reason, reason: detail}
}

// checkCurrent re-reads Task, Attempt, and PolicyState for one reservation.
// A lost premise is a verdict to ABORT. A read failure or an Attempt that no
// longer holds the reservation is an error: the store would refuse an ABORT
// that does not own the Attempt, and nothing can be emitted for it either.
func (e *Emitter) checkCurrent(ctx context.Context, lease leasestate.LeaseRecord, now time.Time) (currentSnapshot, verdict, error) {
	attempt, err := e.cfg.State.LoadAttempt(ctx, lease.AttemptID)
	if err != nil {
		return currentSnapshot{}, verdict{}, fmt.Errorf("load Lease Attempt %s: %w", lease.AttemptID, err)
	}
	if attempt.Phase != attemptstate.AttemptPhaseAssigned ||
		attempt.LeaseID != lease.ID ||
		attempt.TaskID != lease.TaskID ||
		attempt.TaskIRDigest != lease.TaskIRDigest ||
		attempt.WorkerIdentity != lease.WorkerIdentity {
		return currentSnapshot{}, verdict{}, fmt.Errorf(
			"%w: Attempt %s phase %q no longer holds RESERVED Lease %s",
			ErrCurrentState, attempt.ID, attempt.Phase, lease.ID,
		)
	}
	snapshot := currentSnapshot{attemptRevision: attempt.Revision}

	if !now.Before(lease.ExpiresAt) {
		return snapshot, premiseLost(attemptstate.CloseReasonLeaseExpired, "Lease expired before Authority emission"), nil
	}
	if now.Before(lease.IssuedAt) {
		return snapshot, verdict{}, fmt.Errorf("%w: trusted Clock precedes Lease issue time", ErrCurrentState)
	}

	task, err := e.cfg.State.LoadTask(ctx, lease.TaskID)
	if err != nil {
		return snapshot, verdict{}, fmt.Errorf("load Lease Task %s: %w", lease.TaskID, err)
	}
	snapshot.taskRevision = task.Revision
	switch {
	case task.CancellationRequested:
		return snapshot, premiseLost(attemptstate.CloseReasonCancelled, "Task cancelled before Authority emission"), nil
	case task.TaskIRDigest != lease.TaskIRDigest:
		return snapshot, premiseLost(attemptstate.CloseReasonStaleAttempt, "Task TaskIR no longer matches the reservation"), nil
	case task.Phase != taskstate.TaskPhaseReady && task.Phase != taskstate.TaskPhaseActive:
		return snapshot, premiseLost(attemptstate.CloseReasonStaleAttempt, fmt.Sprintf("Task phase %q cannot receive Lease Authority", task.Phase)), nil
	}

	policy, err := e.cfg.State.LoadCurrentPolicy(ctx)
	if err != nil {
		// Unavailable policy fails closed without deciding anything durable.
		return snapshot, verdict{}, fmt.Errorf("read current durable policy state: %w", err)
	}
	if policy.Epoch != lease.PolicyEpoch ||
		policy.Revision != lease.PolicyRevision ||
		policy.Status != policystate.StatusActive {
		return snapshot, premiseLost(attemptstate.CloseReasonPolicyRevoked, fmt.Sprintf(
			"policy %s epoch %d is no longer current (current %s epoch %d %s)",
			lease.PolicyRevision, lease.PolicyEpoch, policy.Revision, policy.Epoch, policy.Status,
		)), nil
	}
	return snapshot, verdict{}, nil
}

func (e *Emitter) buildPayload(lease leasestate.LeaseRecord) (decisionpayload.Lease, error) {
	// The wire carries milliseconds. Round the window inward so the signed
	// Lease never claims time outside the durable reservation.
	issuedAt := lease.IssuedAt.UTC().Truncate(time.Millisecond)
	if issuedAt.Before(lease.IssuedAt) {
		issuedAt = issuedAt.Add(time.Millisecond)
	}
	expiresAt := lease.ExpiresAt.UTC().Truncate(time.Millisecond)
	return decisionpayload.NewLease(decisionpayload.LeaseConfig{
		SchemaVersion:      decisionpayload.LeaseSchemaVersion,
		DecisionKind:       authority.DecisionLease,
		DecisionID:         LeaseDecisionID(lease.ID),
		AuthorityRole:      authority.RoleLease,
		AuthorityPrincipal: e.cfg.AuthorityPrincipal,
		IssuedAt:           issuedAt,
		ExpiresAt:          expiresAt,
		PolicyRevision:     lease.PolicyRevision,
		TaskID:             lease.TaskID,
		TaskIRDigest:       lease.TaskIRDigest,
		AttemptID:          lease.AttemptID,
		WorkerIdentity:     lease.WorkerIdentity,
		LeaseID:            lease.ID,
		FenceEpoch:         lease.FenceEpoch,
		RenewalSequence:    lease.RenewalSequence,
	})
}

func (e *Emitter) verifyEnvelope(
	ctx context.Context,
	envelope, payload []byte,
	want decisionpayload.Lease,
	key decisiontrust.KeyRecord,
	now time.Time,
) error {
	decoded, err := e.cfg.Envelope.DecodeSign1(envelope)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignedEnvelopeMismatch, err)
	}
	if !bytes.Equal(decoded.Payload(), payload) ||
		decoded.KeyID() != key.ID() ||
		decoded.Algorithm() != key.Algorithm() {
		return ErrSignedEnvelopeMismatch
	}
	got, err := e.cfg.Payload.DecodeLease(decoded.Payload())
	if err != nil || !got.Equal(want) {
		return ErrSignedEnvelopeMismatch
	}
	sigStructure, err := e.cfg.Envelope.EncodeSignature1(decoded.ProtectedHeader(), decoded.Payload())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignedEnvelopeMismatch, err)
	}
	bundle, err := e.cfg.TrustBundle.CurrentTrustBundle(ctx)
	if err != nil {
		return fmt.Errorf("read current Trust Bundle: %w", err)
	}
	_, err = e.cfg.Verifier.VerifyCryptographicSignature(decisiontrust.CryptographicVerificationRequest{
		PreCrypto: decisiontrust.PreCryptoVerificationRequest{
			Key: decisiontrust.VerificationRequest{
				KeyID:              decoded.KeyID(),
				TrustDomain:        e.cfg.TrustDomain,
				AuthorityRole:      authority.RoleLease,
				AuthorityPrincipal: e.cfg.AuthorityPrincipal,
				DecisionKind:       authority.DecisionLease,
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
// unavailable signer (keep RESERVED and retry).
func (e *Emitter) signingFailure(ctx context.Context, lease leasestate.LeaseRecord, cause error) (EmitResult, error) {
	switch {
	case errors.Is(cause, decisiontrust.ErrNoSigningKey),
		errors.Is(cause, decisiontrust.ErrSigningAuthorizationChanged),
		errors.Is(cause, decisiontrust.ErrSigningPolicyMismatch):
		return e.abort(ctx, lease, premiseLost(
			attemptstate.CloseReasonPolicyRevoked,
			"LSE-1 signing key is no longer authorized: "+cause.Error(),
		))
	default:
		return EmitResult{}, fmt.Errorf("sign Lease %s: %w", lease.ID, cause)
	}
}

// verificationFailure decides what a failed self-verification means. A stale
// or unreadable Trust Bundle is transient. A key that the relying side would
// reject, or a window that has closed, ends this reservation.
func (e *Emitter) verificationFailure(ctx context.Context, lease leasestate.LeaseRecord, cause error) (EmitResult, error) {
	reason, ok := decisiontrust.FailureReason(cause)
	if !ok {
		return EmitResult{}, fmt.Errorf("verify signed Lease %s: %w", lease.ID, cause)
	}
	switch reason {
	case decisiontrust.ReasonTrustBundleInvalid, decisiontrust.ReasonTrustBundleStale:
		return EmitResult{}, fmt.Errorf("verify signed Lease %s: %w", lease.ID, cause)
	case decisiontrust.ReasonUnknownKey,
		decisiontrust.ReasonKeyNotActiveForVerify,
		decisiontrust.ReasonSubjectMismatch,
		decisiontrust.ReasonAuthorityRoleMismatch,
		decisiontrust.ReasonKeyAlgorithmMismatch,
		decisiontrust.ReasonAlgorithmNotAllowed:
		return e.abort(ctx, lease, premiseLost(
			attemptstate.CloseReasonPolicyRevoked,
			"signed Lease would not verify under the current key state: "+string(reason),
		))
	case decisiontrust.ReasonExpired, decisiontrust.ReasonStaleDecision:
		return e.abort(ctx, lease, premiseLost(
			attemptstate.CloseReasonLeaseExpired,
			"signed Lease window is not acceptable: "+string(reason),
		))
	default:
		return EmitResult{}, fmt.Errorf("verify signed Lease %s: %w", lease.ID, cause)
	}
}

// commitFailure handles a refused EMITTED commit. The private signed bytes
// are dropped in every branch here.
func (e *Emitter) commitFailure(ctx context.Context, lease leasestate.LeaseRecord, cause error) (EmitResult, error) {
	switch {
	case errors.Is(cause, statestore.ErrLeaseEmissionConflict),
		errors.Is(cause, statestore.ErrLeaseEmissionAborted):
		// Another emitter linearized first. Return its durable outcome.
		if result, done, err := e.existing(ctx, lease.ID); done {
			return result, err
		}
		return EmitResult{}, fmt.Errorf("commit Lease emission %s: %w", lease.ID, cause)
	case errors.Is(cause, statestore.ErrPolicyChanged),
		errors.Is(cause, statestore.ErrConflict):
		// The store saw drift after our last read. Re-evaluate and abort if
		// the premise is gone; otherwise the reservation stays RESERVED.
		now, err := e.now()
		if err != nil {
			return EmitResult{}, err
		}
		_, verdict, err := e.checkCurrent(ctx, lease, now)
		if err != nil {
			if result, done, existingErr := e.existing(ctx, lease.ID); done {
				return result, existingErr
			}
			return EmitResult{}, errors.Join(cause, err)
		}
		if verdict.abort {
			return e.abort(ctx, lease, verdict)
		}
		return EmitResult{}, fmt.Errorf("commit Lease emission %s: %w", lease.ID, cause)
	default:
		return EmitResult{}, fmt.Errorf("commit Lease emission %s: %w", lease.ID, cause)
	}
}

// abort durably records ABORTED and closes the Attempt in the same store
// boundary. FenceEpoch is never rewound. If an EMITTED commit won the race,
// that durable outcome is returned instead.
func (e *Emitter) abort(ctx context.Context, lease leasestate.LeaseRecord, v verdict) (EmitResult, error) {
	attempt, err := e.cfg.State.LoadAttempt(ctx, lease.AttemptID)
	if err != nil {
		return EmitResult{}, fmt.Errorf("load Attempt %s for Lease abort: %w", lease.AttemptID, err)
	}
	at, err := e.now()
	if err != nil {
		return EmitResult{}, err
	}
	reason := truncateUTF8(v.reason, 512)
	aborted, err := e.cfg.State.AbortLeaseEmission(ctx, statestore.LeaseEmissionAbort{
		LeaseID:                 lease.ID,
		ExpectedAttemptRevision: attempt.Revision,
		CloseReason:             v.closeReason,
		Reason:                  reason,
		CommittedAt:             at,
	})
	if err != nil {
		if result, done, existingErr := e.existing(ctx, lease.ID); done {
			return result, existingErr
		}
		return EmitResult{}, fmt.Errorf("abort Lease reservation %s (%s): %w", lease.ID, v.reason, err)
	}
	return EmitResult{Record: aborted.Record, Replayed: aborted.Replayed},
		fmt.Errorf("%w: %s: %s", ErrLeaseAborted, aborted.Record.CloseReason(), aborted.Record.Reason())
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
