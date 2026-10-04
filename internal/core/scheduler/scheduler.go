package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/suitability"
)

var (
	ErrInvalidRequest          = errors.New("invalid scheduler request")
	ErrFabricEntryRequired     = errors.New("valid Fabric entry is required")
	ErrFabricEntryTaskMismatch = errors.New("Fabric entry does not match Task/TaskIR")
	ErrInvalidCandidate        = errors.New("invalid scheduling candidate")
)

type Outcome string

const (
	OutcomeProposed    Outcome = "proposed"
	OutcomeNoCandidate Outcome = "no_candidate"
)

type Candidate struct {
	WorkerIdentity         string
	PlanningEligible       bool
	PlanningReason         string
	CacheHit               bool
	EstimatedTransferBytes uint64
	AvailableCPUMillis     uint64
}

type Rejection struct {
	WorkerIdentity string
	Reason         string
}

type RankBasis struct {
	CacheHit               bool
	EstimatedTransferBytes uint64
	AvailableCPUMillis     uint64
}

type Proposal struct {
	taskID                    ids.TaskID
	taskIRDigest              contract.Digest
	attemptID                 ids.AttemptID
	workerIdentity            string
	suitabilityWorkloadDigest contract.Digest
	suitabilityBasisDigest    contract.Digest
	suitabilityPolicyRevision uint64
	rankBasis                 RankBasis
}

func (p Proposal) TaskID() ids.TaskID                         { return p.taskID }
func (p Proposal) TaskIRDigest() contract.Digest              { return p.taskIRDigest }
func (p Proposal) AttemptID() ids.AttemptID                   { return p.attemptID }
func (p Proposal) WorkerIdentity() string                     { return p.workerIdentity }
func (p Proposal) SuitabilityWorkloadDigest() contract.Digest { return p.suitabilityWorkloadDigest }
func (p Proposal) SuitabilityBasisDigest() contract.Digest    { return p.suitabilityBasisDigest }
func (p Proposal) SuitabilityPolicyRevision() uint64          { return p.suitabilityPolicyRevision }
func (p Proposal) RankBasis() RankBasis                       { return p.rankBasis }

type Request struct {
	TaskID       ids.TaskID
	TaskIRDigest contract.Digest
	AttemptID    ids.AttemptID
	FabricEntry  *suitability.FabricEntry
	Candidates   []Candidate
}

type Result struct {
	Outcome    Outcome
	Proposal   *Proposal
	Rejections []Rejection
}

// Plan creates a non-authoritative Placement Proposal.
//
// Candidates are internal planning views assembled from normalized Control
// Plane state. They are not Client request fields. PlanningEligible is only a
// scheduler hard-filter hint; PLC-1 must independently re-check security,
// assurance, data and provider constraints before issuing Placement Approval.
func Plan(req Request) (Result, error) {
	if _, err := ids.ParseTaskID(req.TaskID.String()); err != nil {
		return Result{}, fmt.Errorf("%w: task ID: %v", ErrInvalidRequest, err)
	}
	if req.TaskIRDigest.IsZero() {
		return Result{}, fmt.Errorf("%w: TaskIR digest is required", ErrInvalidRequest)
	}
	if _, err := ids.ParseAttemptID(req.AttemptID.String()); err != nil {
		return Result{}, fmt.Errorf("%w: attempt ID: %v", ErrInvalidRequest, err)
	}
	if req.FabricEntry == nil || !req.FabricEntry.Valid() {
		return Result{}, ErrFabricEntryRequired
	}
	if !req.FabricEntry.MatchesTask(req.TaskID, req.TaskIRDigest) {
		return Result{}, ErrFabricEntryTaskMismatch
	}
	if len(req.Candidates) == 0 {
		return Result{Outcome: OutcomeNoCandidate}, nil
	}

	seen := make(map[string]struct{}, len(req.Candidates))
	eligible := make([]Candidate, 0, len(req.Candidates))
	rejections := make([]Rejection, 0)
	for _, candidate := range req.Candidates {
		if err := validateCandidate(candidate); err != nil {
			return Result{}, err
		}
		if _, exists := seen[candidate.WorkerIdentity]; exists {
			return Result{}, fmt.Errorf("%w: duplicate Worker identity %q", ErrInvalidCandidate, candidate.WorkerIdentity)
		}
		seen[candidate.WorkerIdentity] = struct{}{}

		if !candidate.PlanningEligible {
			reason := candidate.PlanningReason
			if reason == "" {
				reason = "planning_ineligible"
			}
			rejections = append(rejections, Rejection{
				WorkerIdentity: candidate.WorkerIdentity,
				Reason:         reason,
			})
			continue
		}
		eligible = append(eligible, candidate)
	}

	sort.Slice(rejections, func(i, j int) bool {
		return rejections[i].WorkerIdentity < rejections[j].WorkerIdentity
	})
	if len(eligible) == 0 {
		return Result{
			Outcome:    OutcomeNoCandidate,
			Rejections: rejections,
		}, nil
	}

	sort.Slice(eligible, func(i, j int) bool {
		left, right := eligible[i], eligible[j]
		if left.CacheHit != right.CacheHit {
			return left.CacheHit
		}
		if left.EstimatedTransferBytes != right.EstimatedTransferBytes {
			return left.EstimatedTransferBytes < right.EstimatedTransferBytes
		}
		if left.AvailableCPUMillis != right.AvailableCPUMillis {
			return left.AvailableCPUMillis > right.AvailableCPUMillis
		}
		return left.WorkerIdentity < right.WorkerIdentity
	})
	selected := eligible[0]

	return Result{
		Outcome: OutcomeProposed,
		Proposal: &Proposal{
			taskID:                    req.TaskID,
			taskIRDigest:              req.TaskIRDigest,
			attemptID:                 req.AttemptID,
			workerIdentity:            selected.WorkerIdentity,
			suitabilityWorkloadDigest: req.FabricEntry.WorkloadDigest(),
			suitabilityBasisDigest:    req.FabricEntry.BasisDigest(),
			suitabilityPolicyRevision: req.FabricEntry.PolicyRevision(),
			rankBasis: RankBasis{
				CacheHit:               selected.CacheHit,
				EstimatedTransferBytes: selected.EstimatedTransferBytes,
				AvailableCPUMillis:     selected.AvailableCPUMillis,
			},
		},
		Rejections: rejections,
	}, nil
}

func validateCandidate(candidate Candidate) error {
	if candidate.WorkerIdentity == "" ||
		strings.TrimSpace(candidate.WorkerIdentity) != candidate.WorkerIdentity ||
		len(candidate.WorkerIdentity) > 256 {
		return fmt.Errorf("%w: Worker identity is empty, padded, or too long", ErrInvalidCandidate)
	}
	if candidate.PlanningEligible && candidate.PlanningReason != "" {
		return fmt.Errorf("%w: eligible Worker %q cannot carry an ineligibility reason", ErrInvalidCandidate, candidate.WorkerIdentity)
	}
	if !candidate.PlanningEligible && strings.TrimSpace(candidate.PlanningReason) != candidate.PlanningReason {
		return fmt.Errorf("%w: Worker %q planning reason contains surrounding whitespace", ErrInvalidCandidate, candidate.WorkerIdentity)
	}
	return nil
}
