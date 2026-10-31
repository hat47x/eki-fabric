// Package placementissue is the PLC-1 signing boundary of the Control Plane.
//
// An Issuer turns one Scheduler proposal that placementauth.Authorize approved
// into one signed `eki.placement-approval/v1` Decision. It is modelled on
// leaseissue.Emitter, with one structural difference: a Placement Approval is
// never committed as a durable record. The durable gate is the Lease
// reservation that consumes it (leaseissue.Reserve re-verifies the signature
// and re-checks every premise), so this package commits nothing and replays
// nothing.
//
//   - The caller establishes the premise from durable state before calling:
//     the Task is Ready, the Attempt is PLACING with the Worker identity, and
//     the policy revision is the current one. The request fields are inputs to
//     the signing, never its authorization.
//   - The Decision ID is deterministic per Attempt
//     (PlacementDecisionID). A retry after a crash re-signs the same Decision,
//     so it can never mint two Decisions for one Attempt placement.
//   - Before returning, the Issuer re-verifies the signed bytes as a relying
//     party would, against the current Trust Bundle and key state.
//   - A transient failure (unavailable signer, stale Trust Bundle) records
//     nothing, so a retry signs again from scratch.
package placementissue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/cosedecoder"
	"github.com/hat47x/eki-fabric/internal/spi/decisiondecoder"
)

var (
	ErrInvalidConfig  = errors.New("invalid PLC-1 Placement issuer configuration")
	ErrInvalidRequest = errors.New("invalid PLC-1 Placement issue request")
	// ErrSignedEnvelopeMismatch means the produced envelope did not decode to
	// exactly the payload, key, and algorithm that were signed.
	ErrSignedEnvelopeMismatch = errors.New("signed Placement envelope does not match the signed payload")
)

type TrustBundleSource interface {
	CurrentTrustBundle(ctx context.Context) (decisiontrust.TrustBundleMetadata, error)
}

type Clock interface {
	Now() time.Time
}

// PlacementSigner signs a COSE Signature1 structure with the current primary
// key of the requested scope. decisiontrust.ReferenceSoftwareSigner satisfies
// it and re-checks key state before and after producing the signature.
type PlacementSigner interface {
	SignCOSESigStructure(req decisiontrust.COSESigningRequest, now time.Time) (decisiontrust.SignatureResult, error)
}

// SigningKeySource reports the current primary signing key of a scope.
// decisiontrust.Registry satisfies it.
type SigningKeySource interface {
	ResolveSigningKey(req decisiontrust.SigningRequest, now time.Time) (decisiontrust.KeyRecord, error)
}

type Config struct {
	Signer      PlacementSigner
	Keys        SigningKeySource
	Verifier    *decisiontrust.VerifierBoundary
	Payload     decisiondecoder.PlacementApprovalCodec
	Envelope    cosedecoder.SigningCodec
	TrustBundle TrustBundleSource
	Clock       Clock

	// TrustDomain and AuthorityPrincipal name this PLC-1 instance. They are
	// configuration, never request input.
	TrustDomain        string
	AuthorityPrincipal string
	Algorithm          decisiontrust.Algorithm
	// KeyPolicyRevision is the key-management policy revision the PLC-1
	// signing key was registered under (decisiontrust.KeyConfig).
	KeyPolicyRevision string
	// MaxValidity caps the Approval window a request can ask for.
	MaxValidity time.Duration
}

// Issuer is the PLC-1 signing boundary. It turns one approved placement into
// worker-facing signed material, or refuses to.
type Issuer struct {
	cfg Config
}

func New(cfg Config) (*Issuer, error) {
	switch {
	case cfg.Signer == nil:
		return nil, fmt.Errorf("%w: PLC-1 signer is required", ErrInvalidConfig)
	case cfg.Keys == nil:
		return nil, fmt.Errorf("%w: signing key source is required", ErrInvalidConfig)
	case cfg.Verifier == nil:
		return nil, fmt.Errorf("%w: signed-decision verifier is required", ErrInvalidConfig)
	case cfg.Payload == nil:
		return nil, fmt.Errorf("%w: Placement payload codec is required", ErrInvalidConfig)
	case cfg.Envelope == nil:
		return nil, fmt.Errorf("%w: COSE signing codec is required", ErrInvalidConfig)
	case cfg.TrustBundle == nil:
		return nil, fmt.Errorf("%w: Trust Bundle source is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: trusted Clock is required", ErrInvalidConfig)
	case cfg.KeyPolicyRevision == "":
		return nil, fmt.Errorf("%w: signing key policy revision is required", ErrInvalidConfig)
	case cfg.MaxValidity <= 0:
		return nil, fmt.Errorf("%w: maximum validity must be positive", ErrInvalidConfig)
	}
	if err := cfg.signingRequest().Validate(); err != nil {
		return nil, fmt.Errorf("%w: PLC-1 signing scope: %v", ErrInvalidConfig, err)
	}
	return &Issuer{cfg: cfg}, nil
}

func (c Config) signingRequest() decisiontrust.SigningRequest {
	return decisiontrust.SigningRequest{
		TrustDomain:        c.TrustDomain,
		AuthorityRole:      authority.RolePlacementAuthorization,
		AuthorityPrincipal: c.AuthorityPrincipal,
		DecisionKind:       authority.DecisionPlacementApproval,
		Algorithm:          c.Algorithm,
	}
}

// PlacementDecisionID is the deterministic `eki.placement-approval/v1`
// Decision ID of one Attempt placement. A retry after a crash re-signs the
// same Decision, so it can never mint a second Placement Decision for one
// Attempt.
func PlacementDecisionID(attempt ids.AttemptID) string {
	return "placement-decision:" + attempt.String()
}

type IssueRequest struct {
	TaskID         ids.TaskID
	TaskIRDigest   contract.Digest
	AttemptID      ids.AttemptID
	WorkerIdentity string
	// PolicyRevision is the durable policy revision the approval is signed
	// under. The caller reads it from durable state.
	PolicyRevision string
	// Evidence names the conditions the approval rests on. Optional.
	Evidence []decisionpayload.EvidenceRefConfig
	// RequestedValidity shrinks the window below MaxValidity. Zero takes the
	// maximum. The window can only shrink, never grow.
	RequestedValidity time.Duration
}

type IssueResult struct {
	// SignedPlacement is the exact COSE_Sign1 bytes. Only these bytes may be
	// handed to leaseissue.Reserve.
	SignedPlacement []byte
	// Approval is the closed value the decoder derived from the signed
	// payload bytes, including their digest.
	Approval decisionpayload.PlacementApproval
	// PayloadDigest is the SHA-256 of the signed payload bytes.
	PayloadDigest contract.Digest
	// KeyID identifies the signing key.
	KeyID decisiontrust.KeyID
}

// Issue signs one approved placement and returns the signed COSE_Sign1 bytes.
//
// It records nothing and reads no state: the caller establishes the premise
// from durable state, and the Lease reservation that consumes the approval
// re-checks it. A failure returns an error and leaves nothing behind, so a
// retry signs again from scratch.
func (i *Issuer) Issue(ctx context.Context, req IssueRequest) (IssueResult, error) {
	if i == nil || i.cfg.Signer == nil {
		return IssueResult{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return IssueResult{}, err
	}
	if err := validateRequest(req); err != nil {
		return IssueResult{}, err
	}

	now, err := i.now()
	if err != nil {
		return IssueResult{}, err
	}
	issuedAt, expiresAt, err := i.window(now, req.RequestedValidity)
	if err != nil {
		return IssueResult{}, err
	}

	// The payload digest is derived from the encoded bytes below, so the
	// producer-side value leaves it zero.
	approval, err := decisionpayload.NewPlacementApproval(decisionpayload.PlacementApprovalConfig{
		SchemaVersion:      decisionpayload.PlacementApprovalSchemaVersion,
		DecisionKind:       authority.DecisionPlacementApproval,
		DecisionID:         PlacementDecisionID(req.AttemptID),
		AuthorityRole:      authority.RolePlacementAuthorization,
		AuthorityPrincipal: i.cfg.AuthorityPrincipal,
		IssuedAt:           issuedAt,
		ExpiresAt:          expiresAt,
		PolicyRevision:     req.PolicyRevision,
		TaskID:             req.TaskID,
		TaskIRDigest:       req.TaskIRDigest,
		AttemptID:          req.AttemptID,
		WorkerIdentity:     req.WorkerIdentity,
		Evidence:           req.Evidence,
	})
	if err != nil {
		return IssueResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	payload, err := i.cfg.Payload.EncodePlacementApproval(approval)
	if err != nil {
		return IssueResult{}, fmt.Errorf("encode Placement payload: %w", err)
	}
	decoded, err := i.cfg.Payload.DecodePlacementApproval(payload)
	if err != nil || !decoded.Equal(approval) {
		return IssueResult{}, fmt.Errorf("%w: canonical Placement payload does not round-trip", ErrSignedEnvelopeMismatch)
	}

	keyReq := i.cfg.signingRequest()
	key, err := i.cfg.Keys.ResolveSigningKey(keyReq, now)
	if err != nil {
		return IssueResult{}, fmt.Errorf("resolve PLC-1 signing key: %w", err)
	}
	protected, err := i.cfg.Envelope.EncodeProtectedHeader(key.Algorithm(), key.ID())
	if err != nil {
		return IssueResult{}, fmt.Errorf("encode Placement protected header: %w", err)
	}
	sigStructure, err := i.cfg.Envelope.EncodeSignature1(protected, payload)
	if err != nil {
		return IssueResult{}, fmt.Errorf("encode Placement Signature1: %w", err)
	}
	signature, err := i.cfg.Signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key:            keyReq,
		PolicyRevision: i.cfg.KeyPolicyRevision,
		SigStructure:   sigStructure,
	}, now)
	if err != nil {
		return IssueResult{}, fmt.Errorf("sign Placement: %w", err)
	}
	if signature.KeyID != key.ID() || signature.Algorithm != key.Algorithm() {
		return IssueResult{}, fmt.Errorf("sign Placement: %w", decisiontrust.ErrSigningAuthorizationChanged)
	}
	envelope, err := i.cfg.Envelope.EncodeSign1(protected, payload, signature.Signature)
	if err != nil {
		return IssueResult{}, fmt.Errorf("encode signed Placement envelope: %w", err)
	}

	// The signed bytes are still private. Re-verify them as a relying party
	// would, against the current Trust Bundle and key state.
	if err := i.verifyEnvelope(ctx, envelope, payload, decoded, key, now); err != nil {
		return IssueResult{}, err
	}

	return IssueResult{
		SignedPlacement: envelope,
		Approval:        decoded,
		PayloadDigest:   contract.SHA256(payload),
		KeyID:           key.ID(),
	}, nil
}

func validateRequest(req IssueRequest) error {
	if _, err := ids.ParseTaskID(req.TaskID.String()); err != nil {
		return fmt.Errorf("%w: task ID: %v", ErrInvalidRequest, err)
	}
	if req.TaskIRDigest.IsZero() {
		return fmt.Errorf("%w: TaskIR digest is required", ErrInvalidRequest)
	}
	if _, err := ids.ParseAttemptID(req.AttemptID.String()); err != nil {
		return fmt.Errorf("%w: attempt ID: %v", ErrInvalidRequest, err)
	}
	if req.WorkerIdentity == "" || len(req.WorkerIdentity) > 256 || !utf8.ValidString(req.WorkerIdentity) {
		return fmt.Errorf("%w: invalid worker identity", ErrInvalidRequest)
	}
	if req.PolicyRevision == "" || len(req.PolicyRevision) > 128 || !utf8.ValidString(req.PolicyRevision) {
		return fmt.Errorf("%w: invalid policy revision", ErrInvalidRequest)
	}
	if req.RequestedValidity < 0 {
		return fmt.Errorf("%w: requested validity must not be negative", ErrInvalidRequest)
	}
	return nil
}

func (i *Issuer) now() (time.Time, error) {
	now := i.cfg.Clock.Now().UTC()
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("%w: trusted Clock returned zero time", ErrInvalidConfig)
	}
	return now, nil
}

// window rounds the Approval window inward, so the signed Approval never
// claims time outside the moment the signature was produced.
func (i *Issuer) window(now time.Time, requested time.Duration) (time.Time, time.Time, error) {
	issuedAt := now.Truncate(time.Millisecond)
	if issuedAt.Before(now) {
		issuedAt = issuedAt.Add(time.Millisecond)
	}
	validity := i.cfg.MaxValidity
	if requested > 0 && requested < validity {
		validity = requested
	}
	expiresAt := issuedAt.Add(validity).Truncate(time.Millisecond)
	if !issuedAt.Before(expiresAt) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: no positive millisecond Placement window", ErrInvalidRequest)
	}
	return issuedAt, expiresAt, nil
}

// verifyEnvelope checks the just-signed bytes exactly as the consuming side
// would before it trusts them.
func (i *Issuer) verifyEnvelope(
	ctx context.Context,
	envelope, payload []byte,
	want decisionpayload.PlacementApproval,
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
	got, err := i.cfg.Payload.DecodePlacementApproval(decoded.Payload())
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
				AuthorityRole:      authority.RolePlacementAuthorization,
				AuthorityPrincipal: i.cfg.AuthorityPrincipal,
				DecisionKind:       authority.DecisionPlacementApproval,
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
	if err != nil {
		return fmt.Errorf("verify signed Placement: %w", err)
	}
	return nil
}
