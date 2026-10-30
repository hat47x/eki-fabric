package startgate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workorderaccept"
	"github.com/hat47x/eki-fabric/internal/core/workorderverify"
)

const (
	testTrustDomain = "example.org"
	testWorker      = "worker-a"
	testPrincipal   = "control-plane/wor-1"
)

var (
	testIssuedAt = time.UnixMilli(1_780_000_000_000).UTC()
	testExpires  = testIssuedAt.Add(2 * time.Minute)
	testNow      = testIssuedAt.Add(10 * time.Second)
	testKeyID    = decisiontrust.KeyID("wor-key-1")
	testTask     = ids.TaskID("tsk_00000000000000000000000000000001")
	testAttempt  = ids.AttemptID("atm_00000000000000000000000000000001")
	testTaskIR   = contract.SHA256([]byte("taskir"))
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

func admission(t *testing.T, now time.Time, mutate func(*decisionpayload.WorkOrderConfig)) workorderaccept.Admission {
	t.Helper()
	codec, err := cosecbor.New()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := payloadcbor.New()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := fxcbor.CoreDetEncOptions().EncMode()
	if err != nil {
		t.Fatal(err)
	}
	registry := decisiontrust.NewRegistry(testIssuedAt.Add(-time.Hour))
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 41
	private := ed25519.NewKeyFromSeed(seed)
	record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
		ID: testKeyID, AuthorityRole: authority.RoleWorkOrder, AuthorityPrincipal: testPrincipal,
		TrustDomain: testTrustDomain, Algorithm: decisiontrust.AlgorithmEdDSA,
		KeyType: decisiontrust.KeyTypeOKP, Curve: decisiontrust.CurveEd25519, PublicKey: private.Public(),
		SigningNotBefore: testIssuedAt.Add(-time.Hour), SigningNotAfter: testIssuedAt.Add(24 * time.Hour),
		PolicyRevision: "key-policy-1",
	}, testIssuedAt.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(record, testIssuedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Activate(record.ID(), testIssuedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPrimary(record.ID(), testIssuedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	signer, err := decisiontrust.NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.RegisterPrivateKey(record.ID(), private); err != nil {
		t.Fatal(err)
	}
	policy, err := decisiontrust.NewVerificationPolicy(decisiontrust.VerificationPolicyConfig{
		MaxClockSkew:            5 * time.Second,
		MaxTrustBundleStaleness: time.Hour,
		AllowedAlgorithms:       []decisiontrust.Algorithm{decisiontrust.AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionWorkOrder: 5 * time.Minute,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := decisiontrust.NewVerifierBoundary(registry, policy)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workorderverify.New(workorderverify.Config{
		Codec: codec, Payload: payload, Boundary: boundary,
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
	lease := workorderaccept.LeaseEvidence{
		DecisionID: "lease-decision-001", PayloadDigest: contract.SHA256([]byte("lease")),
		TaskID: testTask, TaskIRDigest: testTaskIR, AttemptID: testAttempt,
		WorkerIdentity: testWorker, FenceEpoch: 7,
		ExpiresAt: testIssuedAt.Add(time.Minute),
	}
	ref := func(kind authority.DecisionKind, id string, digest contract.Digest) decisionpayload.DecisionRef {
		r, err := decisionpayload.NewDecisionRef(decisionpayload.DecisionRefConfig{Kind: kind, DecisionID: id, PayloadDigest: digest})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cfg := decisionpayload.WorkOrderConfig{
		SchemaVersion: decisionpayload.WorkOrderSchemaVersion, DecisionKind: authority.DecisionWorkOrder,
		DecisionID: "wor-decision-001", AuthorityRole: authority.RoleWorkOrder, AuthorityPrincipal: testPrincipal,
		IssuedAt: testIssuedAt, ExpiresAt: testExpires, PolicyRevision: "policy-42",
		WorkOrderID: "work-order-001", TaskID: testTask, TaskIRDigest: testTaskIR, AttemptID: testAttempt,
		WorkerIdentity:   testWorker,
		PlacementRef:     ref(authority.DecisionPlacementApproval, "placement-001", contract.SHA256([]byte("placement"))),
		LeaseRef:         ref(authority.DecisionLease, lease.DecisionID, lease.PayloadDigest),
		ExecutorApproval: ref(authority.DecisionExecutorApproval, "executor-001", contract.SHA256([]byte("executor"))),
		ExecutorDigest:   contract.SHA256([]byte("executable")),
		ReadGrants:       []decisionpayload.DecisionRef{ref(authority.DecisionArtifactReadGrant, "read-001", contract.SHA256([]byte("read")))},
		WriteGrants:      []decisionpayload.DecisionRef{ref(authority.DecisionArtifactWriteGrant, "write-001", contract.SHA256([]byte("write")))},
		Limits: decisionpayload.ResourceLimits{
			CPUMillis: 6000, MemoryBytes: 6 << 30, TemporaryDiskBytes: 1 << 30, WallTimeMillis: 600_000,
		},
		Sandbox: decisionpayload.SandboxPolicy{Profile: "sandbox/container/v1"},
		Nonce:   bytes.Repeat([]byte{0x5a}, 16),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	order, err := decisionpayload.NewWorkOrder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	payloadBytes, err := payload.EncodeWorkOrder(order)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := enc.Marshal(map[int64]any{
		1: int64(-8), 2: []int64{decisiontrust.COSEHeaderLabelEKIProfile}, 4: []byte(testKeyID),
		-70000: decisiontrust.SignedDecisionProfileV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	sigStructure, err := codec.EncodeSignature1(protected, payloadBytes)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key: decisiontrust.SigningRequest{
			TrustDomain: testTrustDomain, AuthorityRole: authority.RoleWorkOrder,
			AuthorityPrincipal: testPrincipal, DecisionKind: authority.DecisionWorkOrder,
			Algorithm: decisiontrust.AlgorithmEdDSA,
		},
		PolicyRevision: "key-policy-1", SigStructure: sigStructure,
	}, testIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	protectedBytes, err := enc.Marshal(protected)
	if err != nil {
		t.Fatal(err)
	}
	payloadWire, err := enc.Marshal(payloadBytes)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := enc.Marshal(signed.Signature)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte{0xd2, 0x84}
	raw = append(raw, protectedBytes...)
	raw = append(raw, 0xa0)
	raw = append(raw, payloadWire...)
	raw = append(raw, sig...)

	admitted, err := accept.Accept(context.Background(), workorderaccept.Request{
		Raw: raw, TrustBundle: decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: now.Add(-time.Minute)},
		Lease: lease,
		Placement: workorderaccept.PlacementEvidence{
			DecisionID: "placement-001", PayloadDigest: contract.SHA256([]byte("placement")),
			TaskID: testTask, TaskIRDigest: testTaskIR, AttemptID: testAttempt, WorkerIdentity: testWorker,
		},
		Executor: workorderaccept.ExecutorEvidence{
			DecisionID: "executor-001", PayloadDigest: contract.SHA256([]byte("executor")),
			ExecutorDigest: contract.SHA256([]byte("executable")),
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
