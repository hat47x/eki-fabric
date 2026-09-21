package decisiontrust

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

func TestVerifierBoundaryAcceptsFreshBoundedDecision(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-verifier-ok", 1, now), now)
	registerActivate(t, registry, key, now)

	verifier := mustVerifierBoundary(t, registry)
	req := validPreCryptoRequest(key, now)

	got, err := verifier.ResolvePreCrypto(req)
	if err != nil {
		t.Fatalf("ResolvePreCrypto() error = %v", err)
	}
	if got.ID() != key.ID() {
		t.Fatalf("resolved key = %q, want %q", got.ID(), key.ID())
	}
}

func TestVerifierBoundaryRejectsStaleTrustBundle(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-bundle-stale", 1, now), now)
	registerActivate(t, registry, key, now)

	verifier := mustVerifierBoundary(t, registry)
	req := validPreCryptoRequest(key, now)
	req.TrustBundle.ValidatedAt = now.Add(-31 * time.Minute)

	assertResolveReason(t, verifier, req, ReasonTrustBundleStale)
}

func TestVerifierBoundaryRejectsInvalidTrustBundleMetadata(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-bundle-invalid", 1, now), now)
	registerActivate(t, registry, key, now)

	verifier := mustVerifierBoundary(t, registry)
	req := validPreCryptoRequest(key, now)
	req.TrustBundle.Revision = 0

	assertResolveReason(t, verifier, req, ReasonTrustBundleInvalid)

	req = validPreCryptoRequest(key, now)
	req.TrustBundle.ValidatedAt = now.Add(time.Minute)
	assertResolveReason(t, verifier, req, ReasonTrustBundleInvalid)
}

func TestVerifierBoundaryRejectsNotYetValidExpiredAndOverlongDecision(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-time-policy", 1, now), now)
	registerActivate(t, registry, key, now)
	verifier := mustVerifierBoundary(t, registry)

	t.Run("not yet valid", func(t *testing.T) {
		req := validPreCryptoRequest(key, now)
		req.Key.IssuedAt = now.Add(time.Minute)
		req.ExpiresAt = now.Add(5 * time.Minute)
		assertResolveReason(t, verifier, req, ReasonNotYetValid)
	})

	t.Run("expired", func(t *testing.T) {
		req := validPreCryptoRequest(key, now)
		req.Key.IssuedAt = now.Add(-10 * time.Minute)
		req.ExpiresAt = now.Add(-time.Minute)
		assertResolveReason(t, verifier, req, ReasonExpired)
	})

	t.Run("overlong", func(t *testing.T) {
		req := validPreCryptoRequest(key, now)
		req.Key.IssuedAt = now
		req.ExpiresAt = now.Add(11 * time.Minute)
		assertResolveReason(t, verifier, req, ReasonStaleDecision)
	})

	t.Run("invalid window", func(t *testing.T) {
		req := validPreCryptoRequest(key, now)
		req.ExpiresAt = req.Key.IssuedAt
		assertResolveReason(t, verifier, req, ReasonTimeWindowInvalid)
	})
}

func TestVerifierBoundaryRejectsDisabledAlgorithmBeforeKeyResolution(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-disabled-alg", 1, now), now)
	registerActivate(t, registry, key, now)

	verifier := mustVerifierBoundary(t, registry)
	req := validPreCryptoRequest(key, now)
	req.Key.ProtectedAlgorithm = AlgorithmES256

	assertResolveReason(t, verifier, req, ReasonAlgorithmNotAllowed)
}

func TestVerifierBoundaryMapsRegistryFailuresToStableReasons(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)

	t.Run("unknown key", func(t *testing.T) {
		registry := NewRegistry(now)
		key := mustKey(t, ed25519Config("kid-known-template", 1, now), now)
		registerActivate(t, registry, key, now)
		verifier := mustVerifierBoundary(t, registry)
		req := validPreCryptoRequest(key, now)
		req.Key.KeyID = KeyID("kid-missing")
		assertResolveReason(t, verifier, req, ReasonUnknownKey)
	})

	t.Run("key not active", func(t *testing.T) {
		registry := NewRegistry(now)
		key := mustKey(t, ed25519Config("kid-revoked-gate", 1, now), now)
		registerActivate(t, registry, key, now)
		if err := registry.Revoke(key.ID(), now); err != nil {
			t.Fatalf("Revoke() error = %v", err)
		}
		verifier := mustVerifierBoundary(t, registry)
		req := validPreCryptoRequest(key, now)
		assertResolveReason(t, verifier, req, ReasonKeyNotActiveForVerify)
	})

	t.Run("authority role", func(t *testing.T) {
		registry := NewRegistry(now)
		key := mustKey(t, ed25519Config("kid-role-gate", 1, now), now)
		registerActivate(t, registry, key, now)
		verifier := mustVerifierBoundary(t, registry)
		req := validPreCryptoRequest(key, now)
		req.Key.DecisionKind = authority.DecisionLease
		assertResolveReason(t, verifier, req, ReasonAuthorityRoleMismatch)
	})

	t.Run("subject", func(t *testing.T) {
		registry := NewRegistry(now)
		key := mustKey(t, ed25519Config("kid-subject-gate", 1, now), now)
		registerActivate(t, registry, key, now)
		verifier := mustVerifierBoundary(t, registry)
		req := validPreCryptoRequest(key, now)
		req.Key.AuthorityPrincipal = "authority:workorder:other"
		assertResolveReason(t, verifier, req, ReasonSubjectMismatch)
	})
}

func TestVerificationPolicyRejectsInvalidConfiguration(t *testing.T) {
	_, err := NewVerificationPolicy(VerificationPolicyConfig{
		MaxClockSkew:            -time.Second,
		MaxTrustBundleStaleness: time.Minute,
		AllowedAlgorithms:       []Algorithm{AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionWorkOrder: time.Minute,
		},
	})
	if !errors.Is(err, ErrInvalidVerificationPolicy) {
		t.Fatalf("negative skew error = %v, want ErrInvalidVerificationPolicy", err)
	}

	_, err = NewVerificationPolicy(VerificationPolicyConfig{
		MaxClockSkew:            0,
		MaxTrustBundleStaleness: time.Minute,
		AllowedAlgorithms:       []Algorithm{Algorithm("UNKNOWN")},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionWorkOrder: time.Minute,
		},
	})
	if !errors.Is(err, ErrInvalidVerificationPolicy) {
		t.Fatalf("unknown algorithm error = %v, want ErrInvalidVerificationPolicy", err)
	}
}

func mustVerifierBoundary(t testing.TB, registry *Registry) *VerifierBoundary {
	t.Helper()
	policy, err := NewVerificationPolicy(VerificationPolicyConfig{
		MaxClockSkew:            30 * time.Second,
		MaxTrustBundleStaleness: 30 * time.Minute,
		AllowedAlgorithms:       []Algorithm{AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionWorkOrder: 10 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("NewVerificationPolicy() error = %v", err)
	}
	verifier, err := NewVerifierBoundary(registry, policy)
	if err != nil {
		t.Fatalf("NewVerifierBoundary() error = %v", err)
	}
	return verifier
}

func validPreCryptoRequest(key KeyRecord, now time.Time) PreCryptoVerificationRequest {
	return PreCryptoVerificationRequest{
		Key:         verificationRequestFor(key, authority.DecisionWorkOrder, now),
		ExpiresAt:   now.Add(5 * time.Minute),
		Now:         now,
		TrustBundle: TrustBundleMetadata{Revision: 7, ValidatedAt: now.Add(-time.Minute)},
	}
}

func assertResolveReason(t testing.TB, verifier *VerifierBoundary, req PreCryptoVerificationRequest, want VerificationReason) {
	t.Helper()
	_, err := verifier.ResolvePreCrypto(req)
	if err == nil {
		t.Fatalf("expected verification failure %q", want)
	}
	got, ok := FailureReason(err)
	if !ok {
		t.Fatalf("error %v does not carry VerificationFailure", err)
	}
	if got != want {
		t.Fatalf("verification reason = %q, want %q (err=%v)", got, want, err)
	}
}
