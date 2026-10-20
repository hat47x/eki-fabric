package decisionpayload

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func validLeaseConfig(t *testing.T) LeaseConfig {
	t.Helper()
	issued := time.UnixMilli(1_780_000_000_000).UTC()
	return LeaseConfig{
		SchemaVersion:      LeaseSchemaVersion,
		DecisionKind:       authority.DecisionLease,
		DecisionID:         "lease-decision-001",
		AuthorityRole:      authority.RoleLease,
		AuthorityPrincipal: "control-plane/lse-1",
		IssuedAt:           issued,
		ExpiresAt:          issued.Add(time.Minute),
		PolicyRevision:     "policy-42",
		TaskID:             ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest:       contract.SHA256([]byte("taskir")),
		AttemptID:          ids.AttemptID("atm_00000000000000000000000000000001"),
		WorkerIdentity:     "worker-a",
		LeaseID:            ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch:         7,
	}
}

func TestNewLeasePreservesBindings(t *testing.T) {
	cfg := validLeaseConfig(t)
	got, err := NewLease(cfg)
	if err != nil {
		t.Fatalf("NewLease() error = %v", err)
	}
	if !got.Valid() ||
		got.DecisionKind() != authority.DecisionLease ||
		got.AuthorityRole() != authority.RoleLease ||
		got.LeaseID() != cfg.LeaseID ||
		got.FenceEpoch() != cfg.FenceEpoch ||
		got.TaskID() != cfg.TaskID ||
		got.AttemptID() != cfg.AttemptID ||
		got.WorkerIdentity() != cfg.WorkerIdentity ||
		got.PolicyRevision() != cfg.PolicyRevision ||
		!got.ExpiresAt().Equal(cfg.ExpiresAt) {
		t.Fatalf("Lease lost bindings: %#v", got)
	}
	other, err := NewLease(cfg)
	if err != nil || !got.Equal(other) {
		t.Fatal("identical configs are not Equal")
	}
	cfg.FenceEpoch++
	moved, err := NewLease(cfg)
	if err != nil || got.Equal(moved) {
		t.Fatal("Equal ignored FenceEpoch")
	}
}

func TestNewLeaseRejectsWrongSemantics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*LeaseConfig)
	}{
		{name: "schema", mutate: func(c *LeaseConfig) { c.SchemaVersion = 2 }},
		{name: "kind", mutate: func(c *LeaseConfig) { c.DecisionKind = authority.DecisionPlacementApproval }},
		{name: "role", mutate: func(c *LeaseConfig) { c.AuthorityRole = authority.RolePlacementAuthorization }},
		{name: "decision", mutate: func(c *LeaseConfig) { c.DecisionID = "" }},
		{name: "principal", mutate: func(c *LeaseConfig) { c.AuthorityPrincipal = "" }},
		{name: "window", mutate: func(c *LeaseConfig) { c.ExpiresAt = c.IssuedAt }},
		{name: "sub-millisecond", mutate: func(c *LeaseConfig) { c.ExpiresAt = c.ExpiresAt.Add(time.Microsecond) }},
		{name: "policy", mutate: func(c *LeaseConfig) { c.PolicyRevision = "" }},
		{name: "task", mutate: func(c *LeaseConfig) { c.TaskID = "tsk_bad" }},
		{name: "taskir", mutate: func(c *LeaseConfig) { c.TaskIRDigest = contract.Digest{} }},
		{name: "attempt", mutate: func(c *LeaseConfig) { c.AttemptID = "atm_bad" }},
		{name: "worker", mutate: func(c *LeaseConfig) { c.WorkerIdentity = "" }},
		{name: "lease", mutate: func(c *LeaseConfig) { c.LeaseID = "lse_bad" }},
		{name: "fence", mutate: func(c *LeaseConfig) { c.FenceEpoch = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validLeaseConfig(t)
			tt.mutate(&cfg)
			got, err := NewLease(cfg)
			if !errors.Is(err, ErrInvalidLease) {
				t.Fatalf("NewLease() error = %v, want ErrInvalidLease", err)
			}
			if got.Valid() {
				t.Fatal("rejected config returned a valid Lease")
			}
		})
	}
}
