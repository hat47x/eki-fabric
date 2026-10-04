package placementauth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/scheduler"
)

var (
	ErrInvalidRequest  = errors.New("invalid placement authorization request")
	ErrInvalidEvidence = errors.New("invalid placement evidence")
)

type Outcome string

const (
	OutcomeApproved Outcome = "approved"
	OutcomeRejected Outcome = "rejected"
)

type ReasonCode string

const (
	ReasonProposalBindingMismatch ReasonCode = "proposal_binding_mismatch"
	ReasonPolicyRevisionChanged   ReasonCode = "policy_revision_changed"
	ReasonWorkerIdentityMismatch  ReasonCode = "worker_identity_mismatch"
	ReasonWorkerOffline           ReasonCode = "worker_offline"
	ReasonProviderInactive        ReasonCode = "provider_inactive"
	ReasonExecutorUnsupported     ReasonCode = "executor_unsupported"
	ReasonDataClassUnsatisfied    ReasonCode = "data_class_unsatisfied"
	ReasonResourceInfeasible      ReasonCode = "resource_infeasible"
	ReasonAssuranceUnsatisfied    ReasonCode = "assurance_unsatisfied"
	ReasonTransferBudgetExceeded  ReasonCode = "transfer_budget_exceeded"
)

type EvidenceRef struct {
	Kind     string
	Identity string
	Digest   contract.Digest
}

type Check struct {
	Satisfied bool
	Evidence  EvidenceRef
}

type WorkerAssessment struct {
	WorkerIdentity       string
	Online               Check
	ProviderActive       Check
	ExecutorSupported    Check
	DataClassAllowed     Check
	ResourceFeasible     Check
	AssuranceSatisfied   Check
	TransferWithinBudget Check
}

type Request struct {
	Proposal       *scheduler.Proposal
	TaskID         ids.TaskID
	TaskIRDigest   contract.Digest
	AttemptID      ids.AttemptID
	PolicyRevision uint64
	Worker         WorkerAssessment
}

type Rejection struct {
	Reason   ReasonCode
	Evidence *EvidenceRef
}

type ConditionEvidence struct {
	Condition string
	Evidence  EvidenceRef
}

type ApprovalDraft struct {
	taskID                 ids.TaskID
	taskIRDigest           contract.Digest
	attemptID              ids.AttemptID
	workerIdentity         string
	policyRevision         uint64
	suitabilityBasisDigest contract.Digest
	evidence               []ConditionEvidence
}

func (d ApprovalDraft) TaskID() ids.TaskID                     { return d.taskID }
func (d ApprovalDraft) TaskIRDigest() contract.Digest          { return d.taskIRDigest }
func (d ApprovalDraft) AttemptID() ids.AttemptID               { return d.attemptID }
func (d ApprovalDraft) WorkerIdentity() string                 { return d.workerIdentity }
func (d ApprovalDraft) PolicyRevision() uint64                 { return d.policyRevision }
func (d ApprovalDraft) SuitabilityBasisDigest() contract.Digest { return d.suitabilityBasisDigest }
func (d ApprovalDraft) DecisionKind() authority.DecisionKind   { return authority.DecisionPlacementApproval }
func (d ApprovalDraft) RequiredAuthorityRole() authority.Role  { return authority.RolePlacementAuthorization }

func (d ApprovalDraft) Evidence() []ConditionEvidence {
	return append([]ConditionEvidence(nil), d.evidence...)
}

func (d ApprovalDraft) Valid() bool {
	return d.taskID != "" &&
		!d.taskIRDigest.IsZero() &&
		d.attemptID != "" &&
		d.workerIdentity != "" &&
		d.policyRevision != 0 &&
		!d.suitabilityBasisDigest.IsZero() &&
		len(d.evidence) != 0
}

type Result struct {
	Outcome    Outcome
	Draft      *ApprovalDraft
	Rejections []Rejection
}

type namedCheck struct {
	condition string
	reason    ReasonCode
	check     Check
}

// Authorize independently re-checks the Scheduler proposal against current
// Placement Authority inputs. It returns an unsigned ApprovalDraft only when
// every hard condition is satisfied. The draft still requires PLC-1 signing;
// it is not a Lease, ArtifactGrant, or WorkOrder authority.
func Authorize(req Request) (Result, error) {
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}

	var rejections []Rejection
	if req.Proposal.TaskID() != req.TaskID ||
		req.Proposal.TaskIRDigest().String() != req.TaskIRDigest.String() ||
		req.Proposal.AttemptID() != req.AttemptID {
		rejections = append(rejections, Rejection{Reason: ReasonProposalBindingMismatch})
	}
	if req.Proposal.SuitabilityPolicyRevision() != req.PolicyRevision {
		rejections = append(rejections, Rejection{Reason: ReasonPolicyRevisionChanged})
	}
	if req.Proposal.WorkerIdentity() != req.Worker.WorkerIdentity {
		rejections = append(rejections, Rejection{Reason: ReasonWorkerIdentityMismatch})
	}

	checks := assessmentChecks(req.Worker)
	for _, item := range checks {
		if !item.check.Satisfied {
			evidence := item.check.Evidence
			rejections = append(rejections, Rejection{
				Reason:   item.reason,
				Evidence: &evidence,
			})
		}
	}
	if len(rejections) != 0 {
		return Result{
			Outcome:    OutcomeRejected,
			Rejections: rejections,
		}, nil
	}

	evidence := make([]ConditionEvidence, 0, len(checks))
	for _, item := range checks {
		evidence = append(evidence, ConditionEvidence{
			Condition: item.condition,
			Evidence:  item.check.Evidence,
		})
	}
	draft := &ApprovalDraft{
		taskID:                 req.TaskID,
		taskIRDigest:           req.TaskIRDigest,
		attemptID:              req.AttemptID,
		workerIdentity:         req.Worker.WorkerIdentity,
		policyRevision:         req.PolicyRevision,
		suitabilityBasisDigest: req.Proposal.SuitabilityBasisDigest(),
		evidence:               evidence,
	}
	if !draft.Valid() {
		return Result{}, fmt.Errorf("%w: produced placement draft is incomplete", ErrInvalidRequest)
	}
	return Result{
		Outcome: OutcomeApproved,
		Draft:   draft,
	}, nil
}

func validateRequest(req Request) error {
	if req.Proposal == nil {
		return fmt.Errorf("%w: Scheduler proposal is required", ErrInvalidRequest)
	}
	if _, err := ids.ParseTaskID(req.TaskID.String()); err != nil {
		return fmt.Errorf("%w: task ID: %v", ErrInvalidRequest, err)
	}
	if req.TaskIRDigest.IsZero() {
		return fmt.Errorf("%w: TaskIR digest is required", ErrInvalidRequest)
	}
	if _, err := ids.ParseAttemptID(req.AttemptID.String()); err != nil {
		return fmt.Errorf("%w: attempt ID: %v", ErrInvalidRequest, err)
	}
	if req.PolicyRevision == 0 {
		return fmt.Errorf("%w: policy revision must be positive", ErrInvalidRequest)
	}
	if req.Worker.WorkerIdentity == "" ||
		strings.TrimSpace(req.Worker.WorkerIdentity) != req.Worker.WorkerIdentity ||
		len(req.Worker.WorkerIdentity) > 256 {
		return fmt.Errorf("%w: Worker identity is empty, padded, or too long", ErrInvalidRequest)
	}
	for _, item := range assessmentChecks(req.Worker) {
		if err := validateEvidence(item.check.Evidence); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidEvidence, item.condition, err)
		}
	}
	return nil
}

func validateEvidence(ref EvidenceRef) error {
	if ref.Kind == "" || strings.TrimSpace(ref.Kind) != ref.Kind || len(ref.Kind) > 128 {
		return fmt.Errorf("kind is empty, padded, or too long")
	}
	if ref.Identity == "" || strings.TrimSpace(ref.Identity) != ref.Identity || len(ref.Identity) > 256 {
		return fmt.Errorf("identity is empty, padded, or too long")
	}
	if ref.Digest.IsZero() {
		return fmt.Errorf("digest is required")
	}
	return nil
}

func assessmentChecks(worker WorkerAssessment) []namedCheck {
	return []namedCheck{
		{condition: "worker_online", reason: ReasonWorkerOffline, check: worker.Online},
		{condition: "provider_active", reason: ReasonProviderInactive, check: worker.ProviderActive},
		{condition: "executor_supported", reason: ReasonExecutorUnsupported, check: worker.ExecutorSupported},
		{condition: "data_class_allowed", reason: ReasonDataClassUnsatisfied, check: worker.DataClassAllowed},
		{condition: "resource_feasible", reason: ReasonResourceInfeasible, check: worker.ResourceFeasible},
		{condition: "assurance_satisfied", reason: ReasonAssuranceUnsatisfied, check: worker.AssuranceSatisfied},
		{condition: "transfer_within_budget", reason: ReasonTransferBudgetExceeded, check: worker.TransferWithinBudget},
	}
}
