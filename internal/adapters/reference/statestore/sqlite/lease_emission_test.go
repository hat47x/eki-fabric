package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func TestVersion5DatabaseUpgradesToEmissionSchemaWithoutLosingLeaseState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := openWithMigrations(context.Background(), path, migrations[:5])
	if err != nil {
		t.Fatalf("open v5 store: %v", err)
	}

	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	policy, err := policystate.NewActive(1, "policy-42", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := old.CompareAndSwapPolicy(context.Background(), 0, policy); err != nil {
		t.Fatal(err)
	}
	taskID, err := ids.NewTaskID()
	if err != nil {
		t.Fatal(err)
	}
	task, err := taskstate.NewTask(taskID, contract.SHA256([]byte("v5-emission-upgrade")), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	// A Task is created admitted; ready is a separate write (#365).
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.CreatedAt.Add(time.Second)
	if err := old.CompareAndSwapTask(context.Background(), task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}
	task = ready
	attemptID, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptstate.New(attemptID, task.ID, task.TaskIRDigest, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	placing := attempt
	placing.Phase = attemptstate.AttemptPhasePlacing
	placing.WorkerIdentity = "worker-a"
	placing.Revision++
	placing.UpdatedAt = now.Add(time.Nanosecond)
	if err := old.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, placing); err != nil {
		t.Fatal(err)
	}
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	placementDigest := contract.SHA256([]byte("placement:v5-emission-upgrade"))
	if _, err := old.db.Exec(
		`INSERT INTO lease_fences (task_id, last_fence_epoch) VALUES (?, 1)`,
		task.ID.String(),
	); err != nil {
		t.Fatalf("seed v5 fence: %v", err)
	}
	if _, err := old.db.Exec(`
INSERT INTO leases (
	lease_id, task_id, task_ir_digest, attempt_id, worker_identity,
	placement_trust_domain, placement_authority_principal, placement_decision_id,
	placement_payload_digest, placement_expires_at_ns, policy_revision, policy_epoch,
	fence_epoch, renewal_sequence, issued_at_ns, expires_at_ns
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		leaseID.String(),
		task.ID.String(),
		task.TaskIRDigest.Hex(),
		attempt.ID.String(),
		placing.WorkerIdentity,
		"local.example",
		"control-plane/plc-1",
		"placement-v5-emission-upgrade",
		placementDigest.Hex(),
		now.Add(2*time.Minute).UnixNano(),
		policy.Revision,
		int64(policy.Epoch),
		1,
		0,
		now.Add(time.Second).UnixNano(),
		now.Add(time.Minute).UnixNano(),
	); err != nil {
		t.Fatalf("seed v5 lease: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded := openStore(t, path)
	defer upgraded.Close()

	gotLease, err := upgraded.LoadLease(context.Background(), leaseID)
	if err != nil {
		t.Fatalf("LoadLease() after v6 migration error = %v", err)
	}
	if gotLease.ID != leaseID ||
		gotLease.FenceEpoch != 1 ||
		gotLease.PolicyEpoch != policy.Epoch ||
		gotLease.PlacementPayloadDigest != placementDigest {
		t.Fatalf("Lease changed across v5 -> v6 migration: %#v", gotLease)
	}
	gotAttempt, err := upgraded.LoadAttempt(context.Background(), attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotAttempt.Phase != attemptstate.AttemptPhaseAssigned || gotAttempt.LeaseID != leaseID {
		t.Fatalf("Attempt/Lease binding changed across v5 -> v6: %#v", gotAttempt)
	}
	gotTask, err := upgraded.LoadTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.TaskIRDigest != task.TaskIRDigest || gotTask.Revision != task.Revision {
		t.Fatalf("Task changed across v5 -> v6: got=%#v want=%#v", gotTask, task)
	}
	if _, err := upgraded.LoadLeaseEmission(context.Background(), leaseID); !errors.Is(err, statestore.ErrNotFound) {
		t.Fatalf("migration invented emission state: %v", err)
	}
}

func TestEmissionSchemaRejectsStalePolicyAndRecordMutationBypass(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	_, reserved := seedReservedLeaseForEmission(t, store, "emission-schema-guard")
	lease := reserved.Lease
	attempt, err := store.LoadAttempt(context.Background(), lease.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.LoadTask(context.Background(), lease.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.LoadCurrentPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := policystate.Revoke(current, current.Epoch+1, lease.IssuedAt.Add(time.Second), "revoke before direct insert")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), current.Epoch, revoked); err != nil {
		t.Fatal(err)
	}

	signed := []byte("direct-sql-signed-envelope")
	if _, err := store.db.Exec(`
INSERT INTO lease_emissions (
	lease_id, status, decision_id, trust_domain, authority_principal, key_id,
	algorithm, payload_digest, envelope_digest, signed_envelope,
	policy_revision, policy_epoch, committed_at_ns, abort_reason, abort_close_reason
) VALUES (?, 'emitted', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
		lease.ID.String(),
		"lease-decision-direct",
		"local.example",
		"control-plane/lse-1",
		"kid-lse-1",
		string(decisiontrust.AlgorithmEdDSA),
		contract.SHA256([]byte("payload")).Hex(),
		contract.SHA256(signed).Hex(),
		signed,
		lease.PolicyRevision,
		int64(lease.PolicyEpoch),
		lease.IssuedAt.Add(2*time.Second).UnixNano(),
	); err == nil {
		t.Fatal("direct SQL emitted stale-policy Lease Authority")
	}

	// Restore a fresh active policy; the old Lease stays fenced to epoch 1 and
	// must not be promoted by an unrelated new policy epoch.
	fresh, err := policystate.NewActive(revoked.Epoch+1, "policy-43", revoked.RevokedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), revoked.Epoch, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitLeaseEmission(context.Background(), statestore.LeaseEmissionCommit{
		LeaseID:                 lease.ID,
		ExpectedTaskRevision:    task.Revision,
		ExpectedAttemptRevision: attempt.Revision,
		ExpectedPolicyEpoch:     fresh.Epoch,
		DecisionID:              "lease-decision-wrong-epoch",
		TrustDomain:             "local.example",
		AuthorityPrincipal:      "control-plane/lse-1",
		KeyID:                   decisiontrust.KeyID("kid-lse-1"),
		Algorithm:               decisiontrust.AlgorithmEdDSA,
		PayloadDigest:           contract.SHA256([]byte("payload")),
		EnvelopeDigest:          contract.SHA256(signed),
		SignedEnvelope:          signed,
		CommittedAt:             lease.IssuedAt.Add(3 * time.Second),
	}); !errors.Is(err, statestore.ErrPolicyChanged) {
		t.Fatalf("old Lease emitted under fresh unrelated policy: %v", err)
	}
}

func TestEmissionSchemaMakesCommittedRecordImmutable(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	task, reserved := seedReservedLeaseForEmission(t, store, "emission-immutable")
	lease := reserved.Lease
	attempt, err := store.LoadAttempt(context.Background(), lease.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	signed := []byte("immutable-signed-envelope")
	commit := statestore.LeaseEmissionCommit{
		LeaseID:                 lease.ID,
		ExpectedTaskRevision:    task.Revision,
		ExpectedAttemptRevision: attempt.Revision,
		ExpectedPolicyEpoch:     lease.PolicyEpoch,
		DecisionID:              "lease-decision-immutable",
		TrustDomain:             "local.example",
		AuthorityPrincipal:      "control-plane/lse-1",
		KeyID:                   decisiontrust.KeyID("kid-lse-1"),
		Algorithm:               decisiontrust.AlgorithmEdDSA,
		PayloadDigest:           contract.SHA256([]byte("payload")),
		EnvelopeDigest:          contract.SHA256(signed),
		SignedEnvelope:          signed,
		CommittedAt:             lease.IssuedAt.Add(time.Second),
	}
	if _, err := store.CommitLeaseEmission(context.Background(), commit); err != nil {
		t.Fatal(err)
	}

	if _, err := store.db.Exec(`UPDATE lease_emissions SET decision_id = 'mutated' WHERE lease_id = ?`, lease.ID.String()); err == nil {
		t.Fatal("committed Lease emission was mutable through direct SQL")
	}
	if _, err := store.db.Exec(`DELETE FROM lease_emissions WHERE lease_id = ?`, lease.ID.String()); err == nil {
		t.Fatal("committed Lease emission was deletable through direct SQL")
	}
}

func seedReservedLeaseForEmission(t *testing.T, store *Store, seed string) (taskstate.TaskRecord, statestore.LeaseReservationResult) {
	t.Helper()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	policy, err := policystate.NewActive(1, "policy-42", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), 0, policy); err != nil {
		t.Fatal(err)
	}

	taskID, err := ids.NewTaskID()
	if err != nil {
		t.Fatal(err)
	}
	task, err := taskstate.NewTask(taskID, contract.SHA256([]byte(seed)), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	// A Task is created admitted; ready is a separate write (#365).
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.CreatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}
	task = ready

	attemptID, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptstate.New(attemptID, task.ID, task.TaskIRDigest, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	placing := attempt
	placing.Phase = attemptstate.AttemptPhasePlacing
	placing.WorkerIdentity = "worker-a"
	placing.Revision++
	placing.UpdatedAt = now.Add(time.Nanosecond)
	if err := store.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, placing); err != nil {
		t.Fatal(err)
	}

	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.ReserveLease(context.Background(), statestore.LeaseReservation{
		LeaseID:                     leaseID,
		TaskID:                      task.ID,
		TaskIRDigest:                task.TaskIRDigest,
		ExpectedTaskRevision:        task.Revision,
		AttemptID:                   attempt.ID,
		ExpectedAttemptRevision:     placing.Revision,
		WorkerIdentity:              placing.WorkerIdentity,
		PlacementTrustDomain:        "local.example",
		PlacementAuthorityPrincipal: "control-plane/plc-1",
		PlacementDecisionID:         "placement-" + seed,
		PlacementPayloadDigest:      contract.SHA256([]byte("placement:" + seed)),
		PlacementExpiresAt:          now.Add(2 * time.Minute),
		PolicyRevision:              policy.Revision,
		ExpectedPolicyEpoch:         policy.Epoch,
		IssuedAt:                    now.Add(time.Second),
		ExpiresAt:                   now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return task, reserved
}
