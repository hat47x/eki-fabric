package claimreconcile

import (
	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// M0EffectEvidence contains already-persisted, independently bound records.
// It is NOT a current authority grant or proof of a Provider's actual effects.
// In particular, an absent Content cannot be interpreted as effect_mode=none.
type M0EffectEvidence struct {
	Content              []byte
	RequestFingerprint   contract.Digest
	AdmissionTask        taskstate.TaskRecord
	AdmissionMaxAttempts uint32
	Attempt              attemptstate.AttemptRecord
	Lease                leasestate.LeaseRecord
}

// ProvenM0EffectNone reports whether the admitted and bound M0 request
// explicitly declares effect_mode=none. Failure is fail-closed and returns a
// stable reason category (no raw request data or principal is included).
// The caller must still check the CURRENT policy and authorization, and must
// separately decide whether closing or retrying an Attempt is permitted.
func ProvenM0EffectNone(e M0EffectEvidence) (bool, string) {
	if len(e.Content) == 0 {
		return false, "missing_content"
	}
	if e.AdmissionTask.Validate() != nil || e.Attempt.Validate() != nil || e.Lease.Validate() != nil {
		return false, "invalid_persisted_record"
	}
	if e.AdmissionTask.ID == "" || e.AdmissionTask.ID != e.Attempt.TaskID || e.AdmissionTask.ID != e.Lease.TaskID {
		return false, "task_binding_mismatch"
	}
	if e.Attempt.ID == "" || e.Attempt.ID != e.Lease.AttemptID ||
		e.Lease.ID == "" || e.Attempt.LeaseID != e.Lease.ID ||
		e.Attempt.WorkerIdentity == "" || e.Attempt.WorkerIdentity != e.Lease.WorkerIdentity {
		return false, "execution_binding_mismatch"
	}
	if e.AdmissionTask.TaskIRDigest.IsZero() || e.AdmissionTask.TaskIRDigest != e.Attempt.TaskIRDigest ||
		e.AdmissionTask.TaskIRDigest != e.Lease.TaskIRDigest {
		return false, "taskir_binding_mismatch"
	}
	normalized, err := admission.ParseTaskRequest(e.Content)
	if err != nil {
		return false, "invalid_admission_content"
	}
	fingerprint, err := normalized.Fingerprint()
	if err != nil || fingerprint.IsZero() || fingerprint != e.RequestFingerprint {
		return false, "request_fingerprint_mismatch"
	}
	digest, err := normalized.TaskIRDigest()
	if err != nil || digest.IsZero() || digest != e.AdmissionTask.TaskIRDigest {
		return false, "taskir_digest_mismatch"
	}
	if normalized.Reliability.MaxAttempts != e.AdmissionMaxAttempts {
		return false, "admission_budget_mismatch"
	}
	if normalized.Reliability.EffectMode != "none" {
		return false, "effect_not_none"
	}
	return true, "m0_effect_none_bound"
}
