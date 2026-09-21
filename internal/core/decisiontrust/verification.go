package decisiontrust

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

type VerificationReason string

const (
	ReasonMalformedCOSE          VerificationReason = "MALFORMED_COSE"
	ReasonProtectedHeaderInvalid VerificationReason = "PROTECTED_HEADER_INVALID"
	ReasonUnsupportedProfile     VerificationReason = "UNSUPPORTED_PROFILE"
	ReasonUnknownKey             VerificationReason = "UNKNOWN_KEY"
	ReasonKeyNotActiveForVerify  VerificationReason = "KEY_NOT_ACTIVE_FOR_VERIFY"
	ReasonAlgorithmNotAllowed    VerificationReason = "ALGORITHM_NOT_ALLOWED"
	ReasonKeyAlgorithmMismatch   VerificationReason = "KEY_ALGORITHM_MISMATCH"
	ReasonSignatureInvalid       VerificationReason = "SIGNATURE_INVALID"
	ReasonPayloadNotDeterministic VerificationReason = "PAYLOAD_NOT_DETERMINISTIC"
	ReasonSchemaUnsupported      VerificationReason = "SCHEMA_UNSUPPORTED"
	ReasonAuthorityRoleMismatch  VerificationReason = "AUTHORITY_ROLE_MISMATCH"
	ReasonExpired                VerificationReason = "EXPIRED"
	ReasonNotYetValid            VerificationReason = "NOT_YET_VALID"
	ReasonSubjectMismatch        VerificationReason = "SUBJECT_MISMATCH"
	ReasonStaleDecision          VerificationReason = "STALE_DECISION"
	ReasonPolicyRevoked          VerificationReason = "POLICY_REVOKED"
	ReasonTrustBundleInvalid     VerificationReason = "TRUST_BUNDLE_INVALID"
	ReasonTrustBundleStale       VerificationReason = "TRUST_BUNDLE_STALE"
	ReasonTimeWindowInvalid      VerificationReason = "TIME_WINDOW_INVALID"
)

type VerificationFailure struct {
	Reason VerificationReason
	Err    error
}

func (e *VerificationFailure) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.Reason)
	}
	return fmt.Sprintf("%s: %v", e.Reason, e.Err)
}

func (e *VerificationFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func failure(reason VerificationReason, err error) error {
	return &VerificationFailure{Reason: reason, Err: err}
}

func FailureReason(err error) (VerificationReason, bool) {
	var target *VerificationFailure
	if !errors.As(err, &target) {
		return "", false
	}
	return target.Reason, true
}

var (
	ErrInvalidVerificationPolicy = errors.New("invalid signed-decision verification policy")
	ErrInvalidTrustBundle        = errors.New("invalid signed-decision trust bundle metadata")
	ErrInvalidDecisionWindow     = errors.New("invalid signed-decision time window")
	ErrNilRegistry               = errors.New("signed-decision verifier requires a registry")
)

type TrustBundleMetadata struct {
	Revision    uint64
	ValidatedAt time.Time
}

type VerificationPolicyConfig struct {
	MaxClockSkew            time.Duration
	MaxTrustBundleStaleness time.Duration
	AllowedAlgorithms       []Algorithm
	MaxDecisionLifetime     map[authority.DecisionKind]time.Duration
}

type VerificationPolicy struct {
	maxClockSkew            time.Duration
	maxTrustBundleStaleness time.Duration
	allowedAlgorithms       map[Algorithm]struct{}
	maxDecisionLifetime     map[authority.DecisionKind]time.Duration
}

func NewVerificationPolicy(cfg VerificationPolicyConfig) (VerificationPolicy, error) {
	if cfg.MaxClockSkew < 0 {
		return VerificationPolicy{}, fmt.Errorf("%w: negative clock skew", ErrInvalidVerificationPolicy)
	}
	if cfg.MaxTrustBundleStaleness <= 0 {
		return VerificationPolicy{}, fmt.Errorf("%w: trust bundle staleness must be positive", ErrInvalidVerificationPolicy)
	}
	if len(cfg.AllowedAlgorithms) == 0 {
		return VerificationPolicy{}, fmt.Errorf("%w: at least one algorithm is required", ErrInvalidVerificationPolicy)
	}
	if len(cfg.MaxDecisionLifetime) == 0 {
		return VerificationPolicy{}, fmt.Errorf("%w: at least one decision lifetime is required", ErrInvalidVerificationPolicy)
	}

	allowed := make(map[Algorithm]struct{}, len(cfg.AllowedAlgorithms))
	for _, algorithm := range cfg.AllowedAlgorithms {
		switch algorithm {
		case AlgorithmEdDSA, AlgorithmES256:
			allowed[algorithm] = struct{}{}
		default:
			return VerificationPolicy{}, fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidVerificationPolicy, algorithm)
		}
	}

	lifetimes := make(map[authority.DecisionKind]time.Duration, len(cfg.MaxDecisionLifetime))
	for kind, lifetime := range cfg.MaxDecisionLifetime {
		if !kind.Valid() {
			return VerificationPolicy{}, fmt.Errorf("%w: unknown decision kind %q", ErrInvalidVerificationPolicy, kind)
		}
		if lifetime <= 0 {
			return VerificationPolicy{}, fmt.Errorf("%w: non-positive lifetime for %q", ErrInvalidVerificationPolicy, kind)
		}
		lifetimes[kind] = lifetime
	}

	return VerificationPolicy{
		maxClockSkew:            cfg.MaxClockSkew,
		maxTrustBundleStaleness: cfg.MaxTrustBundleStaleness,
		allowedAlgorithms:       allowed,
		maxDecisionLifetime:     lifetimes,
	}, nil
}

func (p VerificationPolicy) AlgorithmAllowed(algorithm Algorithm) bool {
	_, ok := p.allowedAlgorithms[algorithm]
	return ok
}

func (p VerificationPolicy) MaxDecisionLifetime(kind authority.DecisionKind) (time.Duration, bool) {
	lifetime, ok := p.maxDecisionLifetime[kind]
	return lifetime, ok
}

func (p VerificationPolicy) validateTrustBundle(bundle TrustBundleMetadata, now time.Time) error {
	if bundle.Revision == 0 || bundle.ValidatedAt.IsZero() {
		return failure(ReasonTrustBundleInvalid, ErrInvalidTrustBundle)
	}
	now = now.UTC()
	validatedAt := bundle.ValidatedAt.UTC()
	if validatedAt.After(now.Add(p.maxClockSkew)) {
		return failure(ReasonTrustBundleInvalid, fmt.Errorf("%w: validated time is too far in the future", ErrInvalidTrustBundle))
	}
	if !now.Before(validatedAt.Add(p.maxTrustBundleStaleness).Add(p.maxClockSkew)) {
		return failure(ReasonTrustBundleStale, fmt.Errorf("%w: trust bundle revision %d is stale", ErrInvalidTrustBundle, bundle.Revision))
	}
	return nil
}

func (p VerificationPolicy) validateDecisionWindow(kind authority.DecisionKind, issuedAt, expiresAt, now time.Time) error {
	if issuedAt.IsZero() || expiresAt.IsZero() || !issuedAt.Before(expiresAt) {
		return failure(ReasonTimeWindowInvalid, ErrInvalidDecisionWindow)
	}
	maxLifetime, ok := p.maxDecisionLifetime[kind]
	if !ok {
		return failure(ReasonSchemaUnsupported, fmt.Errorf("%w: no lifetime policy for %q", ErrInvalidVerificationPolicy, kind))
	}

	issuedAt = issuedAt.UTC()
	expiresAt = expiresAt.UTC()
	now = now.UTC()

	if issuedAt.After(now.Add(p.maxClockSkew)) {
		return failure(ReasonNotYetValid, fmt.Errorf("%w: issued time exceeds allowed future skew", ErrInvalidDecisionWindow))
	}
	if !now.Before(expiresAt.Add(p.maxClockSkew)) {
		return failure(ReasonExpired, fmt.Errorf("%w: decision expired", ErrInvalidDecisionWindow))
	}
	if expiresAt.Sub(issuedAt) > maxLifetime {
		return failure(ReasonStaleDecision, fmt.Errorf("%w: decision lifetime exceeds policy", ErrInvalidDecisionWindow))
	}
	return nil
}

type PreCryptoVerificationRequest struct {
	Key         VerificationRequest
	ExpiresAt   time.Time
	Now         time.Time
	TrustBundle TrustBundleMetadata
}

type VerifierBoundary struct {
	registry *Registry
	policy   VerificationPolicy
}

func NewVerifierBoundary(registry *Registry, policy VerificationPolicy) (*VerifierBoundary, error) {
	if registry == nil {
		return nil, ErrNilRegistry
	}
	return &VerifierBoundary{registry: registry, policy: policy}, nil
}

func (v *VerifierBoundary) ResolvePreCrypto(req PreCryptoVerificationRequest) (KeyRecord, error) {
	if req.Now.IsZero() {
		return KeyRecord{}, failure(ReasonTimeWindowInvalid, fmt.Errorf("%w: current time is required", ErrInvalidDecisionWindow))
	}
	switch req.Key.ProtectedAlgorithm {
	case AlgorithmEdDSA, AlgorithmES256:
	default:
		return KeyRecord{}, failure(ReasonAlgorithmNotAllowed, fmt.Errorf("unsupported protected algorithm %q", req.Key.ProtectedAlgorithm))
	}
	if !v.policy.AlgorithmAllowed(req.Key.ProtectedAlgorithm) {
		return KeyRecord{}, failure(ReasonAlgorithmNotAllowed, fmt.Errorf("algorithm %q is disabled by policy", req.Key.ProtectedAlgorithm))
	}
	if err := req.Key.Validate(); err != nil {
		if errors.Is(err, ErrDecisionRoleMismatch) {
			return KeyRecord{}, failure(ReasonAuthorityRoleMismatch, err)
		}
		return KeyRecord{}, failure(ReasonSchemaUnsupported, err)
	}
	if err := v.policy.validateTrustBundle(req.TrustBundle, req.Now); err != nil {
		return KeyRecord{}, err
	}
	if err := v.policy.validateDecisionWindow(req.Key.DecisionKind, req.Key.IssuedAt, req.ExpiresAt, req.Now); err != nil {
		return KeyRecord{}, err
	}

	record, err := v.registry.ResolveVerificationKey(req.Key)
	if err == nil {
		return record, nil
	}
	switch {
	case errors.Is(err, ErrKeyNotFound):
		return KeyRecord{}, failure(ReasonUnknownKey, err)
	case errors.Is(err, ErrKeyNotVerifiable):
		return KeyRecord{}, failure(ReasonKeyNotActiveForVerify, err)
	case errors.Is(err, ErrAlgorithmMismatch):
		return KeyRecord{}, failure(ReasonKeyAlgorithmMismatch, err)
	case errors.Is(err, ErrDecisionRoleMismatch):
		return KeyRecord{}, failure(ReasonAuthorityRoleMismatch, err)
	case errors.Is(err, ErrAuthorityMismatch), errors.Is(err, ErrTrustDomainMismatch):
		return KeyRecord{}, failure(ReasonSubjectMismatch, err)
	default:
		return KeyRecord{}, failure(ReasonSchemaUnsupported, err)
	}
}
