package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/claimreconcile"
	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// recallAttempts is a state store of Attempts held in memory.
type recallAttempts struct {
	record attemptstate.AttemptRecord
	err    error
}

func (f *recallAttempts) CreateAttempt(context.Context, attemptstate.AttemptRecord) error { return nil }

func (f *recallAttempts) LoadAttempt(context.Context, ids.AttemptID) (attemptstate.AttemptRecord, error) {
	if f.err != nil {
		return attemptstate.AttemptRecord{}, f.err
	}
	return f.record, nil
}

func (f *recallAttempts) CompareAndSwapAttempt(_ context.Context, id ids.AttemptID, expected uint64, next attemptstate.AttemptRecord) error {
	if f.err != nil {
		return f.err
	}
	if f.record.Revision != expected {
		return errors.New("stale revision")
	}
	f.record = next
	return nil
}

func (f *recallAttempts) ListAttemptsByTask(context.Context, ids.TaskID) ([]attemptstate.AttemptRecord, error) {
	return nil, nil
}

// recallClaims answers whether a ResultClaim exists.
type recallClaims struct {
	has bool
	err error
}

func (f recallClaims) LoadResultClaim(context.Context, ids.AttemptID) (resultclaim.Claim, bool, error) {
	return resultclaim.Claim{}, f.has, f.err
}

func (f recallClaims) ApplyResultClaim(context.Context, statestore.ResultClaimAcceptance) error {
	return nil
}

// recallRegistry remembers the Leases it took back.
type recallRegistry struct {
	taken []ids.LeaseID
	err   error
}

func (f *recallRegistry) Reclaim(_ context.Context, lease leasestate.LeaseRecord, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.taken = append(f.taken, lease.ID)
	return nil
}

func dispatchedAttempt(t *testing.T, taskID ...ids.TaskID) attemptstate.AttemptRecord {
	t.Helper()
	attemptID, err := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	id := ids.TaskID("tsk_00000000000000000000000000000001")
	if len(taskID) > 0 {
		id = taskID[0]
	}
	record, err := attemptstate.New(attemptID, id, contract.SHA256([]byte("task-ir")), 1, time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	record.Phase = attemptstate.AttemptPhaseDispatched
	return record
}

func expiredCloser(t *testing.T, attempts *recallAttempts, claims recallClaims, admissions ...statestore.AdmissionStore) (recallCloser, *recallRegistry) {
	t.Helper()
	registry := &recallRegistry{}
	closer := recallCloser{
		attempts: attempts, claims: claims, reclaimer: registry,
		grace: RecallGrace, now: func() time.Time { return time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC) },
	}
	if len(admissions) > 0 {
		closer.admissions = admissions[0]
	}
	return closer, registry
}

// An Attempt whose Lease expired without a claim is closed, and only then is the
// Lease taken back (#745).
func TestAnExpiredLeaseWithoutAClaimClosesTheAttempt(t *testing.T) {
	taskID, _ := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	attemptID, _ := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	leaseID, _ := ids.ParseLeaseID("les_00000000000000000000000000000001")
	admission := m0Admission(t, taskID, attemptID, leaseID)
	taskIRDigest := admission.Task.TaskIRDigest
	attempt := dispatchedAttempt(t, taskID)
	attempt.TaskIRDigest = taskIRDigest
	attempt.WorkerIdentity = "worker-a"
	attempt.LeaseID = leaseID
	attempts := &recallAttempts{record: attempt}
	closer, registry := expiredCloser(t, attempts, recallClaims{}, recallAdmissions{admission: admission})
	lease := testLease(t, taskIRDigest)
	lease.TaskID = taskID
	lease.AttemptID = attemptID
	lease.ID = leaseID
	
	if err := closer.Reclaim(context.Background(), lease, "expired"); err != nil {
		t.Fatal(err)
	}
	if attempts.record.Phase != attemptstate.AttemptPhaseClosed {
		t.Fatalf("phase = %s, want closed", attempts.record.Phase)
	}
	if attempts.record.CloseReason != attemptstate.CloseReasonLeaseExpired {
		t.Fatalf("close reason = %s, want lease_expired", attempts.record.CloseReason)
	}
	if len(registry.taken) != 1 {
		t.Fatalf("reclaim calls = %d, want 1", len(registry.taken))
	}
}

// A claim that arrived is not thrown away: the Lease is taken back, the Attempt
// is left to result acceptance.
func TestAnAttemptWithAClaimIsNotClosed(t *testing.T) {
	attempts := &recallAttempts{record: dispatchedAttempt(t)}
	closer, registry := expiredCloser(t, attempts, recallClaims{has: true})
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err != nil {
		t.Fatal(err)
	}
	if attempts.record.Phase == attemptstate.AttemptPhaseClosed {
		t.Fatal("the Attempt was closed even though a claim exists")
	}
	if len(registry.taken) != 0 {
		t.Fatalf("reclaim calls = %d, want none before the claim is decided", len(registry.taken))
	}
}

// During the grace period the Lease stays in the registry, so the next sweep
// looks again.
func TestTheLeaseStaysDuringTheGracePeriod(t *testing.T) {
	attempts := &recallAttempts{record: dispatchedAttempt(t)}
	closer, registry := expiredCloser(t, attempts, recallClaims{})
	// The Lease expired at 12:01; 12:05 is past the grace of 30s. Move the clock
	// back inside it.
	closer.now = func() time.Time { return time.Date(2026, 11, 6, 12, 1, 10, 0, time.UTC) }
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err != nil {
		t.Fatal(err)
	}
	if len(registry.taken) != 0 || attempts.record.Phase == attemptstate.AttemptPhaseClosed {
		t.Fatalf("grace was not respected: taken=%d phase=%s", len(registry.taken), attempts.record.Phase)
	}
}

// A failed close leaves the registry entry alone, so the next sweep tries again.
func TestAFailedCloseKeepsTheRegistryEntry(t *testing.T) {
	attempts := &recallAttempts{record: dispatchedAttempt(t), err: errors.New("store down")}
	closer, registry := expiredCloser(t, attempts, recallClaims{})
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err == nil {
		t.Fatal("a failed close was reported as success")
	}
	if len(registry.taken) != 0 {
		t.Fatal("the Lease was removed from the registry although the Attempt was not closed")
	}
}

// An Attempt that is already closed is not closed twice.
func TestAClosedAttemptIsNotClosedAgain(t *testing.T) {
	record := dispatchedAttempt(t)
	record.Phase = attemptstate.AttemptPhaseClosed
	record.CloseReason = attemptstate.CloseReasonResultAccepted
	attempts := &recallAttempts{record: record}
	closer, _ := expiredCloser(t, attempts, recallClaims{})
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err != nil {
		t.Fatal(err)
	}
	if attempts.record.CloseReason != attemptstate.CloseReasonResultAccepted {
		t.Fatalf("close reason = %s, want the existing result_accepted", attempts.record.CloseReason)
	}
}

// The decision itself is readable without storage.
func TestTheCloseDecisionFollowsTheClaimReconcileRules(t *testing.T) {
	lease := testLease(t)
	now := lease.ExpiresAt.Add(RecallGrace + time.Second)
	cases := []struct {
		name                string
		attempt             attemptstate.AttemptRecord
		claim               bool
		possibleExternalEffect bool
		want                claimreconcile.Decision
	}{
		{"closed", attemptstate.AttemptRecord{Phase: attemptstate.AttemptPhaseClosed}, false, false, claimreconcile.NoAction},
		{"claim", dispatchedAttempt(t), true, false, claimreconcile.NoAction},
		{"open, no claim, no external effect", dispatchedAttempt(t), false, false, claimreconcile.CloseLeaseExpired},
		{"open, no claim, possible external effect", dispatchedAttempt(t), false, true, claimreconcile.Reconcile},
	}
	for _, c := range cases {
		got, err := CloseDecision(c.attempt, c.claim, lease, now, RecallGrace, c.possibleExternalEffect)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: decision = %s, want %s", c.name, got, c.want)
		}
	}
}

// recallAdmissions is a stub AdmissionStore for recall tests.
type recallAdmissions struct {
	admission statestore.Admission
}

func (f recallAdmissions) LoadAdmission(context.Context, ids.TaskID) (statestore.Admission, error) {
	return f.admission, nil
}

func (f recallAdmissions) AdmitTask(context.Context, statestore.Admission, statestore.AdmitLimit) (statestore.AdmitResult, error) {
	return statestore.AdmitResult{}, nil
}

func m0Admission(t *testing.T, taskID ids.TaskID, attemptID ids.AttemptID, leaseID ids.LeaseID) statestore.Admission {
	t.Helper()
	content := []byte(`{
		"schema":"eki.task-request/v1",
		"executor":{"id":"dev.example.transform","version_constraint":"^1"},
		"inputs":[{"name":"source","artifact_id":"art_01"}],
		"parameters":{"mode":"normalize"},
		"resources":{"cpu_millis":1000,"memory_bytes":268435456,"wall_time_seconds":300,"output_bytes_max":16777216,"network_bytes_max":134217728},
		"reliability":{"retryable":true,"preemptible":true,"checkpointable":false,"deterministic":true,"max_attempts":3,"effect_mode":"none"},
		"priority":"normal"
	}`)
	req, err := admission.ParseTaskRequest(content)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	taskIRDigest, err := req.TaskIRDigest()
	if err != nil {
		t.Fatal(err)
	}
	task, err := taskstate.NewTask(taskID, taskIRDigest, time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptstate.New(attemptID, taskID, contract.SHA256([]byte("task-ir")), 1, time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	attempt.Phase = attemptstate.AttemptPhaseDispatched
	attempt.WorkerIdentity = "worker-a"
	attempt.LeaseID = leaseID
	_, err = leasestate.New(leasestate.Config{
		ID: leaseID, TaskID: taskID, TaskIRDigest: contract.SHA256([]byte("task-ir")),
		AttemptID: attemptID, WorkerIdentity: "worker-a",
		PlacementTrustDomain: "local.example", PlacementAuthorityPrincipal: "control-plane/plc-1",
		PlacementDecisionID: "placement-001", PlacementPayloadDigest: contract.SHA256([]byte("placement")),
		PlacementExpiresAt: time.Date(2026, 11, 6, 12, 2, 0, 0, time.UTC),
		PolicyRevision: "policy-42", PolicyEpoch: 1, FenceEpoch: 1,
		IssuedAt: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 11, 6, 12, 1, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return statestore.Admission{
		Task:               task,
		Principal:          "alice",
		IdempotencyKey:     "key-001",
		RequestFingerprint: fingerprint,
		MaxAttempts:        3,
		Content:            content,
	}
}

// An Attempt with a proven M0 effect_mode=none is closed when its Lease expires
// without a ResultClaim (#745).
func TestAnExpiredLeaseWithM0EffectNoneClosesTheAttempt(t *testing.T) {
	taskID, _ := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	attemptID, _ := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	leaseID, _ := ids.ParseLeaseID("les_00000000000000000000000000000001")
	admission := m0Admission(t, taskID, attemptID, leaseID)
	taskIRDigest := admission.Task.TaskIRDigest
	attempt := dispatchedAttempt(t, taskID)
	attempt.TaskIRDigest = taskIRDigest
	attempt.WorkerIdentity = "worker-a"
	attempt.LeaseID = leaseID
	attempts := &recallAttempts{record: attempt}
	closer, registry := expiredCloser(t, attempts, recallClaims{}, recallAdmissions{admission: admission})
	lease := testLease(t, taskIRDigest)
	lease.TaskID = taskID
	lease.AttemptID = attemptID
	lease.ID = leaseID
	if err := closer.Reclaim(context.Background(), lease, "expired"); err != nil {
		t.Fatal(err)
	}
	if attempts.record.Phase != attemptstate.AttemptPhaseClosed {
		t.Fatalf("phase = %s, want closed", attempts.record.Phase)
	}
	if len(registry.taken) != 1 {
		t.Fatalf("reclaimed leases = %d, want 1", len(registry.taken))
	}
}

// An Attempt whose admission is missing or invalid is kept open for human
// decision when its Lease expires, because the effect mode is unknown (#745).
func TestAnExpiredLeaseWithMissingAdmissionKeepsTheAttemptOpen(t *testing.T) {
	attempts := &recallAttempts{record: dispatchedAttempt(t)}
	closer, registry := expiredCloser(t, attempts, recallClaims{}, recallAdmissions{})
	if err := closer.Reclaim(context.Background(), testLease(t), "expired"); err != nil {
		t.Fatal(err)
	}
	if attempts.record.Phase != attemptstate.AttemptPhaseDispatched {
		t.Fatalf("phase = %s, want dispatched (kept open)", attempts.record.Phase)
	}
	if len(registry.taken) != 0 {
		t.Fatalf("reclaimed leases = %d, want 0", len(registry.taken))
	}
}
