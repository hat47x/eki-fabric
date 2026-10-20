// Package workorderverify authenticates signed WorkOrder bytes for a locally
// configured Worker.
//
// It checks the COSE envelope, the closed WorkOrder payload, the local Worker
// binding, the key and role binding, the validity window, Trust Bundle
// freshness, and the signature. It does not check that the referenced Placement
// Approval, Lease, and Executor Approval are current, and it does not consume
// the WorkOrder nonce. Those are separate boundaries: a successful Verify is
// one observation, not permission to start an Executor.
package workorderverify

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/spi/cosedecoder"
	"github.com/hat47x/eki-fabric/internal/spi/decisiondecoder"
)

var ErrInvalidConfig = errors.New("invalid WorkOrder verifier configuration")

type Config struct {
	Codec          cosedecoder.Codec
	Payload        decisiondecoder.WorkOrderDecoder
	Boundary       *decisiontrust.VerifierBoundary
	TrustDomain    string
	WorkerIdentity string
}

type Verifier struct {
	cfg Config
}

func New(cfg Config) (*Verifier, error) {
	switch {
	case cfg.Codec == nil:
		return nil, fmt.Errorf("%w: COSE codec is required", ErrInvalidConfig)
	case cfg.Payload == nil:
		return nil, fmt.Errorf("%w: WorkOrder decoder is required", ErrInvalidConfig)
	case cfg.Boundary == nil:
		return nil, fmt.Errorf("%w: signature verifier is required", ErrInvalidConfig)
	case !validIdentity(cfg.TrustDomain):
		return nil, fmt.Errorf("%w: local trust domain is required", ErrInvalidConfig)
	case !validIdentity(cfg.WorkerIdentity):
		return nil, fmt.Errorf("%w: local Worker identity is required", ErrInvalidConfig)
	}
	return &Verifier{cfg: cfg}, nil
}

func validIdentity(value string) bool {
	return len(value) > 0 && len(value) <= 256 && utf8.ValidString(value)
}

// Request contains hostile bytes and a trusted caller's current clock and
// validated Trust Bundle metadata. Neither identity comes from these bytes.
type Request struct {
	Raw         []byte
	Now         time.Time
	TrustBundle decisiontrust.TrustBundleMetadata
}

// Verified is only produced after strict decoding and cryptographic
// verification. It records one observation at VerifiedAt.
type Verified struct {
	order               decisionpayload.WorkOrder
	keyID               decisiontrust.KeyID
	trustDomain         string
	trustBundleRevision uint64
	verifiedAt          time.Time
}

func (v Verified) Order() decisionpayload.WorkOrder { return v.order }
func (v Verified) KeyID() decisiontrust.KeyID       { return v.keyID }
func (v Verified) TrustDomain() string              { return v.trustDomain }
func (v Verified) TrustBundleRevision() uint64      { return v.trustBundleRevision }
func (v Verified) VerifiedAt() time.Time            { return v.verifiedAt }
func (v Verified) IsZero() bool                     { return !v.order.Valid() }

func failure(reason decisiontrust.VerificationReason, message string) error {
	return &decisiontrust.VerificationFailure{Reason: reason, Err: errors.New(message)}
}

func normalize(err error, fallback decisiontrust.VerificationReason) error {
	if _, ok := decisiontrust.FailureReason(err); ok {
		return err
	}
	return &decisiontrust.VerificationFailure{Reason: fallback, Err: err}
}

// Verify checks the envelope, closed WorkOrder payload, recipient, key binding,
// validity window, Trust Bundle freshness, and signature, in that order. Every
// failure returns a zero Verified and a stable VerificationFailure reason.
func (v *Verifier) Verify(req Request) (Verified, error) {
	if v == nil || v.cfg.Codec == nil {
		return Verified{}, failure(decisiontrust.ReasonSchemaUnsupported, "WorkOrder verifier is not initialized")
	}
	if req.Now.IsZero() {
		return Verified{}, failure(decisiontrust.ReasonTimeWindowInvalid, "current time is required")
	}
	envelope, err := v.cfg.Codec.DecodeSign1(req.Raw)
	if err != nil {
		return Verified{}, normalize(err, decisiontrust.ReasonMalformedCOSE)
	}
	order, err := v.cfg.Payload.DecodeWorkOrder(envelope.Payload())
	if err != nil {
		return Verified{}, normalize(err, decisiontrust.ReasonSchemaUnsupported)
	}
	if !order.Valid() || order.DecisionKind() != authority.DecisionWorkOrder || order.AuthorityRole() != authority.RoleWorkOrder {
		return Verified{}, failure(decisiontrust.ReasonSchemaUnsupported, "payload is not a valid WorkOrder")
	}
	if order.WorkerIdentity() != v.cfg.WorkerIdentity {
		return Verified{}, failure(decisiontrust.ReasonSubjectMismatch, "WorkOrder recipient differs from the local Worker")
	}
	sigStructure, err := v.cfg.Codec.EncodeSignature1(envelope.ProtectedHeader(), envelope.Payload())
	if err != nil {
		return Verified{}, normalize(err, decisiontrust.ReasonMalformedCOSE)
	}
	key, err := v.cfg.Boundary.VerifyCryptographicSignature(decisiontrust.CryptographicVerificationRequest{
		PreCrypto: decisiontrust.PreCryptoVerificationRequest{
			Key: decisiontrust.VerificationRequest{
				KeyID: envelope.KeyID(), TrustDomain: v.cfg.TrustDomain,
				AuthorityRole: order.AuthorityRole(), AuthorityPrincipal: order.AuthorityPrincipal(),
				DecisionKind: order.DecisionKind(), ProtectedAlgorithm: envelope.Algorithm(),
				IssuedAt: order.IssuedAt(),
			},
			ExpiresAt: order.ExpiresAt(), Now: req.Now, TrustBundle: req.TrustBundle,
		},
		SigStructure: sigStructure, Signature: envelope.Signature(),
	})
	if err != nil {
		return Verified{}, normalize(err, decisiontrust.ReasonSignatureInvalid)
	}
	return Verified{order: order, keyID: key.ID(), trustDomain: v.cfg.TrustDomain,
		trustBundleRevision: req.TrustBundle.Revision, verifiedAt: req.Now.UTC()}, nil
}
