package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/leaserecall"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

func testLease(t *testing.T) leasestate.LeaseRecord {
	t.Helper()
	leaseID, err := ids.ParseLeaseID("lse_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	lease, err := leasestate.New(leasestate.Config{
		ID: leaseID, TaskID: taskID, TaskIRDigest: contract.SHA256([]byte("task-ir")),
		AttemptID: attemptID, WorkerIdentity: "worker-a",
		PlacementTrustDomain: "local.example", PlacementAuthorityPrincipal: "control-plane/plc-1",
		PlacementDecisionID: "placement-001", PlacementPayloadDigest: contract.SHA256([]byte("placement")),
		PlacementExpiresAt: issued.Add(2 * time.Minute),
		PolicyRevision:     "policy-42", PolicyEpoch: 1, FenceEpoch: 1,
		IssuedAt: issued, ExpiresAt: issued.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

// A reserved Lease must reach the registry the recall loop reads (#745).
func TestRecordLeaseWritesToRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	registry, err := leaserecall.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{leaseRegistry: registry}
	lease := testLease(t)
	if err := s.recordLease(lease); err != nil {
		t.Fatal(err)
	}
	// A new process reads the file, not the memory of this one.
	reopened, err := leaserecall.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	open, err := reopened.OpenLeases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ID != lease.ID {
		t.Fatalf("registry holds %d leases, want the reserved one", len(open))
	}
}

// Without a registry, recording is a no-op: reclaim is off and says so at start-up.
func TestRecordLeaseWithoutRegistry(t *testing.T) {
	s := &Server{}
	if err := s.recordLease(testLease(t)); err != nil {
		t.Fatal(err)
	}
}

// A registry that cannot be written must fail the dispatch, not hand on a Lease
// nobody will reclaim.
func TestRecordLeaseFailsWhenRegistryUnwritable(t *testing.T) {
	dir := t.TempDir()
	registry, err := leaserecall.OpenFile(filepath.Join(dir, "missing-dir", "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{leaseRegistry: registry}
	if err := s.recordLease(testLease(t)); err == nil {
		t.Fatal("recording into an unwritable registry succeeded")
	}
}
