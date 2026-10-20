package decisionpayload

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func workOrderRef(t *testing.T, kind authority.DecisionKind, id, seed string) DecisionRef {
	t.Helper()
	ref, err := NewDecisionRef(DecisionRefConfig{
		Kind:          kind,
		DecisionID:    id,
		PayloadDigest: contract.SHA256([]byte(seed)),
	})
	if err != nil {
		t.Fatalf("NewDecisionRef() error = %v", err)
	}
	return ref
}

func workOrderConfig(t *testing.T) WorkOrderConfig {
	t.Helper()
	issued := time.UnixMilli(1_780_000_000_000).UTC()
	return WorkOrderConfig{
		SchemaVersion:      WorkOrderSchemaVersion,
		DecisionKind:       authority.DecisionWorkOrder,
		DecisionID:         "work-order-decision-001",
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: "control-plane/wor-1",
		IssuedAt:           issued,
		ExpiresAt:          issued.Add(time.Minute),
		PolicyRevision:     "policy-42",
		WorkOrderID:        "work-order-001",
		TaskID:             ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest:       contract.SHA256([]byte("taskir")),
		AttemptID:          ids.AttemptID("atm_00000000000000000000000000000001"),
		WorkerIdentity:     "worker-a",
		PlacementRef:       workOrderRef(t, authority.DecisionPlacementApproval, "placement-001", "placement"),
		LeaseRef:           workOrderRef(t, authority.DecisionLease, "lease-001", "lease"),
		ExecutorApproval:   workOrderRef(t, authority.DecisionExecutorApproval, "executor-001", "executor"),
		ExecutorDigest:     contract.SHA256([]byte("executable")),
		ReadGrants:         []DecisionRef{workOrderRef(t, authority.DecisionArtifactReadGrant, "read-001", "read")},
		WriteGrants:        []DecisionRef{workOrderRef(t, authority.DecisionArtifactWriteGrant, "write-001", "write")},
		Limits: ResourceLimits{
			CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeMillis: 600_000,
			OutputBytesMaximum: 1 << 20, NetworkBytesMax: 1 << 20,
		},
		Sandbox:            SandboxPolicy{Profile: "sandbox/container/v1"},
		ExternalEffectMode: WorkOrderExternalEffectNone,
		Nonce:              []byte("0123456789abcdef"),
	}
}

func TestNewWorkOrderAcceptsTheClosedShape(t *testing.T) {
	order, err := NewWorkOrder(workOrderConfig(t))
	if err != nil {
		t.Fatalf("NewWorkOrder() error = %v", err)
	}
	if !order.Valid() {
		t.Fatal("constructed WorkOrder is not valid")
	}
	if order.DecisionKind() != authority.DecisionWorkOrder || order.AuthorityRole() != authority.RoleWorkOrder {
		t.Fatalf("kind/role = %q/%q", order.DecisionKind(), order.AuthorityRole())
	}
}

func TestNewWorkOrderRejectsInvalidShapes(t *testing.T) {
	issued := time.UnixMilli(1_780_000_000_000).UTC()
	tooManyReads := func(cfg *WorkOrderConfig) {
		var grants []DecisionRef
		for i := 0; i < MaxWorkOrderReadGrants+1; i++ {
			grants = append(grants, workOrderRef(t, authority.DecisionArtifactReadGrant, fmt.Sprintf("read-%03d", i), "read"))
		}
		cfg.ReadGrants = grants
	}
	cases := map[string]func(*WorkOrderConfig){
		"schema version":   func(c *WorkOrderConfig) { c.SchemaVersion = 2 },
		"decision kind":    func(c *WorkOrderConfig) { c.DecisionKind = authority.DecisionLease },
		"authority role":   func(c *WorkOrderConfig) { c.AuthorityRole = authority.RoleLease },
		"empty decision":   func(c *WorkOrderConfig) { c.DecisionID = "" },
		"empty work order": func(c *WorkOrderConfig) { c.WorkOrderID = "" },
		"bad task id":      func(c *WorkOrderConfig) { c.TaskID = "task-1" },
		"zero task digest": func(c *WorkOrderConfig) { c.TaskIRDigest = contract.Digest{} },
		"unaligned time": func(c *WorkOrderConfig) {
			c.IssuedAt = issued.Add(500 * time.Microsecond)
			c.ExpiresAt = c.IssuedAt.Add(time.Minute)
		},
		"placement kind": func(c *WorkOrderConfig) {
			c.PlacementRef = workOrderRef(t, authority.DecisionLease, "x", "x")
		},
		"lease kind": func(c *WorkOrderConfig) {
			c.LeaseRef = workOrderRef(t, authority.DecisionPlacementApproval, "x", "x")
		},
		"executor kind": func(c *WorkOrderConfig) {
			c.ExecutorApproval = workOrderRef(t, authority.DecisionLease, "x", "x")
		},
		"read grant kind": func(c *WorkOrderConfig) {
			c.ReadGrants = []DecisionRef{workOrderRef(t, authority.DecisionArtifactWriteGrant, "x", "x")}
		},
		"duplicate grant": func(c *WorkOrderConfig) {
			ref := workOrderRef(t, authority.DecisionArtifactReadGrant, "read-001", "read")
			c.ReadGrants = []DecisionRef{ref, ref}
		},
		"too many reads":   tooManyReads,
		"zero executor":    func(c *WorkOrderConfig) { c.ExecutorDigest = contract.Digest{} },
		"zero cpu":         func(c *WorkOrderConfig) { c.Limits.CPUMillis = 0 },
		"zero wall time":   func(c *WorkOrderConfig) { c.Limits.WallTimeMillis = 0 },
		"empty profile":    func(c *WorkOrderConfig) { c.Sandbox.Profile = "" },
		"gpu without vram": func(c *WorkOrderConfig) { c.Sandbox.GPU = &GPULimit{DeviceCount: 1} },
		"effect mode":      func(c *WorkOrderConfig) { c.ExternalEffectMode = 1 },
		"short nonce":      func(c *WorkOrderConfig) { c.Nonce = make([]byte, MinWorkOrderNonceBytes-1) },
		"long nonce":       func(c *WorkOrderConfig) { c.Nonce = make([]byte, MaxWorkOrderNonceBytes+1) },
	}
	for name, mutate := range cases {
		cfg := workOrderConfig(t)
		mutate(&cfg)
		if _, err := NewWorkOrder(cfg); !errors.Is(err, ErrInvalidWorkOrder) {
			t.Fatalf("%s: NewWorkOrder() error = %v, want %v", name, err, ErrInvalidWorkOrder)
		}
	}
}

func TestWorkOrderReturnsDefensiveCopies(t *testing.T) {
	order, err := NewWorkOrder(workOrderConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	nonce := order.Nonce()
	nonce[0] ^= 0xff
	if order.Nonce()[0] == nonce[0] {
		t.Fatal("Nonce() exposes the internal slice")
	}
	grants := order.ReadGrants()
	grants[0] = DecisionRef{}
	if len(order.ReadGrants()) == 0 || !order.ReadGrants()[0].Valid() {
		t.Fatal("ReadGrants() exposes the internal slice")
	}
}
