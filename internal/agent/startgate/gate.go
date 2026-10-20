// Package startgate compares an accepted WorkOrder with the Local Governor's
// current decision before any Executor starts.
//
// An admitted WorkOrder carries the maximum resources the Control Plane
// granted. The Local Governor carries what the person on this endpoint allows
// right now, as a percentage of the machine. The effective limit is the smaller
// of the two, and a resource the governor refuses stops the start.
//
// A Plan is local evidence that one start was allowed with one set of limits.
// It is not enforcement: the caller must apply the limits and must stop the
// execution when a later governor decision stops running work. This package
// never starts an Executor.
package startgate

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workorderaccept"
)

var (
	ErrInvalidConfig    = errors.New("invalid start gate configuration")
	ErrNotAdmitted      = errors.New("the WorkOrder was not admitted")
	ErrGovernorStopped  = errors.New("the Local Governor stops running work")
	ErrNotAccepting     = errors.New("the Local Governor is not accepting new work")
	ErrNoCapacity       = errors.New("the Local Governor allows no capacity for a required resource")
	ErrMachineUnknown   = errors.New("the machine profile is missing a required capacity")
	ErrAdmissionExpired = errors.New("the Admission has expired")
)

type Clock interface{ Now() time.Time }

// MachineProfile is the local, measurable capacity of this endpoint. It is
// detected by the agent, not received from the Control Plane.
type MachineProfile struct {
	CPUMillis   uint64
	MemoryBytes uint64
	GPUDevices  uint32
}

type Config struct {
	Clock Clock
}

type Gate struct {
	clock Clock
}

func New(cfg Config) (*Gate, error) {
	if cfg.Clock == nil {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}
	return &Gate{clock: cfg.Clock}, nil
}

type Request struct {
	Admission workorderaccept.Admission
	Governor  governor.Decision
	Machine   MachineProfile
}

// Plan is the result of one start check. The effective limits are already the
// smaller of the WorkOrder grant and the governor ceiling, and the deadline is
// no later than the Admission expiry.
type Plan struct {
	taskID          ids.TaskID
	attemptID       ids.AttemptID
	orderDecisionID string
	executorDigest  contract.Digest
	nonce           []byte
	limits          decisionpayload.ResourceLimits
	sandbox         decisionpayload.SandboxPolicy
	plannedAt       time.Time
	deadline        time.Time
	reasons         []governor.Reason
	notEnforceab    []governor.Resource
}

func (p Plan) TaskID() ids.TaskID                     { return p.taskID }
func (p Plan) AttemptID() ids.AttemptID               { return p.attemptID }
func (p Plan) OrderDecisionID() string                { return p.orderDecisionID }
func (p Plan) ExecutorDigest() contract.Digest        { return p.executorDigest }
func (p Plan) Nonce() []byte                          { return append([]byte(nil), p.nonce...) }
func (p Plan) Limits() decisionpayload.ResourceLimits { return p.limits }
func (p Plan) Sandbox() decisionpayload.SandboxPolicy { return p.sandbox }
func (p Plan) PlannedAt() time.Time                   { return p.plannedAt }
func (p Plan) Deadline() time.Time                    { return p.deadline }
func (p Plan) Reasons() []governor.Reason {
	return append([]governor.Reason(nil), p.reasons...)
}
func (p Plan) NotEnforceable() []governor.Resource {
	return append([]governor.Resource(nil), p.notEnforceab...)
}
func (p Plan) IsZero() bool { return p.taskID == "" }

// Check returns a Plan when the endpoint may start this Attempt now. Every
// refusal is a sentinel error; a caller that ignores the error still gets a
// zero Plan and must not start.
func (g *Gate) Check(req Request) (Plan, error) {
	if g == nil || g.clock == nil {
		return Plan{}, fmt.Errorf("%w: gate is not initialized", ErrInvalidConfig)
	}
	if req.Admission.IsZero() {
		return Plan{}, ErrNotAdmitted
	}
	now := g.clock.Now().UTC()
	if now.IsZero() {
		return Plan{}, fmt.Errorf("%w: clock returned zero time", ErrInvalidConfig)
	}
	if !now.Before(req.Admission.ExpiresAt()) {
		return Plan{}, ErrAdmissionExpired
	}
	if req.Governor.StopRunning {
		return Plan{}, ErrGovernorStopped
	}
	if !req.Governor.AcceptNewLeases {
		return Plan{}, ErrNotAccepting
	}

	order := req.Admission.Limits()
	if err := requireCapacity(req.Governor, order, req.Admission.Sandbox()); err != nil {
		return Plan{}, err
	}

	limits := order
	cpu, err := scale(req.Machine.CPUMillis, req.Governor.Ceilings[governor.ResourceCPU], order.CPUMillis)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %v", ErrMachineUnknown, err)
	}
	limits.CPUMillis = cpu
	memory, err := scale(req.Machine.MemoryBytes, req.Governor.Ceilings[governor.ResourceMemory], order.MemoryBytes)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %v", ErrMachineUnknown, err)
	}
	limits.MemoryBytes = memory

	sandbox := req.Admission.Sandbox()
	if sandbox.GPU != nil {
		allowed := uint32(uint64(req.Machine.GPUDevices) * uint64(req.Governor.Ceilings[governor.ResourceGPU]) / 100)
		if allowed == 0 {
			return Plan{}, fmt.Errorf("%w: gpu", ErrNoCapacity)
		}
		if allowed < sandbox.GPU.DeviceCount {
			sandbox.GPU = &decisionpayload.GPULimit{
				DeviceCount:      allowed,
				MinimumVRAMBytes: sandbox.GPU.MinimumVRAMBytes,
			}
		}
	}

	deadline := req.Admission.ExpiresAt()
	if wall := time.Duration(order.WallTimeMillis) * time.Millisecond; now.Add(wall).Before(deadline) {
		deadline = now.Add(wall)
	}
	return Plan{
		taskID:          req.Admission.TaskID(),
		attemptID:       req.Admission.AttemptID(),
		orderDecisionID: req.Admission.OrderDecisionID(),
		executorDigest:  req.Admission.ExecutorDigest(),
		nonce:           req.Admission.Nonce(),
		limits:          limits,
		sandbox:         sandbox,
		plannedAt:       now,
		deadline:        deadline.UTC(),
		reasons:         req.Governor.Reasons,
		notEnforceab:    req.Governor.NotEnforceable,
	}, nil
}

// requireCapacity refuses a start when the governor allows none of a resource
// the WorkOrder actually uses. A zero ceiling means "no use", not "unknown".
func requireCapacity(decision governor.Decision, limits decisionpayload.ResourceLimits, sandbox decisionpayload.SandboxPolicy) error {
	required := []governor.Resource{governor.ResourceCPU, governor.ResourceMemory}
	if limits.TemporaryDiskBytes > 0 {
		required = append(required, governor.ResourceDiskIO)
	}
	if limits.NetworkBytesMax > 0 {
		required = append(required, governor.ResourceNetwork)
	}
	if sandbox.GPU != nil {
		required = append(required, governor.ResourceGPU)
	}
	for _, resource := range required {
		if decision.Ceilings[resource] < 1 {
			return fmt.Errorf("%w: %s", ErrNoCapacity, resource)
		}
	}
	return nil
}

// scale returns min(granted, capacity*percent/100). A required capacity that the
// machine cannot report fails closed instead of being treated as zero or
// unlimited.
func scale(capacity uint64, percent int, granted uint64) (uint64, error) {
	if capacity == 0 {
		return 0, errors.New("capacity is not reported")
	}
	if percent < 0 || percent > 100 {
		return 0, fmt.Errorf("ceiling %d is outside 0..100", percent)
	}
	allowed := capacity / 100 * uint64(percent)
	remainder := capacity % 100 * uint64(percent) / 100
	allowed += remainder
	if allowed > granted {
		return granted, nil
	}
	return allowed, nil
}
