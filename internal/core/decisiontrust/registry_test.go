package decisiontrust

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

func TestRegistryRejectsKeyIDReuse(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)

	first := mustKey(t, ed25519Config("kid-reuse", 1, now), now)
	if err := registry.Register(first, now); err != nil {
		t.Fatalf("Register(first) error = %v", err)
	}

	same := mustKey(t, ed25519Config("kid-reuse", 1, now), now)
	if err := registry.Register(same, now); !errors.Is(err, ErrKeyAlreadyExists) {
		t.Fatalf("Register(same) error = %v, want ErrKeyAlreadyExists", err)
	}

	different := mustKey(t, ed25519Config("kid-reuse", 2, now), now)
	if err := registry.Register(different, now); !errors.Is(err, ErrKeyIDReused) {
		t.Fatalf("Register(different) error = %v, want ErrKeyIDReused", err)
	}
}

func TestRegistryTreatsChangedValidityOrPolicyAsKeyIDReuse(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)

	baseCfg := ed25519Config("kid-metadata-reuse", 1, now)
	base := mustKey(t, baseCfg, now)
	if err := registry.Register(base, now); err != nil {
		t.Fatalf("Register(base) error = %v", err)
	}

	changedValidity := baseCfg
	changedValidity.SigningNotAfter = changedValidity.SigningNotAfter.Add(time.Hour)
	if err := registry.Register(mustKey(t, changedValidity, now), now); !errors.Is(err, ErrKeyIDReused) {
		t.Fatalf("changed validity error = %v, want ErrKeyIDReused", err)
	}

	changedPolicy := baseCfg
	changedPolicy.PolicyRevision = "policy-2"
	if err := registry.Register(mustKey(t, changedPolicy, now), now); !errors.Is(err, ErrKeyIDReused) {
		t.Fatalf("changed policy error = %v, want ErrKeyIDReused", err)
	}
}

func TestRegistrySelectsOnlyPrimaryActiveKeyForSigning(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)

	k1 := mustKey(t, ed25519Config("kid-k1", 1, now), now)
	k2 := mustKey(t, ed25519Config("kid-k2", 2, now), now)
	registerActivate(t, registry, k1, now)
	registerActivate(t, registry, k2, now)

	request := signingRequestFor(k1, authority.DecisionWorkOrder)
	if _, err := registry.ResolveSigningKey(request, now); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("ResolveSigningKey() before primary error = %v, want ErrNoSigningKey", err)
	}

	if err := registry.SetPrimary(k1.ID(), now); err != nil {
		t.Fatalf("SetPrimary(k1) error = %v", err)
	}
	got, err := registry.ResolveSigningKey(request, now)
	if err != nil {
		t.Fatalf("ResolveSigningKey(k1) error = %v", err)
	}
	if got.ID() != k1.ID() {
		t.Fatalf("signing key = %q, want %q", got.ID(), k1.ID())
	}

	if err := registry.SetPrimary(k2.ID(), now.Add(time.Second)); err != nil {
		t.Fatalf("SetPrimary(k2) error = %v", err)
	}
	got, err = registry.ResolveSigningKey(request, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ResolveSigningKey(k2) error = %v", err)
	}
	if got.ID() != k2.ID() {
		t.Fatalf("signing key = %q, want %q", got.ID(), k2.ID())
	}
}

func TestSetPrimaryRejectsKeyOutsideSigningWindow(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)

	futureCfg := ed25519Config("kid-future-primary", 1, now)
	futureCfg.SigningNotBefore = now.Add(time.Hour)
	futureCfg.SigningNotAfter = now.Add(2 * time.Hour)
	future := mustKey(t, futureCfg, now)
	registerActivate(t, registry, future, now)
	if err := registry.SetPrimary(future.ID(), now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("SetPrimary(future) error = %v, want ErrInvalidTransition", err)
	}

	expiredCfg := ed25519Config("kid-expired-primary", 2, now)
	expiredCfg.SigningNotBefore = now.Add(-2 * time.Hour)
	expiredCfg.SigningNotAfter = now.Add(-time.Hour)
	expired := mustKey(t, expiredCfg, now)
	registerActivate(t, registry, expired, now)
	if err := registry.SetPrimary(expired.ID(), now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("SetPrimary(expired) error = %v, want ErrInvalidTransition", err)
	}
}

func TestVerifyOnlyKeyCannotReturnToSigning(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-verify-only", 1, now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	if err := registry.SetVerifyOnly(key.ID(), now.Add(time.Minute)); err != nil {
		t.Fatalf("SetVerifyOnly() error = %v", err)
	}
	request := signingRequestFor(key, authority.DecisionWorkOrder)
	if _, err := registry.ResolveSigningKey(request, now.Add(2*time.Minute)); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("ResolveSigningKey() error = %v, want ErrNoSigningKey", err)
	}
	if _, err := registry.ResolveVerificationKey(verificationRequestFor(key, authority.DecisionWorkOrder, now)); err != nil {
		t.Fatalf("ResolveVerificationKey() error = %v", err)
	}
	if err := registry.Activate(key.ID(), now.Add(3*time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Activate(VERIFY_ONLY) error = %v, want ErrInvalidTransition", err)
	}
}

func TestRevokedAndCompromisedKeysDoNotVerify(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	t.Run("revoked", func(t *testing.T) {
		registry := NewRegistry(now)
		key := mustKey(t, ed25519Config("kid-revoked", 1, now), now)
		registerActivate(t, registry, key, now)
		if err := registry.Revoke(key.ID(), now.Add(time.Minute)); err != nil {
			t.Fatalf("Revoke() error = %v", err)
		}
		if _, err := registry.ResolveVerificationKey(verificationRequestFor(key, authority.DecisionWorkOrder, now)); !errors.Is(err, ErrKeyNotVerifiable) {
			t.Fatalf("ResolveVerificationKey() error = %v, want ErrKeyNotVerifiable", err)
		}
	})

	t.Run("compromised", func(t *testing.T) {
		registry := NewRegistry(now)
		key := mustKey(t, ed25519Config("kid-compromised", 1, now), now)
		registerActivate(t, registry, key, now)
		if err := registry.MarkCompromised(key.ID(), now.Add(time.Minute)); err != nil {
			t.Fatalf("MarkCompromised() error = %v", err)
		}
		if _, err := registry.ResolveForVerification(key.ID(), now); !errors.Is(err, ErrKeyNotVerifiable) {
			t.Fatalf("ResolveForVerification() error = %v, want ErrKeyNotVerifiable", err)
		}
	})
}

func TestRevokedKeyCanEscalateToCompromised(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-escalate", 1, now), now)
	registerActivate(t, registry, key, now)
	if err := registry.Revoke(key.ID(), now.Add(time.Minute)); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if err := registry.MarkCompromised(key.ID(), now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkCompromised(REVOKED) error = %v", err)
	}
	got, err := registry.Get(key.ID())
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.State() != KeyStateCompromised {
		t.Fatalf("State() = %q, want %q", got.State(), KeyStateCompromised)
	}
}

func TestVerificationRequiresIssuedTimeInsideSigningWindow(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	cfg := ed25519Config("kid-window", 1, now)
	key := mustKey(t, cfg, now)
	registerActivate(t, registry, key, now)

	if _, err := registry.ResolveVerificationKey(verificationRequestFor(key, authority.DecisionWorkOrder, cfg.SigningNotBefore.Add(-time.Nanosecond))); !errors.Is(err, ErrKeyNotVerifiable) {
		t.Fatalf("verification before validity error = %v, want ErrKeyNotVerifiable", err)
	}
	if _, err := registry.ResolveVerificationKey(verificationRequestFor(key, authority.DecisionWorkOrder, cfg.SigningNotAfter)); !errors.Is(err, ErrKeyNotVerifiable) {
		t.Fatalf("verification at exclusive end error = %v, want ErrKeyNotVerifiable", err)
	}
	if _, err := registry.ResolveVerificationKey(verificationRequestFor(key, authority.DecisionWorkOrder, now)); err != nil {
		t.Fatalf("verification inside validity error = %v", err)
	}
}

func TestDecisionKindPreventsCrossAuthoritySigning(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-scope", 1, now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	wrong := signingRequestFor(key, authority.DecisionLease)
	if _, err := registry.ResolveSigningKey(wrong, now); !errors.Is(err, ErrDecisionRoleMismatch) {
		t.Fatalf("cross-authority signing error = %v, want ErrDecisionRoleMismatch", err)
	}
}

func TestPrimaryIsClearedWhenKeyLeavesActiveState(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-primary-clear", 1, now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}
	if err := registry.Revoke(key.ID(), now.Add(time.Minute)); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := registry.ResolveSigningKey(signingRequestFor(key, authority.DecisionWorkOrder), now.Add(2*time.Minute)); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("ResolveSigningKey() error = %v, want ErrNoSigningKey", err)
	}
}

func TestRegistryRevisionAdvancesOnAdministrativeMutation(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	start, _ := registry.Revision()

	key := mustKey(t, ed25519Config("kid-revision", 1, now), now)
	if err := registry.Register(key, now.Add(time.Second)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	afterRegister, registerTime := registry.Revision()
	if afterRegister <= start {
		t.Fatalf("revision did not advance: %d -> %d", start, afterRegister)
	}
	if !registerTime.Equal(now.Add(time.Second)) {
		t.Fatalf("updatedAt = %v, want %v", registerTime, now.Add(time.Second))
	}

	if err := registry.Activate(key.ID(), now.Add(2*time.Second)); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	afterActivate, _ := registry.Revision()
	if afterActivate <= afterRegister {
		t.Fatalf("revision did not advance on activation: %d -> %d", afterRegister, afterActivate)
	}
}


func signingRequestFor(key KeyRecord, kind authority.DecisionKind) SigningRequest {
	return SigningRequest{
		TrustDomain:        key.TrustDomain(),
		AuthorityRole:      key.AuthorityRole(),
		AuthorityPrincipal: key.AuthorityPrincipal(),
		DecisionKind:       kind,
		Algorithm:          key.Algorithm(),
	}
}

func verificationRequestFor(key KeyRecord, kind authority.DecisionKind, issuedAt time.Time) VerificationRequest {
	return VerificationRequest{
		KeyID:              key.ID(),
		TrustDomain:        key.TrustDomain(),
		AuthorityRole:      key.AuthorityRole(),
		AuthorityPrincipal: key.AuthorityPrincipal(),
		DecisionKind:       kind,
		ProtectedAlgorithm: key.Algorithm(),
		IssuedAt:           issuedAt,
	}
}

func mustKey(t testing.TB, cfg KeyConfig, now time.Time) KeyRecord {
	t.Helper()
	record, err := NewKeyRecord(cfg, now)
	if err != nil {
		t.Fatalf("NewKeyRecord() error = %v", err)
	}
	return record
}

func registerActivate(t testing.TB, registry *Registry, key KeyRecord, now time.Time) {
	t.Helper()
	if err := registry.Register(key, now); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Activate(key.ID(), now); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
}
