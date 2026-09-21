package decisiontrust

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

func TestSigningRequestRejectsDecisionAuthorityMismatch(t *testing.T) {
	req := SigningRequest{
		TrustDomain:        "local.example",
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: "authority:workorder:local",
		DecisionKind:       authority.DecisionLease,
		Algorithm:          AlgorithmEdDSA,
	}
	if err := req.Validate(); !errors.Is(err, ErrDecisionRoleMismatch) {
		t.Fatalf("Validate() error = %v, want ErrDecisionRoleMismatch", err)
	}
}

func TestResolveVerificationKeyRejectsProtectedAlgorithmMismatch(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-alg-mismatch", 1, now), now)
	registerActivate(t, registry, key, now)

	req := verificationRequestFor(key, authority.DecisionWorkOrder, now)
	req.ProtectedAlgorithm = AlgorithmES256

	if _, err := registry.ResolveVerificationKey(req); !errors.Is(err, ErrAlgorithmMismatch) {
		t.Fatalf("ResolveVerificationKey() error = %v, want ErrAlgorithmMismatch", err)
	}
}

func TestResolveVerificationKeyRejectsTrustDomainMismatch(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-domain-mismatch", 1, now), now)
	registerActivate(t, registry, key, now)

	req := verificationRequestFor(key, authority.DecisionWorkOrder, now)
	req.TrustDomain = "foreign.example"

	if _, err := registry.ResolveVerificationKey(req); !errors.Is(err, ErrTrustDomainMismatch) {
		t.Fatalf("ResolveVerificationKey() error = %v, want ErrTrustDomainMismatch", err)
	}
}

func TestResolveVerificationKeyRejectsAuthorityPrincipalMismatch(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-principal-mismatch", 1, now), now)
	registerActivate(t, registry, key, now)

	req := verificationRequestFor(key, authority.DecisionWorkOrder, now)
	req.AuthorityPrincipal = "authority:workorder:other"

	if _, err := registry.ResolveVerificationKey(req); !errors.Is(err, ErrAuthorityMismatch) {
		t.Fatalf("ResolveVerificationKey() error = %v, want ErrAuthorityMismatch", err)
	}
}

func TestResolveVerificationKeyRejectsDecisionAuthorityMismatchBeforeKeyLookup(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	registry := NewRegistry(now)
	key := mustKey(t, ed25519Config("kid-role-mismatch", 1, now), now)
	registerActivate(t, registry, key, now)

	req := verificationRequestFor(key, authority.DecisionLease, now)

	if _, err := registry.ResolveVerificationKey(req); !errors.Is(err, ErrDecisionRoleMismatch) {
		t.Fatalf("ResolveVerificationKey() error = %v, want ErrDecisionRoleMismatch", err)
	}
}

func TestVerificationRequestRequiresIssuedTime(t *testing.T) {
	req := VerificationRequest{
		KeyID:              "kid-missing-time",
		TrustDomain:        "local.example",
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: "authority:workorder:local",
		DecisionKind:       authority.DecisionWorkOrder,
		ProtectedAlgorithm: AlgorithmEdDSA,
	}
	if err := req.Validate(); !errors.Is(err, ErrInvalidKeyRecord) {
		t.Fatalf("Validate() error = %v, want ErrInvalidKeyRecord", err)
	}
}
