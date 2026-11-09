package claimreconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

const effectNoneRequest = `{"schema":"eki.task-request/v1","executor":{"id":"dev.example.transform","version_constraint":"^1"},"inputs":[],"parameters":{"mode":"normalize"},"resources":{"cpu_millis":1000,"memory_bytes":268435456,"wall_time_seconds":300,"output_bytes_max":16777216,"network_bytes_max":134217728},"reliability":{"retryable":true,"preemptible":true,"checkpointable":false,"deterministic":true,"max_attempts":3,"effect_mode":"none"}}`

func validM0EffectEvidence(t *testing.T) M0EffectEvidence {
	t.Helper()
	normalized, err := admission.ParseTaskRequest([]byte(effectNoneRequest))
	if err != nil {
		t.Fatal(err)
	}
	fp, err := normalized.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := normalized.TaskIRDigest()
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	aid, _ := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	lid, _ := ids.ParseLeaseID("les_00000000000000000000000000000001")
	now := time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC)
	task, err := taskstate.NewTask(tid, digest, now)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptstate.New(aid, tid, digest, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	attempt.WorkerIdentity = "worker-a"
	attempt.LeaseID = lid
	attempt.Phase = attemptstate.AttemptPhaseDispatched
	lease, err := leasestate.New(leasestate.Config{
		ID: lid, TaskID: tid, AttemptID: aid, TaskIRDigest: digest,
		WorkerIdentity: "worker-a", PlacementTrustDomain: "local.example",
		PlacementAuthorityPrincipal: "control-plane/plc-1", PlacementDecisionID: "placement-001",
		PlacementPayloadDigest: contract.SHA256([]byte("placement")),
		PlacementExpiresAt:     now.Add(10 * time.Minute), PolicyRevision: "policy-42",
		PolicyEpoch: 1, FenceEpoch: 1, IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return M0EffectEvidence{Content: []byte(effectNoneRequest), RequestFingerprint: fp,
		AdmissionTask: task, AdmissionMaxAttempts: normalized.Reliability.MaxAttempts,
		Attempt: attempt, Lease: lease}
}

func TestProvenM0EffectNoneRequiresBoundAdmission(t *testing.T) {
	baseline := validM0EffectEvidence(t)
	if ok, reason := ProvenM0EffectNone(baseline); !ok {
		t.Fatalf("valid M0 record not recognized: %s", reason)
	}
	checks := []struct {
		name   string
		mutate func(*M0EffectEvidence)
		reason string
	}{
		{"missing-content", func(e *M0EffectEvidence) { e.Content = nil }, "missing_content"},
		{"unknown-effect", func(e *M0EffectEvidence) {
			e.Content = []byte(strings.Replace(effectNoneRequest, `"effect_mode":"none"`, `"effect_mode":"non_idempotent"`, 1))
		}, "invalid_admission_content"},
		{"changed-content", func(e *M0EffectEvidence) {
			e.Content = []byte(strings.Replace(effectNoneRequest, `"mode":"normalize"`, `"mode":"different"`, 1))
		}, "request_fingerprint_mismatch"},
		{"wrong-fingerprint", func(e *M0EffectEvidence) { e.RequestFingerprint = contract.SHA256([]byte("wrong")) }, "request_fingerprint_mismatch"},
		{"wrong-task-digest", func(e *M0EffectEvidence) { e.Attempt.TaskIRDigest = contract.SHA256([]byte("wrong")) }, "taskir_binding_mismatch"},
		{"wrong-worker", func(e *M0EffectEvidence) { e.Attempt.WorkerIdentity = "worker-b" }, "execution_binding_mismatch"},
		{"wrong-lease", func(e *M0EffectEvidence) { e.Attempt.LeaseID = "les_00000000000000000000000000000002" }, "execution_binding_mismatch"},
		{"wrong-task", func(e *M0EffectEvidence) { e.Lease.TaskID = "tsk_00000000000000000000000000000002" }, "task_binding_mismatch"},
		{"wrong-attempt", func(e *M0EffectEvidence) { e.Lease.AttemptID = "atm_00000000000000000000000000000002" }, "execution_binding_mismatch"},
		{"wrong-budget", func(e *M0EffectEvidence) { e.AdmissionMaxAttempts = 2 }, "admission_budget_mismatch"},
		{"invalid-JSON", func(e *M0EffectEvidence) { e.Content = []byte("{") }, "invalid_admission_content"},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			input := baseline
			tc.mutate(&input)
			ok, reason := ProvenM0EffectNone(input)
			if ok || reason != tc.reason {
				t.Fatalf("got %v/%s, want false/%s", ok, reason, tc.reason)
			}
		})
	}
}
