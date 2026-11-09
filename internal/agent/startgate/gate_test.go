package startgate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/workorderaccept"
	"github.com/hat47x/eki-fabric/internal/core/workorderverify"
	wo "github.com/hat47x/eki-fabric/internal/testfixtures/workorder"
)

const (
	testTrustDomain = wo.TrustDomain
	testWorker      = wo.Worker
	testPrincipal   = wo.Principal
)

var (
	testIssuedAt = wo.IssuedAt
	testExpires  = wo.Expires
	testNow      = wo.Now
	testKeyID    = wo.KeyID
	testTask     = wo.Task
	testAttempt  = wo.Attempt
	testTaskIR   = wo.TaskIR
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeGuard struct{ used map[string]bool }

func (g *fakeGuard) Claim(_ context.Context, nonce []byte, _ time.Time) (bool, error) {
	if g.used == nil {
		g.used = map[string]bool{}
	}
	key := string(nonce)
	if g.used[key] {
		return false, nil
	}
	g.used[key] = true
	return true, nil
}

func (g *fakeGuard) Fence(context.Context, string, uint64, time.Time) (bool, error) {
	return true, nil
}

// admission accepts a signed WorkOrder through the real verifier and returns the
// Admission it yields. The default WorkOrder's limits are raised so that the
// Governor ceiling, not the grant, is what the tests scale.
func admission(t *testing.T, now time.Time, mutate func(*decisionpayload.WorkOrderConfig)) workorderaccept.Admission {
	t.Helper()
	signing := wo.NewSigning(t, 41)
	verifier, err := workorderverify.New(workorderverify.Config{
		Codec: signing.Codec, Payload: signing.Payload, Boundary: signing.Boundary,
		TrustDomain: testTrustDomain, WorkerIdentity: testWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	accept, err := workorderaccept.New(workorderaccept.Config{
		Verifier: verifier, Replay: &fakeGuard{}, Clock: &fakeClock{now: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := signing.SignedWorkOrder(func(cfg *decisionpayload.WorkOrderConfig) {
		cfg.Limits = decisionpayload.ResourceLimits{
			CPUMillis: 6000, MemoryBytes: 6 << 30, TemporaryDiskBytes: 1 << 30, WallTimeMillis: 600_000,
		}
		if mutate != nil {
			mutate(cfg)
		}
	})
	admitted, err := accept.Accept(context.Background(), workorderaccept.Request{
		Raw: raw, TrustBundle: decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: now.Add(-time.Minute)},
		Lease: workorderaccept.LeaseEvidence{
			DecisionID: wo.LeaseDecisionID, PayloadDigest: wo.LeaseDigest,
			TaskID: testTask, TaskIRDigest: testTaskIR, AttemptID: testAttempt,
			WorkerIdentity: testWorker, FenceEpoch: 7,
			ExpiresAt: testIssuedAt.Add(time.Minute),
		},
		Placement: workorderaccept.PlacementEvidence{
			DecisionID: "placement-001", PayloadDigest: wo.PlacementDigest,
			TaskID: testTask, TaskIRDigest: testTaskIR, AttemptID: testAttempt, WorkerIdentity: testWorker,
		},
		Executor: workorderaccept.ExecutorEvidence{
			DecisionID: "executor-001", PayloadDigest: wo.ExecutorDigest,
			ExecutorDigest: wo.ExecutableDigest,
		},
	})
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	return admitted
}

func governorDecision(ceilings map[governor.Resource]int) governor.Decision {
	return governor.Decision{AcceptNewLeases: true, Ceilings: ceilings}
}

func healthyCeilings() map[governor.Resource]int {
	return map[governor.Resource]int{
		governor.ResourceCPU: 50, governor.ResourceMemory: 50,
		governor.ResourceDiskIO: 50, governor.ResourceNetwork: 50,
	}
}

func newGate(t *testing.T, now time.Time) *Gate {
	t.Helper()
	gate, err := New(Config{Clock: &fakeClock{now: now}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return gate
}

// newGateWith builds a gate that lets maxConcurrent Attempts run at once.
func newGateWith(t *testing.T, now time.Time, maxConcurrent int) *Gate {
	t.Helper()
	gate, err := New(Config{Clock: &fakeClock{now: now}, MaxConcurrent: maxConcurrent})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return gate
}

func TestCheckScalesTheGrantByTheGovernorCeiling(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate := newGate(t, testNow)
	plan, err := gate.Check(Request{
		Admission: admitted,
		Governor:  governorDecision(healthyCeilings()),
		Machine:   MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30, GPUDevices: 0},
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if plan.IsZero() {
		t.Fatal("Check() returned a zero Plan")
	}
	if got, want := plan.Limits().CPUMillis, uint64(4000); got != want {
		t.Fatalf("CPU = %d, want %d (8000 * 50%%, under the 6000 grant)", got, want)
	}
	if got, want := plan.Limits().MemoryBytes, uint64(4<<30); got != want {
		t.Fatalf("memory = %d, want %d", got, want)
	}
	if !plan.Deadline().Equal(admitted.ExpiresAt()) {
		t.Fatalf("deadline = %s, want the Admission expiry %s", plan.Deadline(), admitted.ExpiresAt())
	}
	if plan.TaskID() != testTask || plan.AttemptID() != testAttempt {
		t.Fatalf("plan subject = %s/%s", plan.TaskID(), plan.AttemptID())
	}
}

func TestCheckKeepsTheGrantWhenTheMachineIsLarger(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate := newGate(t, testNow)
	ceilings := healthyCeilings()
	ceilings[governor.ResourceCPU] = 100
	ceilings[governor.ResourceMemory] = 100
	plan, err := gate.Check(Request{
		Admission: admitted,
		Governor:  governorDecision(ceilings),
		Machine:   MachineProfile{CPUMillis: 64000, MemoryBytes: 64 << 30},
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if plan.Limits().CPUMillis != 6000 || plan.Limits().MemoryBytes != 6<<30 {
		t.Fatalf("limits = %+v, want the WorkOrder grant", plan.Limits())
	}
}

func TestCheckRefusesWhenTheGovernorStopsOrRefuses(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate := newGate(t, testNow)
	machine := MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30}

	stopped := governorDecision(healthyCeilings())
	stopped.StopRunning = true
	if _, err := gate.Check(Request{Admission: admitted, Governor: stopped, Machine: machine}); !errors.Is(err, ErrGovernorStopped) {
		t.Fatalf("stopped error = %v, want %v", err, ErrGovernorStopped)
	}

	refusing := governorDecision(healthyCeilings())
	refusing.AcceptNewLeases = false
	if _, err := gate.Check(Request{Admission: admitted, Governor: refusing, Machine: machine}); !errors.Is(err, ErrNotAccepting) {
		t.Fatalf("not accepting error = %v, want %v", err, ErrNotAccepting)
	}
}

func TestCheckRefusesZeroCapacityForARequiredResource(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate := newGate(t, testNow)
	machine := MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30}

	for _, resource := range []governor.Resource{governor.ResourceCPU, governor.ResourceMemory, governor.ResourceDiskIO} {
		ceilings := healthyCeilings()
		ceilings[resource] = 0
		_, err := gate.Check(Request{Admission: admitted, Governor: governorDecision(ceilings), Machine: machine})
		if !errors.Is(err, ErrNoCapacity) {
			t.Fatalf("%s: error = %v, want %v", resource, err, ErrNoCapacity)
		}
	}
}

func TestCheckRefusesUnknownMachineCapacity(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate := newGate(t, testNow)
	if _, err := gate.Check(Request{
		Admission: admitted, Governor: governorDecision(healthyCeilings()),
		Machine: MachineProfile{MemoryBytes: 8 << 30},
	}); !errors.Is(err, ErrMachineUnknown) {
		t.Fatalf("missing CPU error = %v, want %v", err, ErrMachineUnknown)
	}
}

func TestCheckRefusesExpiredOrZeroAdmission(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate := newGate(t, testNow)
	machine := MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30}

	if _, err := gate.Check(Request{Governor: governorDecision(healthyCeilings()), Machine: machine}); !errors.Is(err, ErrNotAdmitted) {
		t.Fatalf("zero admission error = %v, want %v", err, ErrNotAdmitted)
	}
	later := newGate(t, admitted.ExpiresAt())
	if _, err := later.Check(Request{Admission: admitted, Governor: governorDecision(healthyCeilings()), Machine: machine}); !errors.Is(err, ErrAdmissionExpired) {
		t.Fatalf("expired admission error = %v, want %v", err, ErrAdmissionExpired)
	}
}

func TestCheckCapsGPUDevicesByTheGovernorCeiling(t *testing.T) {
	admitted := admission(t, testNow, func(cfg *decisionpayload.WorkOrderConfig) {
		cfg.Sandbox.GPU = &decisionpayload.GPULimit{DeviceCount: 2, MinimumVRAMBytes: 16 << 30}
	})
	gate := newGate(t, testNow)
	ceilings := healthyCeilings()
	ceilings[governor.ResourceGPU] = 50
	plan, err := gate.Check(Request{
		Admission: admitted, Governor: governorDecision(ceilings),
		Machine: MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30, GPUDevices: 2},
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if plan.Sandbox().GPU == nil || plan.Sandbox().GPU.DeviceCount != 1 {
		t.Fatalf("GPU = %+v, want 1 device", plan.Sandbox().GPU)
	}
}

func TestCheckCapsTheDeadlineByTheWallTimeGrant(t *testing.T) {
	now := testIssuedAt.Add(2 * time.Second)
	admitted := admission(t, now, func(cfg *decisionpayload.WorkOrderConfig) {
		cfg.Limits.WallTimeMillis = 5000
	})
	gate := newGate(t, now)
	plan, err := gate.Check(Request{
		Admission: admitted, Governor: governorDecision(healthyCeilings()),
		Machine: MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30},
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got, want := plan.Deadline(), now.Add(5*time.Second); !got.Equal(want) {
		t.Fatalf("deadline = %s, want %s", got, want)
	}
}

func TestNewRejectsMissingClock(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestNewRejectsNegativeMaxConcurrent(t *testing.T) {
	if _, err := New(Config{Clock: &fakeClock{now: testNow}, MaxConcurrent: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want %v", err, ErrInvalidConfig)
	}
}

// The Governor's ceiling covers everything running on the endpoint together; a
// new Attempt may take only what the running reservations leave (ADR-0031 §2.2).
func TestCheckScalesTheGrantByWhatTheRunningAttemptsLeave(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate := newGateWith(t, testNow, 2)
	// 8000 millis at 50% leaves 4000 for everything; 3000 is already reserved.
	plan, err := gate.Check(Request{
		Admission: admitted,
		Governor:  governorDecision(healthyCeilings()),
		Machine:   MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30},
		InUse:     Usage{Running: 1, CPUMillis: 3000, MemoryBytes: 3 << 30},
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got, want := plan.Limits().CPUMillis, uint64(1000); got != want {
		t.Fatalf("CPU = %d, want %d (4000 ceiling - 3000 reserved)", got, want)
	}
	if got, want := plan.Limits().MemoryBytes, uint64(1<<30); got != want {
		t.Fatalf("memory = %d, want %d", got, want)
	}
}

// The same bytes that start on an idle endpoint are refused once the running
// Attempts have taken the whole ceiling; the nonce is not spent because the
// gate runs before the claim.
func TestCheckRefusesAStartWhenTheRunningAttemptsFillTheCeiling(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate := newGateWith(t, testNow, 2)
	_, err := gate.Check(Request{
		Admission: admitted,
		Governor:  governorDecision(healthyCeilings()),
		Machine:   MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30},
		InUse:     Usage{Running: 1, CPUMillis: 5000, MemoryBytes: 5 << 30},
	})
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("error = %v, want %v", err, ErrNoCapacity)
	}
}

func TestCheckRefusesAStartWhenEverySlotIsInUse(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate := newGate(t, testNow)
	if got, want := gate.MaxConcurrent(), uint32(1); got != want {
		t.Fatalf("MaxConcurrent() = %d, want the reference default %d", got, want)
	}
	_, err := gate.Check(Request{
		Admission: admitted,
		Governor:  governorDecision(healthyCeilings()),
		Machine:   MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30},
		InUse:     Usage{Running: 1},
	})
	if !errors.Is(err, ErrNoSlot) {
		t.Fatalf("error = %v, want %v", err, ErrNoSlot)
	}
}

func TestCheckAllowsTheConcurrencyTheConfigPermits(t *testing.T) {
	admitted := admission(t, testNow, nil)
	gate, err := New(Config{Clock: &fakeClock{now: testNow}, MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	plan, err := gate.Check(Request{
		Admission: admitted,
		Governor:  governorDecision(healthyCeilings()),
		Machine:   MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30},
		InUse:     Usage{Running: 1, CPUMillis: 1000, MemoryBytes: 1 << 30},
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got, want := plan.Limits().CPUMillis, uint64(3000); got != want {
		t.Fatalf("CPU = %d, want %d (4000 ceiling - 1000 reserved)", got, want)
	}
}

func TestCheckCapsGPUDevicesByWhatTheRunningAttemptsLeave(t *testing.T) {
	admitted := admission(t, testNow, func(cfg *decisionpayload.WorkOrderConfig) {
		cfg.Sandbox.GPU = &decisionpayload.GPULimit{DeviceCount: 2, MinimumVRAMBytes: 16 << 30}
	})
	gate := newGateWith(t, testNow, 2)
	ceilings := healthyCeilings()
	ceilings[governor.ResourceGPU] = 100
	machine := MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30, GPUDevices: 2}

	plan, err := gate.Check(Request{Admission: admitted, Governor: governorDecision(ceilings), Machine: machine,
		InUse: Usage{Running: 1, GPUDevices: 1}})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if plan.Sandbox().GPU == nil || plan.Sandbox().GPU.DeviceCount != 1 {
		t.Fatalf("GPU = %+v, want 1 device (2 allowed - 1 in use)", plan.Sandbox().GPU)
	}

	if _, err := gate.Check(Request{Admission: admitted, Governor: governorDecision(ceilings), Machine: machine,
		InUse: Usage{Running: 1, GPUDevices: 2}}); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("error = %v, want %v when every GPU is reserved", err, ErrNoCapacity)
	}
}
