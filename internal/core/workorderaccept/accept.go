// Package workorderaccept decides whether a Worker may treat a verified
// WorkOrder as an admission to run one Attempt.
//
// The boundary combines four things that must agree:
//
//  1. the signed WorkOrder, verified by workorderverify;
//  2. the Worker's own durable Lease record for this Attempt;
//  3. the Placement Approval and Executor Approval the WorkOrder names;
//  4. a one-time claim of the WorkOrder nonce.
//
// Success produces an Admission. An Admission is still not permission to start
// an Executor: it records that the instruction was current and unused at
// AdmittedAt, and it carries the limits the Worker must apply. The Local
// Governor can reduce or stop the execution at any later moment without asking
// the Control Plane.
package workorderaccept

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workorderverify"
	"github.com/hat47x/eki-fabric/internal/spi/replayguard"
)

var (
	ErrInvalidConfig     = errors.New("invalid WorkOrder acceptance configuration")
	ErrInvalidRequest    = errors.New("invalid WorkOrder acceptance request")
	ErrLeaseMismatch     = errors.New("WorkOrder does not match the local Lease")
	ErrLeaseExpired      = errors.New("the local Lease is not active")
	ErrPlacementMismatch = errors.New("WorkOrder does not match the Placement Approval")
	ErrExecutorMismatch  = errors.New("WorkOrder does not match the Executor Approval")
	ErrReplay            = errors.New("WorkOrder nonce has already been used")
	ErrReplayUnknown     = errors.New("WorkOrder nonce claim outcome is unknown")
)

type Clock interface{ Now() time.Time }

// LeaseEvidence is the Worker's own durable record of the Lease for this
// Attempt. It is read from local state, not from the WorkOrder bytes.
type LeaseEvidence struct {
	DecisionID     string
	PayloadDigest  contract.Digest
	TaskID         ids.TaskID
	TaskIRDigest   contract.Digest
	AttemptID      ids.AttemptID
	WorkerIdentity string
	FenceEpoch     uint64
	ExpiresAt      time.Time
}

// PlacementEvidence is the Placement Approval the Worker holds for this
// Attempt. The WorkOrder's reference must point at exactly this payload.
type PlacementEvidence struct {
	DecisionID     string
	PayloadDigest  contract.Digest
	TaskID         ids.TaskID
	TaskIRDigest   contract.Digest
	AttemptID      ids.AttemptID
	WorkerIdentity string
}

// ExecutorEvidence is the Executor Approval the Worker holds for this Attempt.
type ExecutorEvidence struct {
	DecisionID     string
	PayloadDigest  contract.Digest
	ExecutorDigest contract.Digest
}

type Config struct {
	Verifier *workorderverify.Verifier
	Replay   replayguard.Guard
	Clock    Clock
}

type Service struct {
	verifier *workorderverify.Verifier
	replay   replayguard.Guard
	clock    Clock
}

func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Verifier == nil:
		return nil, fmt.Errorf("%w: WorkOrder verifier is required", ErrInvalidConfig)
	case cfg.Replay == nil:
		return nil, fmt.Errorf("%w: nonce replay guard is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}
	return &Service{verifier: cfg.Verifier, replay: cfg.Replay, clock: cfg.Clock}, nil
}

type Request struct {
	// Raw is the hostile signed WorkOrder received from the Control Plane.
	Raw         []byte
	TrustBundle decisiontrust.TrustBundleMetadata
	Lease       LeaseEvidence
	Placement   PlacementEvidence
	Executor    ExecutorEvidence
}

// Admission is the result of accepting one WorkOrder. It is local evidence
// that the instruction was current and its nonce was unused at AdmittedAt. It
// is not a Lease and not permission to start an Executor.
type Admission struct {
	orderDecisionID string
	workOrderID     string
	taskID          ids.TaskID
	taskIRDigest    contract.Digest
	attemptID       ids.AttemptID
	workerIdentity  string
	leaseDecisionID string
	fenceEpoch      uint64
	executorDigest  contract.Digest
	nonce           []byte
	limits          decisionpayload.ResourceLimits
	sandbox         decisionpayload.SandboxPolicy
	admittedAt      time.Time
	expiresAt       time.Time
	keyID           decisiontrust.KeyID
	trustBundleRev  uint64
}

func (a Admission) OrderDecisionID() string                { return a.orderDecisionID }
func (a Admission) WorkOrderID() string                    { return a.workOrderID }
func (a Admission) TaskID() ids.TaskID                     { return a.taskID }
func (a Admission) TaskIRDigest() contract.Digest          { return a.taskIRDigest }
func (a Admission) AttemptID() ids.AttemptID               { return a.attemptID }
func (a Admission) WorkerIdentity() string                 { return a.workerIdentity }
func (a Admission) LeaseDecisionID() string                { return a.leaseDecisionID }
func (a Admission) FenceEpoch() uint64                     { return a.fenceEpoch }
func (a Admission) ExecutorDigest() contract.Digest        { return a.executorDigest }
func (a Admission) Limits() decisionpayload.ResourceLimits { return a.limits }
func (a Admission) Sandbox() decisionpayload.SandboxPolicy { return a.sandbox }
func (a Admission) AdmittedAt() time.Time                  { return a.admittedAt }
func (a Admission) ExpiresAt() time.Time                   { return a.expiresAt }
func (a Admission) KeyID() decisiontrust.KeyID             { return a.keyID }
func (a Admission) TrustBundleRevision() uint64            { return a.trustBundleRev }
func (a Admission) Nonce() []byte                          { return append([]byte(nil), a.nonce...) }

// IsZero reports whether the Admission was produced by Accept.
func (a Admission) IsZero() bool { return a.orderDecisionID == "" }

// Accept verifies the WorkOrder, checks it against the Worker's own Lease,
// Placement Approval, and Executor Approval, and claims the nonce exactly once.
//
// Ordering matters: the nonce is claimed only after every other check passes,
// so a mismatched or expired instruction cannot burn a nonce. A replay guard
// error is reported as unknown and must be treated as a refusal.
func (s *Service) Accept(ctx context.Context, req Request) (Admission, error) {
	if s == nil || s.verifier == nil || s.replay == nil || s.clock == nil {
		return Admission{}, fmt.Errorf("%w: service is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return Admission{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return Admission{}, err
	}
	now := s.clock.Now().UTC()
	if now.IsZero() {
		return Admission{}, fmt.Errorf("%w: clock returned zero time", ErrInvalidRequest)
	}
	if err := validateEvidence(req); err != nil {
		return Admission{}, err
	}

	verified, err := s.verifier.Verify(workorderverify.Request{
		Raw: req.Raw, Now: now, TrustBundle: req.TrustBundle,
	})
	if err != nil {
		return Admission{}, err
	}
	order := verified.Order()

	if order.TaskID() != req.Lease.TaskID ||
		order.TaskIRDigest() != req.Lease.TaskIRDigest ||
		order.AttemptID() != req.Lease.AttemptID ||
		order.WorkerIdentity() != req.Lease.WorkerIdentity ||
		order.LeaseRef().DecisionID() != req.Lease.DecisionID ||
		order.LeaseRef().PayloadDigest() != req.Lease.PayloadDigest {
		return Admission{}, ErrLeaseMismatch
	}
	if req.Lease.FenceEpoch == 0 || !now.Before(req.Lease.ExpiresAt) {
		return Admission{}, ErrLeaseExpired
	}
	if order.PlacementRef().DecisionID() != req.Placement.DecisionID ||
		order.PlacementRef().PayloadDigest() != req.Placement.PayloadDigest ||
		req.Placement.TaskID != order.TaskID() ||
		req.Placement.TaskIRDigest != order.TaskIRDigest() ||
		req.Placement.AttemptID != order.AttemptID() ||
		req.Placement.WorkerIdentity != order.WorkerIdentity() {
		return Admission{}, ErrPlacementMismatch
	}
	if order.ExecutorApprovalRef().DecisionID() != req.Executor.DecisionID ||
		order.ExecutorApprovalRef().PayloadDigest() != req.Executor.PayloadDigest ||
		order.ExecutorDigest() != req.Executor.ExecutorDigest {
		return Admission{}, ErrExecutorMismatch
	}

	expiresAt := order.ExpiresAt()
	if req.Lease.ExpiresAt.Before(expiresAt) {
		expiresAt = req.Lease.ExpiresAt
	}
	claimed, err := s.replay.Claim(ctx, order.Nonce(), expiresAt)
	if err != nil {
		return Admission{}, fmt.Errorf("%w: %w", ErrReplayUnknown, err)
	}
	if !claimed {
		return Admission{}, ErrReplay
	}
	return Admission{
		orderDecisionID: order.DecisionID(),
		workOrderID:     order.WorkOrderID(),
		taskID:          order.TaskID(),
		taskIRDigest:    order.TaskIRDigest(),
		attemptID:       order.AttemptID(),
		workerIdentity:  order.WorkerIdentity(),
		leaseDecisionID: req.Lease.DecisionID,
		fenceEpoch:      req.Lease.FenceEpoch,
		executorDigest:  order.ExecutorDigest(),
		nonce:           order.Nonce(),
		limits:          order.Limits(),
		sandbox:         order.Sandbox(),
		admittedAt:      now,
		expiresAt:       expiresAt.UTC(),
		keyID:           verified.KeyID(),
		trustBundleRev:  verified.TrustBundleRevision(),
	}, nil
}

func validateEvidence(req Request) error {
	switch {
	case req.Lease.DecisionID == "":
		return fmt.Errorf("%w: Lease decision ID is required", ErrInvalidRequest)
	case req.Lease.PayloadDigest.IsZero():
		return fmt.Errorf("%w: Lease payload digest is required", ErrInvalidRequest)
	case req.Lease.TaskID == "" || req.Lease.TaskIRDigest.IsZero() || req.Lease.AttemptID == "":
		return fmt.Errorf("%w: Lease subject is required", ErrInvalidRequest)
	case req.Lease.WorkerIdentity == "":
		return fmt.Errorf("%w: Lease Worker identity is required", ErrInvalidRequest)
	case req.Placement.DecisionID == "":
		return fmt.Errorf("%w: Placement decision ID is required", ErrInvalidRequest)
	case req.Placement.PayloadDigest.IsZero():
		return fmt.Errorf("%w: Placement payload digest is required", ErrInvalidRequest)
	case req.Executor.DecisionID == "":
		return fmt.Errorf("%w: Executor decision ID is required", ErrInvalidRequest)
	case req.Executor.PayloadDigest.IsZero():
		return fmt.Errorf("%w: Executor payload digest is required", ErrInvalidRequest)
	case req.Executor.ExecutorDigest.IsZero():
		return fmt.Errorf("%w: Executor artifact digest is required", ErrInvalidRequest)
	}
	return nil
}
