package workorderissue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// The end-to-end WOR-1 rows (happy path through the Worker boundaries, stale
// policy, revoked key, expired / superseded Lease, mismatched Worker, crash and
// replay) run against real State Store adapters in
// conformance/statestoretest/workorder_signing.go.

func TestNewRejectsIncompleteConfig(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(empty) error = %v, want ErrInvalidConfig", err)
	}
}

func TestIssueOnNilIssuerFailsClosed(t *testing.T) {
	var issuer *Issuer
	if _, err := issuer.Issue(context.Background(), IssueRequest{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Issue() error = %v, want ErrInvalidConfig", err)
	}
}

func TestValidateRequest(t *testing.T) {
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	valid := IssueRequest{
		LeaseID:        leaseID,
		WorkerIdentity: "worker-a",
		ExecutorApproval: ExecutorApproval{
			DecisionID:     "executor-approval-1",
			PayloadDigest:  contract.SHA256([]byte("executor-approval")),
			ExecutorDigest: contract.SHA256([]byte("executor")),
		},
	}
	if err := validateRequest(valid); err != nil {
		t.Fatalf("validateRequest(valid) error = %v", err)
	}
	cases := map[string]func(*IssueRequest){
		"lease ID":          func(r *IssueRequest) { r.LeaseID = "" },
		"worker":            func(r *IssueRequest) { r.WorkerIdentity = "" },
		"long worker":       func(r *IssueRequest) { r.WorkerIdentity = strings.Repeat("w", 257) },
		"executor decision": func(r *IssueRequest) { r.ExecutorApproval.DecisionID = "" },
		"executor payload":  func(r *IssueRequest) { r.ExecutorApproval.PayloadDigest = contract.Digest{} },
		"executor artifact": func(r *IssueRequest) { r.ExecutorApproval.ExecutorDigest = contract.Digest{} },
	}
	for name, mutate := range cases {
		req := valid
		mutate(&req)
		if err := validateRequest(req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: validateRequest() error = %v, want ErrInvalidRequest", name, err)
		}
	}
}

func TestDecisionIdentityIsDeterministicPerLease(t *testing.T) {
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	if WorkOrderDecisionID(leaseID) != WorkOrderDecisionID(leaseID) || WorkOrderID(leaseID) == WorkOrderDecisionID(leaseID) {
		t.Fatal("WorkOrder identities are not deterministic and distinct")
	}
	if len(WorkOrderDecisionID(leaseID)) > 128 || len(WorkOrderID(leaseID)) > 128 {
		t.Fatal("WorkOrder identities exceed the wire limit")
	}
}
