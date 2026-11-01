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
	ErrNoCapacity       = errors.New("no capacity is left for a required resource")
	ErrNoSlot           = errors.New("every execution slot is in use")
	ErrMachineUnknown   = errors.New("the machine profile is missing a required capacity")
	ErrAdmissionExpired = errors.New("the Admission has expired")
)

type Clock interface{ Now() time.Time }

// Usage is what the Attempts already running on this endpoint occupy. The gate
// judges the next start against what is left, so two Attempts cannot each take
// the whole machine (ADR-0031 §2.2).
//
// Temporary disk and network are not sized here: this version of the gate only
// asks whether the Governor allows them at all.
type Usage struct {
	// Running is how many Attempts are executing on this endpoint.
	Running uint32
	// CPUMillis, MemoryBytes, and GPUDevices are the limits the running Plans
	// reserved, summed. A Plan's limit is the ceiling the Attempt may use, not
	// the amount it has used so far (ADR-0031 §2.3).
	CPUMillis   uint64
	MemoryBytes uint64
	GPUDevices  uint32
}

// MachineProfile is the local, measurable capacity of this endpoint. It is
// detected by the agent, not received from the Control Plane.
type MachineProfile struct {
	CPUMillis   uint64
	MemoryBytes uint64
	GPUDevices  uint32
}

type Config struct {
	Clock Clock
	// MaxConcurrent is how many Attempts may run at once. Zero means 1: the
	// reference Agent runs one Attempt at a time, so running two is a choice an
	// operator makes, not a default (ADR-0031 §2.1).
	MaxConcurrent int
}

type Gate struct {
	clock         Clock
	maxConcurrent uint32
}

func New(cfg Config) (*Gate, error) {
	if cfg.Clock == nil {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}
	if cfg.MaxConcurrent < 0 {
		return nil, fmt.Errorf("%w: MaxConcurrent must not be negative", ErrInvalidConfig)
	}
	maxConcurrent := uint32(cfg.MaxConcurrent)
	if maxConcurrent == 0 {
		maxConcurrent = 1
	}
	return &Gate{clock: cfg.Clock, maxConcurrent: maxConcurrent}, nil
}

// MaxConcurrent is how many Attempts this gate lets run at once.
func (g *Gate) MaxConcurrent() uint32 { return g.maxConcurrent }

type Request struct {
	Admission workorderaccept.Admission
	Governor  governor.Decision
	Machine   MachineProfile
	// InUse is what the Attempts already running here occupy. A caller that
	// runs one Attempt at a time passes the zero value.
	InUse Usage
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
	// The slot limit is checked before any resource arithmetic: a full endpoint
	// refuses the start whatever the numbers say (ADR-0031 §2.2).
	if req.InUse.Running >= g.maxConcurrent {
		return Plan{}, ErrNoSlot
	}

	limits := order
	cpu, err := free(req.Machine.CPUMillis, req.Governor.Ceilings[governor.ResourceCPU], req.InUse.CPUMillis, order.CPUMillis, "cpu")
	if err != nil {
		return Plan{}, err
	}
	limits.CPUMillis = cpu
	memory, err := free(req.Machine.MemoryBytes, req.Governor.Ceilings[governor.ResourceMemory], req.InUse.MemoryBytes, order.MemoryBytes, "memory")
	if err != nil {
		return Plan{}, err
	}
	limits.MemoryBytes = memory

	sandbox := req.Admission.Sandbox()
	if sandbox.GPU != nil {
		allowed := uint32(uint64(req.Machine.GPUDevices) * uint64(req.Governor.Ceilings[governor.ResourceGPU]) / 100)
		// The running Attempts already hold some of the allowed devices; the
		// new one may take only what is left (ADR-0031 §2.2).
		if req.InUse.GPUDevices >= allowed {
			return Plan{}, fmt.Errorf("%w: gpu", ErrNoCapacity)
		}
		available := allowed - req.InUse.GPUDevices
		if available < sandbox.GPU.DeviceCount {
			sandbox.GPU = &decisionpayload.GPULimit{
				DeviceCount:      available,
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

// free returns min(granted, capacity*percent/100 - inUse). The Governor's
// ceiling applies to everything running on the endpoint together, so what a new
// Attempt may take is what is left after the running reservations
// (ADR-0031 §2.2). A required capacity that the machine cannot report fails
// closed instead of being treated as zero or unlimited.
func free(capacity uint64, percent int, inUse, granted uint64, resource string) (uint64, error) {
	if capacity == 0 {
		return 0, fmt.Errorf("%w: capacity is not reported", ErrMachineUnknown)
	}
	if percent < 0 || percent > 100 {
		return 0, fmt.Errorf("%w: ceiling %d is outside 0..100", ErrMachineUnknown, percent)
	}
	total := capacity/100*uint64(percent) + capacity%100*uint64(percent)/100
	if inUse >= total {
		return 0, fmt.Errorf("%w: %s", ErrNoCapacity, resource)
	}
	available := total - inUse
	if available > granted {
		return granted, nil
	}
	return available, nil
}
