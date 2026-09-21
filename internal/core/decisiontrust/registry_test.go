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

func TestRegistrySelectsOnlyPrimaryActiveKeyForSigning(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)

	k1 := mustKey(t, ed25519Config("kid-k1", 1, now), now)
	k2 := mustKey(t, ed25519Config("kid-k2", 2, now), now)
	registerActivate(t, registry, k1, now)
	registerActivate(t, registry, k2, now)

	scope := k1.scope()
	if _, err := registry.ResolveForSigning(scope, now); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("ResolveForSigning() before primary error = %v, want ErrNoSigningKey", err)
	}

	if err := registry.SetPrimary(k1.ID(), now); err != nil {
		t.Fatalf("SetPrimary(k1) error = %v", err)
	}
	got, err := registry.ResolveForSigning(scope, now)
	if err != nil {
		t.Fatalf("ResolveForSigning(k1) error = %v", err)
	}
	if got.ID() != k1.ID() {
		t.Fatalf("signing key = %q, want %q", got.ID(), k1.ID())
	}

	if err := registry.SetPrimary(k2.ID(), now.Add(time.Second)); err != nil {
		t.Fatalf("SetPrimary(k2) error = %v", err)
	}
	got, err = registry.ResolveForSigning(scope, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ResolveForSigning(k2) error = %v", err)
	}
	if got.ID() != k2.ID() {
		t.Fatalf("signing key = %q, want %q", got.ID(), k2.ID())
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
	if _, err := registry.ResolveForSigning(key.scope(), now.Add(2*time.Minute)); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("ResolveForSigning() error = %v, want ErrNoSigningKey", err)
	}
	if _, err := registry.ResolveForVerification(key.ID(), now); err != nil {
		t.Fatalf("ResolveForVerification() error = %v", err)
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
		if _, err := registry.ResolveForVerification(key.ID(), now); !errors.Is(err, ErrKeyNotVerifiable) {
			t.Fatalf("ResolveForVerification() error = %v, want ErrKeyNotVerifiable", err)
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

	if _, err := registry.ResolveForVerification(key.ID(), cfg.SigningNotBefore.Add(-time.Nanosecond)); !errors.Is(err, ErrKeyNotVerifiable) {
		t.Fatalf("verification before validity error = %v, want ErrKeyNotVerifiable", err)
	}
	if _, err := registry.ResolveForVerification(key.ID(), cfg.SigningNotAfter); !errors.Is(err, ErrKeyNotVerifiable) {
		t.Fatalf("verification at exclusive end error = %v, want ErrKeyNotVerifiable", err)
	}
	if _, err := registry.ResolveForVerification(key.ID(), now); err != nil {
		t.Fatalf("verification inside validity error = %v", err)
	}
}

func TestScopePreventsCrossAuthoritySigning(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-scope", 1, now), now)
	registerActivate(t, registry, key, now)
	if err := registry.SetPrimary(key.ID(), now); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	wrong := key.scope()
	wrong.AuthorityRole = authority.RoleLease
	if _, err := registry.ResolveForSigning(wrong, now); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("cross-authority signing error = %v, want ErrNoSigningKey", err)
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
	if _, err := registry.ResolveForSigning(key.scope(), now.Add(2*time.Minute)); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("ResolveForSigning() error = %v, want ErrNoSigningKey", err)
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
