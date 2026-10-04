package scheduler

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/suitability"
)

func TestPlanRequiresTaskBoundFabricEntryAndProducesNonAuthoritativeProposal(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	entry := justifiedEntry(t, taskID, taskIR)

	result, err := Plan(Request{
		TaskID:       taskID,
		TaskIRDigest: taskIR,
		AttemptID:    attemptID,
		FabricEntry:  entry,
		Candidates: []Candidate{
			{WorkerIdentity: "worker-b", PlanningEligible: true, CacheHit: false, EstimatedTransferBytes: 100, AvailableCPUMillis: 5_000},
			{WorkerIdentity: "worker-c", PlanningEligible: true, CacheHit: true, EstimatedTransferBytes: 200, AvailableCPUMillis: 1_000},
			{WorkerIdentity: "worker-a", PlanningEligible: true, CacheHit: true, EstimatedTransferBytes: 100, AvailableCPUMillis: 500},
			{WorkerIdentity: "worker-z", PlanningEligible: false, PlanningReason: "local_governor_paused"},
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Outcome != OutcomeProposed || result.Proposal == nil {
		t.Fatalf("result = %+v", result)
	}
	proposal := result.Proposal
	if proposal.TaskID() != taskID ||
		proposal.AttemptID() != attemptID ||
		proposal.TaskIRDigest().String() != taskIR.String() {
		t.Fatalf("proposal lost Task/Attempt binding: %+v", proposal)
	}
	if proposal.WorkerIdentity() != "worker-a" {
		t.Fatalf("WorkerIdentity = %q, want worker-a", proposal.WorkerIdentity())
	}
	if proposal.SuitabilityBasisDigest().String() != entry.BasisDigest().String() ||
		proposal.SuitabilityWorkloadDigest().String() != entry.WorkloadDigest().String() ||
		proposal.SuitabilityPolicyRevision() != entry.PolicyRevision() {
		t.Fatalf("proposal lost Suitability basis: %+v", proposal)
	}
	if len(result.Rejections) != 1 ||
		result.Rejections[0].WorkerIdentity != "worker-z" ||
		result.Rejections[0].Reason != "local_governor_paused" {
		t.Fatalf("Rejections = %+v", result.Rejections)
	}
}

func TestPlanCannotCreateProposalWithoutJustifiedFabricEntry(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))

	receipt, entry, err := suitability.EvaluateForFabricEntry(notJustifiedInput(taskID, taskIR))
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry() error = %v", err)
	}
	if receipt.Decision != suitability.DecisionFabricNotJustified || entry != nil {
		t.Fatalf("Suitability result = %+v entry=%+v", receipt, entry)
	}

	result, err := Plan(Request{
		TaskID:       taskID,
		TaskIRDigest: taskIR,
		AttemptID:    attemptID,
		FabricEntry:  entry,
		Candidates: []Candidate{
			{WorkerIdentity: "worker-a", PlanningEligible: true},
		},
	})
	if !errors.Is(err, ErrFabricEntryRequired) {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Proposal != nil {
		t.Fatalf("not-justified path produced proposal: %+v", result.Proposal)
	}
}

func TestPlanRejectsCrossTaskOrChangedTaskIRReuse(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	otherTaskID := mustTaskID(t, "tsk_00000000000000000000000000000002")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	entry := justifiedEntry(t, taskID, taskIR)

	tests := []struct {
		name   string
		taskID ids.TaskID
		taskIR contract.Digest
	}{
		{name: "another Task", taskID: otherTaskID, taskIR: taskIR},
		{name: "changed TaskIR", taskID: taskID, taskIR: contract.SHA256([]byte("changed-task-ir"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Plan(Request{
				TaskID:       tt.taskID,
				TaskIRDigest: tt.taskIR,
				AttemptID:    attemptID,
				FabricEntry:  entry,
				Candidates: []Candidate{
					{WorkerIdentity: "worker-a", PlanningEligible: true},
				},
			})
			if !errors.Is(err, ErrFabricEntryTaskMismatch) {
				t.Fatalf("Plan() error = %v", err)
			}
			if result.Proposal != nil {
				t.Fatalf("mismatched Task produced proposal: %+v", result.Proposal)
			}
		})
	}
}

func TestPlanNoEligibleCandidateReturnsNoProposalAndKeepsReasons(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	entry := justifiedEntry(t, taskID, taskIR)

	result, err := Plan(Request{
		TaskID:       taskID,
		TaskIRDigest: taskIR,
		AttemptID:    attemptID,
		FabricEntry:  entry,
		Candidates: []Candidate{
			{WorkerIdentity: "worker-b", PlanningEligible: false, PlanningReason: "executor_unsupported"},
			{WorkerIdentity: "worker-a", PlanningEligible: false},
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Outcome != OutcomeNoCandidate || result.Proposal != nil {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Rejections) != 2 ||
		result.Rejections[0].WorkerIdentity != "worker-a" ||
		result.Rejections[0].Reason != "planning_ineligible" ||
		result.Rejections[1].WorkerIdentity != "worker-b" ||
		result.Rejections[1].Reason != "executor_unsupported" {
		t.Fatalf("Rejections = %+v", result.Rejections)
	}
}

func TestPlanUsesDeterministicLexicographicTieBreak(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	entry := justifiedEntry(t, taskID, taskIR)

	result, err := Plan(Request{
		TaskID:       taskID,
		TaskIRDigest: taskIR,
		AttemptID:    attemptID,
		FabricEntry:  entry,
		Candidates: []Candidate{
			{WorkerIdentity: "worker-z", PlanningEligible: true, CacheHit: true, EstimatedTransferBytes: 10, AvailableCPUMillis: 1_000},
			{WorkerIdentity: "worker-a", PlanningEligible: true, CacheHit: true, EstimatedTransferBytes: 10, AvailableCPUMillis: 1_000},
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Proposal == nil || result.Proposal.WorkerIdentity() != "worker-a" {
		t.Fatalf("proposal = %+v", result.Proposal)
	}
}

func TestPlanRejectsDuplicateOrMalformedWorkerIdentity(t *testing.T) {
	taskID := mustTaskID(t, "tsk_00000000000000000000000000000001")
	attemptID := mustAttemptID(t, "atm_00000000000000000000000000000001")
	taskIR := contract.SHA256([]byte("task-ir"))
	entry := justifiedEntry(t, taskID, taskIR)

	tests := []struct {
		name       string
		candidates []Candidate
	}{
		{
			name: "duplicate",
			candidates: []Candidate{
				{WorkerIdentity: "worker-a", PlanningEligible: true},
				{WorkerIdentity: "worker-a", PlanningEligible: true},
			},
		},
		{
			name: "padded",
			candidates: []Candidate{
				{WorkerIdentity: " worker-a ", PlanningEligible: true},
			},
		},
		{
			name: "eligible with rejection reason",
			candidates: []Candidate{
				{WorkerIdentity: "worker-a", PlanningEligible: true, PlanningReason: "should-not-be-here"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Plan(Request{
				TaskID:       taskID,
				TaskIRDigest: taskIR,
				AttemptID:    attemptID,
				FabricEntry:  entry,
				Candidates:   tt.candidates,
			}); !errors.Is(err, ErrInvalidCandidate) {
				t.Fatalf("Plan() error = %v", err)
			}
		})
	}
}

func justifiedEntry(t *testing.T, taskID ids.TaskID, taskIR contract.Digest) *suitability.FabricEntry {
	t.Helper()
	_, entry, err := suitability.EvaluateForFabricEntry(suitabilityInput(
		taskID,
		taskIR,
		60_000,
		[]suitability.ComponentEstimate{
			{Kind: suitability.ComponentInputTransfer, Millis: 100, EvidenceRef: "fixture:input"},
			{Kind: suitability.ComponentCompute, Millis: 20_000, EvidenceRef: "fixture:compute"},
		},
	))
	if err != nil {
		t.Fatalf("EvaluateForFabricEntry() error = %v", err)
	}
	if entry == nil {
		t.Fatal("justified input did not produce FabricEntry")
	}
	return entry
}

func notJustifiedInput(taskID ids.TaskID, taskIR contract.Digest) suitability.Input {
	return suitabilityInput(
		taskID,
		taskIR,
		60_000,
		[]suitability.ComponentEstimate{
			{Kind: suitability.ComponentInputTransfer, Millis: 81_920, EvidenceRef: "fixture:input"},
			{Kind: suitability.ComponentCompute, Millis: 20_000, EvidenceRef: "fixture:compute"},
		},
	)
}

func suitabilityInput(
	taskID ids.TaskID,
	taskIR contract.Digest,
	baselineMillis uint64,
	components []suitability.ComponentEstimate,
) suitability.Input {
	included := map[suitability.ComponentKind]bool{}
	for _, component := range components {
		included[component.Kind] = true
	}
	all := []suitability.ComponentKind{
		suitability.ComponentSuitabilityDecision,
		suitability.ComponentAdmissionPlacement,
		suitability.ComponentDispatch,
		suitability.ComponentQueue,
		suitability.ComponentInputTransfer,
		suitability.ComponentCompute,
		suitability.ComponentRetryPenalty,
		suitability.ComponentOutputTransfer,
		suitability.ComponentVerification,
	}
	var excluded []suitability.ComponentExclusion
	for _, kind := range all {
		if included[kind] {
			continue
		}
		excluded = append(excluded, suitability.ComponentExclusion{
			Kind:   kind,
			Reason: "fixture explicitly excludes this path cost",
		})
	}
	return suitability.Input{
		TaskID:                taskID,
		TaskIRDigest:          taskIR,
		WorkloadDigest:        contract.SHA256([]byte("fixture-workload")),
		PolicyRevision:        1,
		EvaluatorVersion:      "suitability-v0",
		FeasibilityConfirmed:  true,
		Baseline:              &suitability.DurationEvidence{Millis: baselineMillis, EvidenceRef: "fixture:baseline"},
		Components:            components,
		Excluded:              excluded,
		MinimumBenefitMillis:  1_000,
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
