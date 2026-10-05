package decisionpayload

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func TestNewPlacementApprovalPreservesExactP2Bindings(t *testing.T) {
	cfg := validPlacementConfig(t)
	got, err := NewPlacementApproval(cfg)
	if err != nil {
		t.Fatalf("NewPlacementApproval() error = %v", err)
	}
	if !got.Valid() {
		t.Fatal("PlacementApproval.Valid() = false")
	}
	if got.SchemaVersion() != PlacementApprovalSchemaVersion ||
		got.DecisionKind() != authority.DecisionPlacementApproval ||
		got.AuthorityRole() != authority.RolePlacementAuthorization ||
		got.DecisionID() != cfg.DecisionID ||
		got.AuthorityPrincipal() != cfg.AuthorityPrincipal ||
		got.PolicyRevision() != cfg.PolicyRevision ||
		got.TaskID() != cfg.TaskID ||
		got.TaskIRDigest().String() != cfg.TaskIRDigest.String() ||
		got.AttemptID() != cfg.AttemptID ||
		got.WorkerIdentity() != cfg.WorkerIdentity ||
		got.PayloadDigest().String() != cfg.PayloadDigest.String() {
		t.Fatalf("decoded Placement Approval lost P2 bindings: %#v", got)
	}
	if !got.IssuedAt().Equal(cfg.IssuedAt) || !got.ExpiresAt().Equal(cfg.ExpiresAt) {
		t.Fatalf("time binding changed: issued=%v expires=%v", got.IssuedAt(), got.ExpiresAt())
	}

	evidence := got.Evidence()
	if len(evidence) != 1 ||
		evidence[0].Kind() != cfg.Evidence[0].Kind ||
		evidence[0].Identity() != cfg.Evidence[0].Identity ||
		evidence[0].Digest().String() != cfg.Evidence[0].Digest.String() {
		t.Fatalf("evidence = %#v", evidence)
	}

	cfg.Evidence[0].Kind = "mutated"
	evidence[0] = EvidenceRef{}
	if got.Evidence()[0].Kind() != "assurance" {
		t.Fatal("PlacementApproval retained caller-owned mutable evidence")
	}
}

func TestNewPlacementApprovalRejectsWrongSecuritySemantics(t *testing.T) {
	valid := validPlacementConfig(t)
	tests := []struct {
		name   string
		mutate func(*PlacementApprovalConfig)
	}{
		{name: "schema", mutate: func(cfg *PlacementApprovalConfig) { cfg.SchemaVersion = 2 }},
		{name: "kind", mutate: func(cfg *PlacementApprovalConfig) { cfg.DecisionKind = authority.DecisionLease }},
		{name: "role", mutate: func(cfg *PlacementApprovalConfig) { cfg.AuthorityRole = authority.RoleLease }},
		{name: "decision-id", mutate: func(cfg *PlacementApprovalConfig) { cfg.DecisionID = "" }},
		{name: "principal", mutate: func(cfg *PlacementApprovalConfig) { cfg.AuthorityPrincipal = "" }},
		{name: "window", mutate: func(cfg *PlacementApprovalConfig) { cfg.ExpiresAt = cfg.IssuedAt }},
		{name: "policy", mutate: func(cfg *PlacementApprovalConfig) { cfg.PolicyRevision = "" }},
		{name: "task", mutate: func(cfg *PlacementApprovalConfig) { cfg.TaskID = "tsk_bad" }},
		{name: "taskir", mutate: func(cfg *PlacementApprovalConfig) { cfg.TaskIRDigest = contract.Digest{} }},
		{name: "attempt", mutate: func(cfg *PlacementApprovalConfig) { cfg.AttemptID = "atm_bad" }},
		{name: "worker", mutate: func(cfg *PlacementApprovalConfig) { cfg.WorkerIdentity = "" }},
		{name: "payload-digest", mutate: func(cfg *PlacementApprovalConfig) { cfg.PayloadDigest = contract.Digest{} }},
		{name: "evidence-kind", mutate: func(cfg *PlacementApprovalConfig) { cfg.Evidence[0].Kind = "" }},
		{name: "evidence-identity", mutate: func(cfg *PlacementApprovalConfig) { cfg.Evidence[0].Identity = "" }},
		{name: "evidence-digest", mutate: func(cfg *PlacementApprovalConfig) { cfg.Evidence[0].Digest = contract.Digest{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			cfg.Evidence = append([]EvidenceRefConfig(nil), valid.Evidence...)
			tt.mutate(&cfg)
			got, err := NewPlacementApproval(cfg)
			if !errors.Is(err, ErrInvalidPlacementApproval) {
				t.Fatalf("NewPlacementApproval() error = %v, want ErrInvalidPlacementApproval", err)
			}
			if got.Valid() {
				t.Fatalf("invalid config returned valid PlacementApproval: %#v", got)
			}
		})
	}
}

func TestNewPlacementApprovalRejectsEvidenceAboveP2Bound(t *testing.T) {
	cfg := validPlacementConfig(t)
	cfg.Evidence = make([]EvidenceRefConfig, 17)
	for i := range cfg.Evidence {
		cfg.Evidence[i] = EvidenceRefConfig{
			Kind:     "assurance",
			Identity: "fixture",
			Digest:   contract.SHA256([]byte{byte(i)}),
		}
	}
	if _, err := NewPlacementApproval(cfg); !errors.Is(err, ErrInvalidPlacementApproval) {
		t.Fatalf("NewPlacementApproval() error = %v, want ErrInvalidPlacementApproval", err)
	}
}

func validPlacementConfig(t *testing.T) PlacementApprovalConfig {
	t.Helper()
	taskID, err := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	return PlacementApprovalConfig{
		SchemaVersion:      PlacementApprovalSchemaVersion,
		DecisionKind:       authority.DecisionPlacementApproval,
		DecisionID:         "plc-decision-001",
		AuthorityRole:      authority.RolePlacementAuthorization,
		AuthorityPrincipal: "control-plane/plc-1",
		IssuedAt:           issued,
		ExpiresAt:          issued.Add(2 * time.Minute),
		PolicyRevision:     "policy-42",
		TaskID:             taskID,
		TaskIRDigest:       contract.SHA256([]byte("task-ir")),
		AttemptID:          attemptID,
		WorkerIdentity:     "worker-a",
		Evidence: []EvidenceRefConfig{{
			Kind:     "assurance",
			Identity: "worker-a/assurance",
			Digest:   contract.SHA256([]byte("assurance")),
		}},
		PayloadDigest: contract.SHA256([]byte("canonical-payload")),
	}
}
