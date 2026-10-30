package workorderaccept

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
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workorderverify"
)

const (
	testTrustDomain = "example.org"
	testWorker      = "worker-a"
	testPrincipal   = "control-plane/wor-1"
	testPolicyRev   = "policy-42"
)

var (
	testIssuedAt = time.UnixMilli(1_780_000_000_000).UTC()
	testExpires  = testIssuedAt.Add(2 * time.Minute)
	testNow      = testIssuedAt.Add(10 * time.Second)
	testKeyID    = decisiontrust.KeyID("wor-key-1")

	testTask    = ids.TaskID("tsk_00000000000000000000000000000001")
	testAttempt = ids.AttemptID("atm_00000000000000000000000000000001")
	testTaskIR  = contract.SHA256([]byte("taskir"))
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeGuard struct {
	claimed  map[string]bool
	until    []time.Time
	fences   map[string]uint64
	err      error
	claimErr error
}

func (g *fakeGuard) Claim(_ context.Context, nonce []byte, until time.Time) (bool, error) {
	if g.claimErr != nil {
		return false, g.claimErr
	}
	if g.err != nil {
		return false, g.err
	}
	if g.claimed == nil {
		g.claimed = map[string]bool{}
	}
	key := string(nonce)
	if g.claimed[key] {
		return false, nil
	}
	g.claimed[key] = true
	g.until = append(g.until, until)
	return true, nil
}

func (g *fakeGuard) Fence(_ context.Context, taskID string, epoch uint64, _ time.Time) (bool, error) {
	if g.err != nil {
		return false, g.err
	}
	if g.fences == nil {
		g.fences = map[string]uint64{}
	}
	if stored, ok := g.fences[taskID]; ok && epoch < stored {
		return false, nil
	}
	g.fences[taskID] = epoch
	return true, nil
}

type fixture struct {
	t       *testing.T
	signer  *decisiontrust.ReferenceSoftwareSigner
	codec   *cosecbor.Codec
	payload *payloadcbor.Decoder
	enc     fxcbor.EncMode
	bundle  decisiontrust.TrustBundleMetadata
	guard   *fakeGuard
	service *Service
	lease   LeaseEvidence
}

func newFixture(t *testing.T) *fixture {
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
	seed[0] = 31
	private := ed25519.NewKeyFromSeed(seed)
	record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
		ID:                 testKeyID,
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: testPrincipal,
		TrustDomain:        testTrustDomain,
		Algorithm:          decisiontrust.AlgorithmEdDSA,
		KeyType:            decisiontrust.KeyTypeOKP,
		Curve:              decisiontrust.CurveEd25519,
		PublicKey:          private.Public(),
		SigningNotBefore:   testIssuedAt.Add(-time.Hour),
		SigningNotAfter:    testIssuedAt.Add(24 * time.Hour),
		PolicyRevision:     "key-policy-1",
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
	guard := &fakeGuard{}
	service, err := New(Config{Verifier: verifier, Replay: guard, Clock: &fakeClock{now: testNow}})
	if err != nil {
		t.Fatal(err)
	}
	lease := LeaseEvidence{
		DecisionID:     "lease-decision-001",
		PayloadDigest:  contract.SHA256([]byte("lease")),
		TaskID:         testTask,
		TaskIRDigest:   testTaskIR,
		AttemptID:      testAttempt,
		WorkerIdentity: testWorker,
		FenceEpoch:     7,
		ExpiresAt:      testIssuedAt.Add(time.Minute),
	}
	return &fixture{
		t: t, signer: signer, codec: codec, payload: payload, enc: enc,
		bundle:  decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: testNow.Add(-time.Minute)},
		guard:   guard,
		service: service,
		lease:   lease,
	}
}

func (f *fixture) request(mutate func(*decisionpayload.WorkOrderConfig)) Request {
	f.t.Helper()
	ref := func(kind authority.DecisionKind, id string, digest contract.Digest) decisionpayload.DecisionRef {
		r, err := decisionpayload.NewDecisionRef(decisionpayload.DecisionRefConfig{
			Kind: kind, DecisionID: id, PayloadDigest: digest,
		})
		if err != nil {
			f.t.Fatal(err)
		}
		return r
	}
	cfg := decisionpayload.WorkOrderConfig{
		SchemaVersion:      decisionpayload.WorkOrderSchemaVersion,
		DecisionKind:       authority.DecisionWorkOrder,
		DecisionID:         "wor-decision-001",
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: testPrincipal,
		IssuedAt:           testIssuedAt,
		ExpiresAt:          testExpires,
		PolicyRevision:     testPolicyRev,
		WorkOrderID:        "work-order-001",
		TaskID:             testTask,
		TaskIRDigest:       testTaskIR,
		AttemptID:          testAttempt,
		WorkerIdentity:     testWorker,
		PlacementRef:       ref(authority.DecisionPlacementApproval, "placement-001", contract.SHA256([]byte("placement"))),
		LeaseRef:           ref(authority.DecisionLease, f.lease.DecisionID, f.lease.PayloadDigest),
		ExecutorApproval:   ref(authority.DecisionExecutorApproval, "executor-001", contract.SHA256([]byte("executor"))),
		ExecutorDigest:     contract.SHA256([]byte("executable")),
		ReadGrants:         []decisionpayload.DecisionRef{ref(authority.DecisionArtifactReadGrant, "read-001", contract.SHA256([]byte("read")))},
		WriteGrants:        []decisionpayload.DecisionRef{ref(authority.DecisionArtifactWriteGrant, "write-001", contract.SHA256([]byte("write")))},
		Limits: decisionpayload.ResourceLimits{
			CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeMillis: 600_000,
		},
		Sandbox: decisionpayload.SandboxPolicy{Profile: "sandbox/container/v1"},
		Nonce:   bytes.Repeat([]byte{0x5a}, 16),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	order, err := decisionpayload.NewWorkOrder(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	payloadBytes, err := f.payload.EncodeWorkOrder(order)
	if err != nil {
		f.t.Fatal(err)
	}
	req := Request{
		Raw:         f.sign(payloadBytes),
		TrustBundle: f.bundle,
		Lease:       f.lease,
		Placement: PlacementEvidence{
			DecisionID: "placement-001", PayloadDigest: contract.SHA256([]byte("placement")),
			TaskID: testTask, TaskIRDigest: testTaskIR, AttemptID: testAttempt, WorkerIdentity: testWorker,
		},
		Executor: ExecutorEvidence{
			DecisionID: "executor-001", PayloadDigest: contract.SHA256([]byte("executor")),
			ExecutorDigest: contract.SHA256([]byte("executable")),
		},
	}
	return req
}

func (f *fixture) sign(payload []byte) []byte {
	f.t.Helper()
	protected, err := f.enc.Marshal(map[int64]any{
		1:      int64(-8),
		2:      []int64{decisiontrust.COSEHeaderLabelEKIProfile},
		4:      []byte(testKeyID),
		-70000: decisiontrust.SignedDecisionProfileV1,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	sigStructure, err := f.codec.EncodeSignature1(protected, payload)
	if err != nil {
		f.t.Fatal(err)
	}
	signed, err := f.signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key: decisiontrust.SigningRequest{
			TrustDomain:        testTrustDomain,
			AuthorityRole:      authority.RoleWorkOrder,
			AuthorityPrincipal: testPrincipal,
			DecisionKind:       authority.DecisionWorkOrder,
			Algorithm:          decisiontrust.AlgorithmEdDSA,
		},
		PolicyRevision: "key-policy-1",
		SigStructure:   sigStructure,
	}, testIssuedAt)
	if err != nil {
		f.t.Fatal(err)
	}
	protectedBytes, err := f.enc.Marshal(protected)
	if err != nil {
		f.t.Fatal(err)
	}
	payloadBytes, err := f.enc.Marshal(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	sig, err := f.enc.Marshal(signed.Signature)
	if err != nil {
		f.t.Fatal(err)
	}
	out := []byte{0xd2, 0x84}
	out = append(out, protectedBytes...)
	out = append(out, 0xa0)
	out = append(out, payloadBytes...)
	return append(out, sig...)
}

func TestAcceptBindsTheOrderToLocalEvidence(t *testing.T) {
	fx := newFixture(t)
	admission, err := fx.service.Accept(context.Background(), fx.request(nil))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if admission.IsZero() {
		t.Fatal("Accept() returned a zero Admission")
	}
	if admission.TaskID() != testTask || admission.AttemptID() != testAttempt || admission.WorkerIdentity() != testWorker {
		t.Fatalf("admission subject = %s/%s/%s", admission.TaskID(), admission.AttemptID(), admission.WorkerIdentity())
	}
	if admission.FenceEpoch() != fx.lease.FenceEpoch {
		t.Fatalf("fence = %d, want %d", admission.FenceEpoch(), fx.lease.FenceEpoch)
	}
	if admission.LeaseDecisionID() != fx.lease.DecisionID {
		t.Fatalf("lease decision = %q, want %q", admission.LeaseDecisionID(), fx.lease.DecisionID)
	}
	if !admission.ExpiresAt().Equal(fx.lease.ExpiresAt) {
		t.Fatalf("expiry = %s, want the earlier Lease expiry %s", admission.ExpiresAt(), fx.lease.ExpiresAt)
	}
	if !admission.AdmittedAt().Equal(testNow) {
		t.Fatalf("admitted at = %s, want %s", admission.AdmittedAt(), testNow)
	}
	if len(fx.guard.until) != 1 || !fx.guard.until[0].Equal(fx.lease.ExpiresAt) {
		t.Fatalf("nonce claim until = %v, want the effective expiry", fx.guard.until)
	}
	if admission.KeyID() != testKeyID || admission.TrustBundleRevision() != fx.bundle.Revision {
		t.Fatalf("key/bundle = %q/%d", admission.KeyID(), admission.TrustBundleRevision())
	}
}

func TestAcceptRefusesMismatchedEvidenceWithoutBurningTheNonce(t *testing.T) {
	fx := newFixture(t)

	t.Run("lease", func(t *testing.T) {
		req := fx.request(nil)
		req.Lease.AttemptID = ids.AttemptID("atm_00000000000000000000000000000002")
		if _, err := fx.service.Accept(context.Background(), req); !errors.Is(err, ErrLeaseMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrLeaseMismatch)
		}
	})
	t.Run("expired lease", func(t *testing.T) {
		req := fx.request(nil)
		req.Lease.ExpiresAt = testNow
		if _, err := fx.service.Accept(context.Background(), req); !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("error = %v, want %v", err, ErrLeaseExpired)
		}
	})
	t.Run("placement", func(t *testing.T) {
		req := fx.request(nil)
		req.Placement.PayloadDigest = contract.SHA256([]byte("other-placement"))
		if _, err := fx.service.Accept(context.Background(), req); !errors.Is(err, ErrPlacementMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrPlacementMismatch)
		}
	})
	t.Run("executor", func(t *testing.T) {
		req := fx.request(nil)
		req.Executor.ExecutorDigest = contract.SHA256([]byte("other-executable"))
		if _, err := fx.service.Accept(context.Background(), req); !errors.Is(err, ErrExecutorMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrExecutorMismatch)
		}
	})
	if len(fx.guard.claimed) != 0 {
		t.Fatalf("nonce was claimed despite failures: %v", fx.guard.claimed)
	}
}

func TestAcceptRejectsReplayedNonce(t *testing.T) {
	fx := newFixture(t)
	req := fx.request(nil)
	if _, err := fx.service.Accept(context.Background(), req); err != nil {
		t.Fatalf("first Accept() error = %v", err)
	}
	if _, err := fx.service.Accept(context.Background(), req); !errors.Is(err, ErrReplay) {
		t.Fatalf("second Accept() error = %v, want %v", err, ErrReplay)
	}
}

func TestAcceptReportsReplayGuardFailure(t *testing.T) {
	fx := newFixture(t)
	fx.guard.err = errors.New("store unavailable")
	// The fence claim is the first durable touch, so its failure surfaces
	// first; either unknown outcome is a refusal.
	if _, err := fx.service.Accept(context.Background(), fx.request(nil)); !errors.Is(err, ErrFenceUnknown) {
		t.Fatalf("error = %v, want %v", err, ErrFenceUnknown)
	}
	// A guard that answers the fence but fails the nonce claim is unknown too.
	fx.guard.fences = map[string]uint64{fx.lease.TaskID.String(): fx.lease.FenceEpoch}
	fx.guard.err = nil
	fx.guard.claimErr = errors.New("store unavailable")
	if _, err := fx.service.Accept(context.Background(), fx.request(nil)); !errors.Is(err, ErrReplayUnknown) {
		t.Fatalf("error = %v, want %v", err, ErrReplayUnknown)
	}
}

func TestAcceptPropagatesVerificationFailure(t *testing.T) {
	fx := newFixture(t)
	req := fx.request(nil)
	req.Raw[len(req.Raw)-1] ^= 0xff
	if _, err := fx.service.Accept(context.Background(), req); err == nil {
		t.Fatal("Accept() accepted a tampered WorkOrder")
	}
	if len(fx.guard.claimed) != 0 {
		t.Fatal("nonce was claimed for a tampered WorkOrder")
	}
}

func TestAcceptRejectsIncompleteEvidence(t *testing.T) {
	fx := newFixture(t)
	cases := map[string]func(*Request){
		"lease decision":     func(r *Request) { r.Lease.DecisionID = "" },
		"lease digest":       func(r *Request) { r.Lease.PayloadDigest = contract.Digest{} },
		"placement decision": func(r *Request) { r.Placement.DecisionID = "" },
		"executor decision":  func(r *Request) { r.Executor.DecisionID = "" },
		"executor digest":    func(r *Request) { r.Executor.ExecutorDigest = contract.Digest{} },
	}
	for name, mutate := range cases {
		req := fx.request(nil)
		mutate(&req)
		if _, err := fx.service.Accept(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s: error = %v, want %v", name, err, ErrInvalidRequest)
		}
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	fx := newFixture(t)
	base := Config{Verifier: fx.service.verifier, Replay: fx.guard, Clock: &fakeClock{now: testNow}}
	cases := map[string]func(*Config){
		"verifier": func(c *Config) { c.Verifier = nil },
		"replay":   func(c *Config) { c.Replay = nil },
		"clock":    func(c *Config) { c.Clock = nil },
	}
	for name, mutate := range cases {
		cfg := base
		mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: New() error = %v, want %v", name, err, ErrInvalidConfig)
		}
	}
}
