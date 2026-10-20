// Package placementverify composes the P2 Placement Approval verification
// pipeline (ADR-0005 section 14 and the MVP plan's Placement Approval payload
// boundary):
//
//	strict COSE envelope decode
//	  -> strict P2 payload decode
//	  -> key / role / principal / algorithm / window / signature verification
//	  -> current Task / Attempt / Worker / policy revalidation
//
// Each stage lives in its own package behind an interface or boundary. This
// package only orders them and refuses to skip one. A Verified value is
// evidence that the first three stages passed; it is not a Lease. LSE-1 must
// still call Revalidate against current state and decide for itself.
package placementverify

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/cosedecoder"
	"github.com/hat47x/eki-fabric/internal/spi/decisiondecoder"
)

var ErrInvalidConfig = errors.New("invalid placement approval verifier configuration")

// Config wires the verifier. Every field is required.
type Config struct {
	Codec    cosedecoder.Codec
	Payload  decisiondecoder.PlacementApprovalDecoder
	Boundary *decisiontrust.VerifierBoundary
	// TrustDomain is the relying party's trust domain. It comes from local
	// configuration, never from the signed bytes: a decision cannot choose
	// which domain's keys are allowed to vouch for it.
	TrustDomain string
}

// Verifier verifies raw Placement Approval COSE_Sign1 bytes.
type Verifier struct {
	codec       cosedecoder.Codec
	payload     decisiondecoder.PlacementApprovalDecoder
	boundary    *decisiontrust.VerifierBoundary
	trustDomain string
}

func New(cfg Config) (*Verifier, error) {
	switch {
	case cfg.Codec == nil:
		return nil, fmt.Errorf("%w: codec is required", ErrInvalidConfig)
	case cfg.Payload == nil:
		return nil, fmt.Errorf("%w: payload decoder is required", ErrInvalidConfig)
	case cfg.Boundary == nil:
		return nil, fmt.Errorf("%w: verifier boundary is required", ErrInvalidConfig)
	case cfg.TrustDomain == "" || len(cfg.TrustDomain) > 256 || !utf8.ValidString(cfg.TrustDomain):
		return nil, fmt.Errorf("%w: trust domain must be 1..256 valid UTF-8 bytes", ErrInvalidConfig)
	}
	return &Verifier{
		codec:       cfg.Codec,
		payload:     cfg.Payload,
		boundary:    cfg.Boundary,
		trustDomain: cfg.TrustDomain,
	}, nil
}

// Request is one verification. Now and TrustBundle are supplied by the caller
// so verification is deterministic and testable; the verifier reads no clock.
type Request struct {
	Raw         []byte
	Now         time.Time
	TrustBundle decisiontrust.TrustBundleMetadata
}

// Verified is a Placement Approval whose envelope, payload, key binding,
// validity window, and signature have been verified at verifiedAt.
//
// It can only be produced by Verifier.Verify. It carries no flag a caller
// could set, and it does not authorize a Lease.
type Verified struct {
	approval            decisionpayload.PlacementApproval
	keyID               decisiontrust.KeyID
	trustDomain         string
	trustBundleRevision uint64
	verifiedAt          time.Time
}

func (v Verified) Approval() decisionpayload.PlacementApproval { return v.approval }
func (v Verified) PayloadDigest() contract.Digest              { return v.approval.PayloadDigest() }
func (v Verified) KeyID() decisiontrust.KeyID                  { return v.keyID }
func (v Verified) TrustDomain() string                         { return v.trustDomain }
func (v Verified) TrustBundleRevision() uint64                 { return v.trustBundleRevision }
func (v Verified) VerifiedAt() time.Time                       { return v.verifiedAt }

// IsZero reports whether v was not produced by a successful Verify.
func (v Verified) IsZero() bool { return !v.approval.Valid() }

func failf(reason decisiontrust.VerificationReason, format string, args ...any) error {
	return &decisiontrust.VerificationFailure{Reason: reason, Err: fmt.Errorf(format, args...)}
}

// Verify runs the pipeline. Any failure returns a
// *decisiontrust.VerificationFailure and the zero Verified.
func (v *Verifier) Verify(req Request) (Verified, error) {
	if v == nil || v.codec == nil {
		return Verified{}, failf(decisiontrust.ReasonSchemaUnsupported, "verifier is not initialized")
	}
	if req.Now.IsZero() {
		return Verified{}, failf(decisiontrust.ReasonTimeWindowInvalid, "current time is required")
	}

	envelope, err := v.codec.DecodeSign1(req.Raw)
	if err != nil {
		return Verified{}, normalize(err, decisiontrust.ReasonMalformedCOSE)
	}

	approval, err := v.payload.DecodePlacementApproval(envelope.Payload())
	if err != nil {
		return Verified{}, normalize(err, decisiontrust.ReasonSchemaUnsupported)
	}
	if !approval.Valid() || approval.DecisionKind() != authority.DecisionPlacementApproval {
		return Verified{}, failf(decisiontrust.ReasonSchemaUnsupported, "payload is not a valid Placement Approval")
	}

	sigStructure, err := v.codec.EncodeSignature1(envelope.ProtectedHeader(), envelope.Payload())
	if err != nil {
		return Verified{}, failf(decisiontrust.ReasonMalformedCOSE, "build Sig_structure: %v", err)
	}

	// The key is found by what the envelope claims (kid, alg), but accepted
	// only if the registry's record agrees with the payload's claimed role and
	// principal and with our own trust domain. Disagreement fails closed
	// inside ResolvePreCrypto.
	record, err := v.boundary.VerifyCryptographicSignature(decisiontrust.CryptographicVerificationRequest{
		PreCrypto: decisiontrust.PreCryptoVerificationRequest{
			Key: decisiontrust.VerificationRequest{
				KeyID:              envelope.KeyID(),
				TrustDomain:        v.trustDomain,
				AuthorityRole:      approval.AuthorityRole(),
				AuthorityPrincipal: approval.AuthorityPrincipal(),
				DecisionKind:       approval.DecisionKind(),
				ProtectedAlgorithm: envelope.Algorithm(),
				IssuedAt:           approval.IssuedAt(),
			},
			ExpiresAt:   approval.ExpiresAt(),
			Now:         req.Now,
			TrustBundle: req.TrustBundle,
		},
		SigStructure: sigStructure,
		Signature:    envelope.Signature(),
	})
	if err != nil {
		return Verified{}, normalize(err, decisiontrust.ReasonSignatureInvalid)
	}

	return Verified{
		approval:            approval,
		keyID:               record.ID(),
		trustDomain:         v.trustDomain,
		trustBundleRevision: req.TrustBundle.Revision,
		verifiedAt:          req.Now.UTC(),
	}, nil
}

// normalize keeps a VerificationFailure as is and wraps anything else, so a
// caller never sees an adapter's own error type as a contract.
func normalize(err error, fallback decisiontrust.VerificationReason) error {
	if _, ok := decisiontrust.FailureReason(err); ok {
		return err
	}
	return &decisiontrust.VerificationFailure{Reason: fallback, Err: err}
}

// Current is the live state LSE-1 holds at the moment it decides. A signature
// that was valid when Verify ran says nothing about whether the Task still
// exists, is still schedulable, or is still on the same policy.
type Current struct {
	Task           taskstate.TaskRecord
	AttemptID      ids.AttemptID
	WorkerIdentity string
	PolicyRevision string
	Now            time.Time
}

// Revalidate checks the verified approval against current state. It returns
// nil only if the approval still binds exactly this Task, Attempt, Worker, and
// policy revision, and its window is still open.
//
// A nil result is a necessary condition for LSE-1 to issue a Lease, not a
// sufficient one.
func (v Verified) Revalidate(cur Current) error {
	if v.IsZero() {
		return failf(decisiontrust.ReasonSchemaUnsupported, "no verified approval")
	}
	if cur.Now.IsZero() {
		return failf(decisiontrust.ReasonTimeWindowInvalid, "current time is required")
	}
	if err := cur.Task.Validate(); err != nil {
		return failf(decisiontrust.ReasonSubjectMismatch, "current Task record is invalid: %v", err)
	}

	a := v.approval
	now := cur.Now.UTC()
	if !now.Before(a.ExpiresAt()) {
		return failf(decisiontrust.ReasonExpired, "approval expired at %s", a.ExpiresAt().Format(time.RFC3339))
	}
	if now.Before(a.IssuedAt()) {
		return failf(decisiontrust.ReasonNotYetValid, "approval is issued in the future")
	}

	if cur.Task.ID != a.TaskID() {
		return failf(decisiontrust.ReasonSubjectMismatch, "approval is for Task %s, current Task is %s", a.TaskID(), cur.Task.ID)
	}
	if cur.Task.TaskIRDigest != a.TaskIRDigest() {
		return failf(decisiontrust.ReasonSubjectMismatch, "Task IR digest differs from the approved digest")
	}
	if cur.AttemptID != a.AttemptID() {
		return failf(decisiontrust.ReasonSubjectMismatch, "approval is for Attempt %s, current Attempt is %s", a.AttemptID(), cur.AttemptID)
	}
	if cur.WorkerIdentity != a.WorkerIdentity() {
		return failf(decisiontrust.ReasonSubjectMismatch, "approval is for Worker %q, current Worker is %q", a.WorkerIdentity(), cur.WorkerIdentity)
	}
	if cur.PolicyRevision != a.PolicyRevision() {
		return failf(decisiontrust.ReasonPolicyRevoked, "approval policy revision %q is not current (%q)", a.PolicyRevision(), cur.PolicyRevision)
	}

	// A new Lease is only meaningful for a Task that can still make progress.
	switch cur.Task.Phase {
	case taskstate.TaskPhaseReady, taskstate.TaskPhaseActive:
	default:
		return failf(decisiontrust.ReasonStaleDecision, "Task is in phase %q, which cannot take a new Lease", cur.Task.Phase)
	}
	if cur.Task.CancellationRequested {
		return failf(decisiontrust.ReasonStaleDecision, "cancellation has been requested for the Task")
	}
	return nil
}
