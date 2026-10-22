package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/workorderemission"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// emitLeaseForWorkOrder commits a Lease emission through the adapter so the
// WorkOrder schema guard has an EMITTED Lease to bind.
func emitLeaseForWorkOrder(t *testing.T, store *Store, seed string) (leasestate.LeaseRecord, leaseemission.Record, uint64, uint64) {
	t.Helper()
	task, reserved := seedReservedLeaseForEmission(t, store, seed)
	lease := reserved.Lease
	attempt, err := store.LoadAttempt(context.Background(), lease.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	signed := []byte("lease-envelope:" + seed)
	emitted, err := store.CommitLeaseEmission(context.Background(), statestore.LeaseEmissionCommit{
		LeaseID:                 lease.ID,
		ExpectedTaskRevision:    task.Revision,
		ExpectedAttemptRevision: attempt.Revision,
		ExpectedPolicyEpoch:     lease.PolicyEpoch,
		DecisionID:              "lease-decision:" + seed,
		TrustDomain:             "local.example",
		AuthorityPrincipal:      "control-plane/lse-1",
		KeyID:                   decisiontrust.KeyID("kid-lse-1"),
		Algorithm:               decisiontrust.AlgorithmEdDSA,
		PayloadDigest:           contract.SHA256([]byte("lease-payload:" + seed)),
		EnvelopeDigest:          contract.SHA256(signed),
		SignedEnvelope:          signed,
		CommittedAt:             lease.IssuedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return lease, emitted.Record, task.Revision, attempt.Revision
}

func insertWorkOrderDirect(store *Store, lease leasestate.LeaseRecord, fence uint64) error {
	signed := []byte("direct-sql-work-order")
	_, err := store.db.Exec(`
INSERT INTO work_order_emissions (
	lease_id, status, decision_id, work_order_id, worker_identity, trust_domain,
	authority_principal, key_id, algorithm, payload_digest, envelope_digest,
	signed_envelope, policy_revision, policy_epoch, fence_epoch, expires_at_ns,
	committed_at_ns, abort_reason_code, abort_reason
) VALUES (?, 'emitted', 'wo-direct', 'wo-direct', ?, 'local.example', 'control-plane/wor-1',
	'kid-wor-1', 'EdDSA', ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
		lease.ID.String(),
		lease.WorkerIdentity,
		contract.SHA256([]byte("payload")).Hex(),
		contract.SHA256(signed).Hex(),
		signed,
		lease.PolicyRevision,
		int64(lease.PolicyEpoch),
		int64(fence),
		lease.IssuedAt.Add(10*time.Second).UnixNano(),
		lease.IssuedAt.Add(2*time.Second).UnixNano(),
	)
	return err
}

func TestWorkOrderSchemaRejectsBypassWrites(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	// A Lease that is only RESERVED has no signed Lease Decision to bind.
	_, reserved := seedReservedLeaseForEmission(t, store, "wo-schema-reserved")
	if err := insertWorkOrderDirect(store, reserved.Lease, reserved.Lease.FenceEpoch); err == nil {
		t.Fatal("direct SQL stored a WorkOrder for a RESERVED Lease")
	}
}

func TestWorkOrderSchemaRejectsWrongFenceAndMakesRecordImmutable(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	lease, leaseRecord, taskRevision, attemptRevision := emitLeaseForWorkOrder(t, store, "wo-schema-emitted")
	if err := insertWorkOrderDirect(store, lease, lease.FenceEpoch+1); err == nil {
		t.Fatal("direct SQL stored a WorkOrder with a fence other than the latest")
	}

	signed := []byte("adapter-work-order")
	commit := statestore.WorkOrderEmissionCommit{
		LeaseID:                 lease.ID,
		ExpectedTaskRevision:    taskRevision,
		ExpectedAttemptRevision: attemptRevision,
		ExpectedPolicyEpoch:     lease.PolicyEpoch,
		ExpectedPolicyRevision:  lease.PolicyRevision,
		ExpectedFenceEpoch:      lease.FenceEpoch,
		LeaseDecisionID:         leaseRecord.DecisionID(),
		LeasePayloadDigest:      leaseRecord.PayloadDigest(),
		WorkerIdentity:          lease.WorkerIdentity,
		DecisionID:              "work-order-decision:" + lease.ID.String(),
		WorkOrderID:             "work-order:" + lease.ID.String(),
		TrustDomain:             "local.example",
		AuthorityPrincipal:      "control-plane/wor-1",
		KeyID:                   decisiontrust.KeyID("kid-wor-1"),
		Algorithm:               decisiontrust.AlgorithmEdDSA,
		PayloadDigest:           contract.SHA256([]byte("wo-payload")),
		EnvelopeDigest:          contract.SHA256(signed),
		SignedEnvelope:          signed,
		ExpiresAt:               lease.IssuedAt.Add(20 * time.Second),
		CommittedAt:             lease.IssuedAt.Add(2 * time.Second),
	}
	if _, err := store.CommitWorkOrderEmission(context.Background(), commit); err != nil {
		t.Fatalf("CommitWorkOrderEmission() error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE work_order_emissions SET decision_id = 'mutated' WHERE lease_id = ?`, lease.ID.String()); err == nil {
		t.Fatal("committed WorkOrder emission was mutable through direct SQL")
	}
	if _, err := store.db.Exec(`DELETE FROM work_order_emissions WHERE lease_id = ?`, lease.ID.String()); err == nil {
		t.Fatal("committed WorkOrder emission was deletable through direct SQL")
	}
	if _, err := store.AbortWorkOrderEmission(context.Background(), statestore.WorkOrderEmissionAbort{
		LeaseID: lease.ID, AbortReason: workorderemission.AbortPolicyRevoked, Reason: "late abort",
		CommittedAt: lease.IssuedAt.Add(3 * time.Second),
	}); err == nil {
		t.Fatal("abort overwrote an EMITTED WorkOrder")
	}
}

func TestWorkOrderAbortChangesNoOtherState(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	lease, _, _, attemptRevision := emitLeaseForWorkOrder(t, store, "wo-schema-abort")
	if _, err := store.AbortWorkOrderEmission(context.Background(), statestore.WorkOrderEmissionAbort{
		LeaseID: lease.ID, AbortReason: workorderemission.AbortWorkerMismatch, Reason: "wrong Worker",
		CommittedAt: lease.IssuedAt.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	attempt, err := store.LoadAttempt(context.Background(), lease.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Revision != attemptRevision || attempt.LeaseID != lease.ID {
		t.Fatalf("WorkOrder abort changed the Attempt: %#v", attempt)
	}
	var fence int64
	if err := store.db.QueryRow(`SELECT last_fence_epoch FROM lease_fences WHERE task_id = ?`, lease.TaskID.String()).Scan(&fence); err != nil {
		t.Fatal(err)
	}
	if uint64(fence) != lease.FenceEpoch {
		t.Fatalf("WorkOrder abort moved the fence: %d -> %d", lease.FenceEpoch, fence)
	}
}
