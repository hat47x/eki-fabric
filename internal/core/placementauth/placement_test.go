package placementauth

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/scheduler"
	"github.com/hat47x/eki-fabric/internal/core/suitability"
)

func TestAuthorizeRequiresIndependentHardChecksBeforeDraft(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	proposal := plannedProposal(t, taskID, attemptID, taskIR, "worker-a")
	worker := passingAssessment("worker-a")

	result, err := Authorize(Request{
		Proposal:       proposal,
		TaskID:         taskID,
		TaskIRDigest:   taskIR,
		AttemptID:      attemptID,
		PolicyRevision: 1,
		Worker:         worker,
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if result.Outcome != OutcomeApproved || result.Draft == nil {
		t.Fatalf("result = %+v", result)
	}
	draft := result.Draft
	if draft.TaskID() != taskID ||
		draft.AttemptID() != attemptID ||
		draft.TaskIRDigest().String() != taskIR.String() ||
		draft.WorkerIdentity() != "worker-a" {
		t.Fatalf("draft lost binding: %+v", draft)
	}
	if draft.SuitabilityBasisDigest().String() != proposal.SuitabilityBasisDigest().String() {
		t.Fatalf("Suitability basis changed: %s != %s", draft.SuitabilityBasisDigest(), proposal.SuitabilityBasisDigest())
	}
	if draft.DecisionKind() != authority.DecisionPlacementApproval ||
		draft.RequiredAuthorityRole() != authority.RolePlacementAuthorization {
		t.Fatalf("wrong decision signing boundary: kind=%s role=%s", draft.DecisionKind(), draft.RequiredAuthorityRole())
	}
	if err := authority.ValidateRoleForDecision(draft.RequiredAuthorityRole(), draft.DecisionKind()); err != nil {
		t.Fatalf("authority boundary invalid: %v", err)
	}
	if len(draft.Evidence()) != 7 {
		t.Fatalf("Evidence count = %d", len(draft.Evidence()))
	}
}

func TestAuthorizeRejectsHighRankedWorkerWhenDataClassFails(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	proposal := plannedProposal(t, taskID, attemptID, taskIR, "worker-a")

	worker := passingAssessment("worker-a")
	worker.DataClassAllowed = check(false, "data-policy", "worker-a/data-class")

	result, err := Authorize(Request{
		Proposal:       proposal,
		TaskID:         taskID,
		TaskIRDigest:   taskIR,
		AttemptID:      attemptID,
		PolicyRevision: 1,
		Worker:         worker,
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if result.Outcome != OutcomeRejected || result.Draft != nil {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Rejections) != 1 ||
		result.Rejections[0].Reason != ReasonDataClassUnsatisfied ||
		result.Rejections[0].Evidence == nil {
		t.Fatalf("Rejections = %+v", result.Rejections)
	}
}

func TestAuthorizeRejectsPolicyDriftAndBindingMismatch(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	otherTaskID := mustTaskID(t, "tsk_00000000000000000000000000000002")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	proposal := plannedProposal(t, taskID, attemptID, taskIR, "worker-a")

	tests := []struct {
		name   string
		req    Request
		reason ReasonCode
	}{
		{
			name: "policy drift",
			req: Request{
				Proposal:       proposal,
				TaskID:         taskID,
				TaskIRDigest:   taskIR,
				AttemptID:      attemptID,
				PolicyRevision: 2,
				Worker:         passingAssessment("worker-a"),
			},
			reason: ReasonPolicyRevisionChanged,
		},
		{
			name: "Task mismatch",
			req: Request{
				Proposal:       proposal,
				TaskID:         otherTaskID,
				TaskIRDigest:   taskIR,
				AttemptID:      attemptID,
				PolicyRevision: 1,
				Worker:         passingAssessment("worker-a"),
			},
			reason: ReasonProposalBindingMismatch,
		},
		{
			name: "Worker mismatch",
			req: Request{
				Proposal:       proposal,
				TaskID:         taskID,
				TaskIRDigest:   taskIR,
				AttemptID:      attemptID,
				PolicyRevision: 1,
				Worker:         passingAssessment("worker-b"),
			},
			reason: ReasonWorkerIdentityMismatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Authorize(tt.req)
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if result.Outcome != OutcomeRejected || result.Draft != nil {
				t.Fatalf("result = %+v", result)
			}
			found := false
			for _, rejection := range result.Rejections {
				if rejection.Reason == tt.reason {
					found = true
				}
			}
			if !found {
				t.Fatalf("Rejections = %+v, want %s", result.Rejections, tt.reason)
			}
		})
	}
}

func TestAuthorizePreservesMultipleIndependentFailures(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	proposal := plannedProposal(t, taskID, attemptID, taskIR, "worker-a")
	worker := passingAssessment("worker-a")
	worker.ProviderActive = check(false, "provider-state", "worker-a/provider")
	worker.AssuranceSatisfied = check(false, "assurance", "worker-a/assurance")
	worker.TransferWithinBudget = check(false, "network-budget", "worker-a/transfer")

	result, err := Authorize(Request{
		Proposal:       proposal,
		TaskID:         taskID,
		TaskIRDigest:   taskIR,
		AttemptID:      attemptID,
		PolicyRevision: 1,
		Worker:         worker,
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	want := []ReasonCode{
		ReasonProviderInactive,
		ReasonAssuranceUnsatisfied,
		ReasonTransferBudgetExceeded,
	}
	if len(result.Rejections) != len(want) {
		t.Fatalf("Rejections = %+v", result.Rejections)
	}
	for i := range want {
		if result.Rejections[i].Reason != want[i] {
			t.Fatalf("Rejections[%d] = %s, want %s", i, result.Rejections[i].Reason, want[i])
		}
	}
}

func TestAuthorizeFailsClosedWhenEvidenceIsMissing(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	proposal := plannedProposal(t, taskID, attemptID, taskIR, "worker-a")
	worker := passingAssessment("worker-a")
	worker.AssuranceSatisfied.Evidence.Digest = contract.Digest{}

	_, err := Authorize(Request{
		Proposal:       proposal,
		TaskID:         taskID,
		TaskIRDigest:   taskIR,
		AttemptID:      attemptID,
		PolicyRevision: 1,
		Worker:         worker,
	})
	if !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("Authorize() error = %v", err)
	}
}

func plannedProposal(
	t *testing.T,
	taskID ids.TaskID,
	attemptID ids.AttemptID,
	taskIR contract.Digest,
	workerIdentity string,
) *scheduler.Proposal {
	t.Helper()
	_, entry, err := suitability.EvaluateForFabricEntry(suitabilityInput(taskID, taskIR))
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry() error = %v", err)
	}
	if entry == nil {
		t.Fatal("expected FabricEntry")
	}
	result, err := scheduler.Plan(scheduler.Request{
		TaskID:       taskID,
		TaskIRDigest: taskIR,
		AttemptID:    attemptID,
		FabricEntry:  entry,
		Candidates: []scheduler.Candidate{
			{
				WorkerIdentity:         workerIdentity,
				PlanningEligible:       true,
				CacheHit:               true,
				EstimatedTransferBytes: 100,
				AvailableCPUMillis:     2_000,
			},
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Proposal == nil {
		t.Fatal("expected Scheduler Proposal")
	}
	return result.Proposal
}

func passingAssessment(worker string) WorkerAssessment {
	return WorkerAssessment{
		WorkerIdentity:       worker,
		Online:               check(true, "worker-session", worker+"/online"),
		ProviderActive:       check(true, "provider-state", worker+"/provider"),
		ExecutorSupported:    check(true, "executor-support", worker+"/executor"),
		DataClassAllowed:     check(true, "data-policy", worker+"/data-class"),
		ResourceFeasible:     check(true, "resource-feasibility", worker+"/resources"),
		AssuranceSatisfied:   check(true, "assurance", worker+"/assurance"),
		TransferWithinBudget: check(true, "network-budget", worker+"/transfer"),
	}
}

func check(satisfied bool, kind, identity string) Check {
	return Check{
		Satisfied: satisfied,
		Evidence: EvidenceRef{
			Kind:     kind,
			Identity: identity,
			Digest:   contract.SHA256([]byte(kind + ":" + identity)),
		},
	}
}

func suitabilityInput(taskID ids.TaskID, taskIR contract.Digest) suitability.Input {
	return suitability.Input{
		TaskID:               taskID,
		TaskIRDigest:         taskIR,
		WorkloadDigest:       contract.SHA256([]byte("fixture-workload")),
		PolicyRevision:       1,
		EvaluatorVersion:     "suitability-v0",
		FeasibilityConfirmed: true,
		Baseline: &suitability.DurationEvidence{
			Millis:      60_000,
			EvidenceRef: "fixture:baseline",
		},
		Components: []suitability.ComponentEstimate{
			{Kind: suitability.ComponentInputTransfer, Millis: 100, EvidenceRef: "fixture:input"},
			{Kind: suitability.ComponentCompute, Millis: 20_000, EvidenceRef: "fixture:compute"},
		},
		Excluded: []suitability.ComponentExclusion{
			{Kind: suitability.ComponentSuitabilityDecision, Reason: "fixture"},
			{Kind: suitability.ComponentAdmissionPlacement, Reason: "fixture"},
			{Kind: suitability.ComponentDispatch, Reason: "fixture"},
			{Kind: suitability.ComponentQueue, Reason: "fixture"},
			{Kind: suitability.ComponentRetryPenalty, Reason: "fixture"},
			{Kind: suitability.ComponentOutputTransfer, Reason: "fixture"},
			{Kind: suitability.ComponentVerification, Reason: "fixture"},
		},
		MinimumBenefitMillis: 1_000,
	}
}

func mustTaskID(t *testing.T, value string) ids.TaskID {
	t.Helper()
	id, err := ids.ParseTaskID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustAttemptID(t *testing.T, value string) ids.AttemptID {
	t.Helper()
	id, err := ids.ParseAttemptID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
