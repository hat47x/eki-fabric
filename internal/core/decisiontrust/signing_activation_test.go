package decisiontrust

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

func TestSetPrimaryRejectsTimeBeforeActivation(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	activation := now.Add(time.Hour)
	registry := NewRegistry(now)

	cfg := ed25519Config("kid-primary-activation", 1, now)
	cfg.SigningNotBefore = now.Add(-time.Hour)
	cfg.SigningNotAfter = now.Add(24 * time.Hour)
	key := mustKey(t, cfg, now)

	if err := registry.Register(key, now); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Activate(key.ID(), activation); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	if err := registry.SetPrimary(key.ID(), activation.Add(-time.Nanosecond)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("SetPrimary(before activation) error = %v, want ErrInvalidTransition", err)
	}
	if err := registry.SetPrimary(key.ID(), activation); err != nil {
		t.Fatalf("SetPrimary(at activation) error = %v", err)
	}
}

func TestResolveSigningKeyRejectsTimeBeforeActivation(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	activation := now.Add(time.Hour)
	registry := NewRegistry(now)

	cfg := ed25519Config("kid-signing-activation", 1, now)
	cfg.SigningNotBefore = now.Add(-time.Hour)
	cfg.SigningNotAfter = now.Add(24 * time.Hour)
	key := mustKey(t, cfg, now)

	if err := registry.Register(key, now); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Activate(key.ID(), activation); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if err := registry.SetPrimary(key.ID(), activation); err != nil {
		t.Fatalf("SetPrimary() error = %v", err)
	}

	req := signingRequestFor(key, authority.DecisionWorkOrder)
	if _, err := registry.ResolveSigningKey(req, activation.Add(-time.Nanosecond)); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("ResolveSigningKey(before activation) error = %v, want ErrNoSigningKey", err)
	}
	if _, err := registry.ResolveSigningKey(req, activation); err != nil {
		t.Fatalf("ResolveSigningKey(at activation) error = %v", err)
	}
}
