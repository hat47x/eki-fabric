package coreflow

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/placementauth"
	"github.com/hat47x/eki-fabric/internal/core/scheduler"
	"github.com/hat47x/eki-fabric/internal/core/suitability"
)

func TestNotWorthDistributingCreatesNoPlacementAuthorityCandidate(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir:not-worth-distributing"))

	receipt, entry, err := suitability.EvaluateForFabricEntry(suitabilityInput(
		taskID,
		taskIR,
		60_000,
		81_920,
		20_000,
	))
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry() error = %v", err)
	}
	if receipt.Decision != suitability.DecisionFabricNotJustified {
		t.Fatalf("Decision = %q, want %q", receipt.Decision, suitability.DecisionFabricNotJustified)
	}
	if entry != nil {
		t.Fatalf("not-justified path produced FabricEntry: %+v", entry)
	}

	planned, err := scheduler.Plan(scheduler.Request{
		TaskID:       taskID,
		TaskIRDigest: taskIR,
		AttemptID:    attemptID,
		FabricEntry:  entry,
		Candidates: []scheduler.Candidate{
			{WorkerIdentity: "worker-fast", PlanningEligible: true, CacheHit: true, AvailableCPUMillis: 10_000},
		},
	})
	if !errors.Is(err, scheduler.ErrFabricEntryRequired) {
		t.Fatalf("Plan() error = %v, want ErrFabricEntryRequired", err)
	}
	if planned.Proposal != nil {
		t.Fatalf("not-justified path produced Scheduler proposal: %+v", planned.Proposal)
	}

	// There is intentionally no PLC-1 input to authorize here. The negative
	// product-value path stops before any placement authority candidate exists.
}

func TestNeedsMeasurementCreatesNoPlacementAuthorityCandidate(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir:needs-measurement"))

	in := suitabilityInput(taskID, taskIR, 60_000, 100, 20_000)
	in.Baseline = nil

	receipt, entry, err := suitability.EvaluateForFabricEntry(in)
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry() error = %v", err)
	}
	if receipt.Decision != suitability.DecisionNeedsMeasurement {
		t.Fatalf("Decision = %q, want %q", receipt.Decision, suitability.DecisionNeedsMeasurement)
	}
	if entry != nil {
		t.Fatalf("needs-measurement path produced FabricEntry: %+v", entry)
	}

	planned, err := scheduler.Plan(scheduler.Request{
		TaskID:       taskID,
		TaskIRDigest: taskIR,
		AttemptID:    attemptID,
		FabricEntry:  entry,
		Candidates: []scheduler.Candidate{
			{WorkerIdentity: "worker-fast", PlanningEligible: true},
		},
	})
	if !errors.Is(err, scheduler.ErrFabricEntryRequired) {
		t.Fatalf("Plan() error = %v, want ErrFabricEntryRequired", err)
	}
	if planned.Proposal != nil {
		t.Fatalf("needs-measurement path produced Scheduler proposal: %+v", planned.Proposal)
	}
}

func TestSchedulerPreferenceCannotOverridePlacementHardFailure(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir:hard-failure"))

	proposal := justifiedProposal(t, taskID, attemptID, taskIR, "worker-best")
	worker := passingAssessment("worker-best")
	worker.DataClassAllowed = assessmentCheck(false, "data-policy", "worker-best/data-class")

	result, err := placementauth.Authorize(placementauth.Request{
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
	if result.Outcome != placementauth.OutcomeRejected {
		t.Fatalf("Outcome = %q, want rejected", result.Outcome)
	}
	if result.Draft != nil {
		t.Fatalf("hard placement failure produced authority draft: %+v", result.Draft)
	}
	if len(result.Rejections) != 1 ||
		result.Rejections[0].Reason != placementauth.ReasonDataClassUnsatisfied {
		t.Fatalf("Rejections = %+v", result.Rejections)
	}
}

func TestOnlyFullyRevalidatedPathProducesPLC1Draft(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir:approved"))

	proposal := justifiedProposal(t, taskID, attemptID, taskIR, "worker-a")
	result, err := placementauth.Authorize(placementauth.Request{
		Proposal:       proposal,
		TaskID:         taskID,
		TaskIRDigest:   taskIR,
		AttemptID:      attemptID,
		PolicyRevision: 1,
		Worker:         passingAssessment("worker-a"),
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if result.Outcome != placementauth.OutcomeApproved || result.Draft == nil {
		t.Fatalf("result = %+v", result)
	}
	if !result.Draft.Valid() {
		t.Fatal("approved path produced invalid PLC-1 draft")
	}
	if result.Draft.DecisionKind() != authority.DecisionPlacementApproval {
		t.Fatalf("DecisionKind = %q", result.Draft.DecisionKind())
	}
	if result.Draft.RequiredAuthorityRole() != authority.RolePlacementAuthorization {
		t.Fatalf("RequiredAuthorityRole = %q", result.Draft.RequiredAuthorityRole())
	}
	if result.Draft.TaskID() != taskID ||
		result.Draft.AttemptID() != attemptID ||
		result.Draft.TaskIRDigest().String() != taskIR.String() ||
		result.Draft.WorkerIdentity() != "worker-a" {
		t.Fatalf("PLC-1 draft lost subject binding: %+v", result.Draft)
	}

	// This is deliberately the terminal authority object in the current core
	// flow. No Lease, ArtifactGrant, or WorkOrder is minted by Suitability,
	// Scheduler, or Placement Authorization.
}

func TestPolicyDriftStopsAuthorityChainAfterScheduler(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir:policy-drift"))

	proposal := justifiedProposal(t, taskID, attemptID, taskIR, "worker-a")
	result, err := placementauth.Authorize(placementauth.Request{
		Proposal:       proposal,
		TaskID:         taskID,
		TaskIRDigest:   taskIR,
		AttemptID:      attemptID,
		PolicyRevision: 2,
		Worker:         passingAssessment("worker-a"),
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if result.Draft != nil {
		t.Fatalf("policy drift produced PLC-1 draft: %+v", result.Draft)
	}
	found := false
	for _, rejection := range result.Rejections {
		if rejection.Reason == placementauth.ReasonPolicyRevisionChanged {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Rejections = %+v, want policy_revision_changed", result.Rejections)
	}
}

func justifiedProposal(
	t *testing.T,
	taskID ids.TaskID,
	attemptID ids.AttemptID,
	taskIR contract.Digest,
	worker string,
) *scheduler.Proposal {
	t.Helper()
	_, entry, err := suitability.EvaluateForFabricEntry(suitabilityInput(
		taskID,
		taskIR,
		60_000,
		100,
		20_000,
	))
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry() error = %v", err)
	}
	if entry == nil {
		t.Fatal("justified input did not produce FabricEntry")
	}
	planned, err := scheduler.Plan(scheduler.Request{
		TaskID:       taskID,
		TaskIRDigest: taskIR,
		AttemptID:    attemptID,
		FabricEntry:  entry,
		Candidates: []scheduler.Candidate{
			{
				WorkerIdentity:         worker,
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
	if planned.Proposal == nil {
		t.Fatal("justified path did not produce Scheduler proposal")
	}
	return planned.Proposal
}

func passingAssessment(worker string) placementauth.WorkerAssessment {
	return placementauth.WorkerAssessment{
		WorkerIdentity:       worker,
		Online:               assessmentCheck(true, "worker-session", worker+"/online"),
		ProviderActive:       assessmentCheck(true, "provider-state", worker+"/provider"),
		ExecutorSupported:    assessmentCheck(true, "executor-support", worker+"/executor"),
		DataClassAllowed:     assessmentCheck(true, "data-policy", worker+"/data-class"),
		ResourceFeasible:     assessmentCheck(true, "resource-feasibility", worker+"/resources"),
		AssuranceSatisfied:   assessmentCheck(true, "assurance", worker+"/assurance"),
		TransferWithinBudget: assessmentCheck(true, "network-budget", worker+"/transfer"),
	}
}

func assessmentCheck(satisfied bool, kind, identity string) placementauth.Check {
	return placementauth.Check{
		Satisfied: satisfied,
		Evidence: placementauth.EvidenceRef{
			Kind:     kind,
			Identity: identity,
			Digest:   contract.SHA256([]byte(kind + ":" + identity)),
		},
	}
}

func suitabilityInput(
	taskID ids.TaskID,
	taskIR contract.Digest,
	baselineMillis uint64,
	inputTransferMillis uint64,
	computeMillis uint64,
) suitability.Input {
	return suitability.Input{
		TaskID:               taskID,
		TaskIRDigest:         taskIR,
		WorkloadDigest:       contract.SHA256([]byte("fixture-workload:" + taskIR.String())),
		PolicyRevision:       1,
		EvaluatorVersion:     "suitability-v0",
		FeasibilityConfirmed: true,
		Baseline: &suitability.DurationEvidence{
			Millis:      baselineMillis,
			EvidenceRef: "fixture:local-baseline",
		},
		Components: []suitability.ComponentEstimate{
			{Kind: suitability.ComponentInputTransfer, Millis: inputTransferMillis, EvidenceRef: "fixture:input-transfer"},
			{Kind: suitability.ComponentCompute, Millis: computeMillis, EvidenceRef: "fixture:compute"},
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
