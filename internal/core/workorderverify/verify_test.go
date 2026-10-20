package workorderverify

import (
	"bytes"
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
)

type fixture struct {
	t        *testing.T
	registry *decisiontrust.Registry
	signer   *decisiontrust.ReferenceSoftwareSigner
	codec    *cosecbor.Codec
	payload  *payloadcbor.Decoder
	enc      fxcbor.EncMode
	bundle   decisiontrust.TrustBundleMetadata
	verifier *Verifier
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
	seed[0] = 23
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
	verifier, err := New(Config{
		Codec: codec, Payload: payload, Boundary: boundary,
		TrustDomain: testTrustDomain, WorkerIdentity: testWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{
		t: t, registry: registry, signer: signer, codec: codec, payload: payload, enc: enc,
		bundle:   decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: testNow.Add(-time.Minute)},
		verifier: verifier,
	}
}

func (f *fixture) order(mutate func(*decisionpayload.WorkOrderConfig)) decisionpayload.WorkOrder {
	f.t.Helper()
	ref := func(kind authority.DecisionKind, id, seed string) decisionpayload.DecisionRef {
		r, err := decisionpayload.NewDecisionRef(decisionpayload.DecisionRefConfig{
			Kind: kind, DecisionID: id, PayloadDigest: contract.SHA256([]byte(seed)),
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
		TaskID:             ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest:       contract.SHA256([]byte("taskir")),
		AttemptID:          ids.AttemptID("atm_00000000000000000000000000000001"),
		WorkerIdentity:     testWorker,
		PlacementRef:       ref(authority.DecisionPlacementApproval, "placement-001", "placement"),
		LeaseRef:           ref(authority.DecisionLease, "lease-001", "lease"),
		ExecutorApproval:   ref(authority.DecisionExecutorApproval, "executor-001", "executor"),
		ExecutorDigest:     contract.SHA256([]byte("executable")),
		ReadGrants:         []decisionpayload.DecisionRef{ref(authority.DecisionArtifactReadGrant, "read-001", "read")},
		WriteGrants:        []decisionpayload.DecisionRef{ref(authority.DecisionArtifactWriteGrant, "write-001", "write")},
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
	return order
}

func (f *fixture) signPayload(payload []byte) []byte {
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
	out := []byte{0xd2, 0x84}
	protectedBytes, err := f.enc.Marshal(protected)
	if err != nil {
		f.t.Fatal(err)
	}
	out = append(out, protectedBytes...)
	out = append(out, 0xa0)
	payloadBytes, err := f.enc.Marshal(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	out = append(out, payloadBytes...)
	sig, err := f.enc.Marshal(signed.Signature)
	if err != nil {
		f.t.Fatal(err)
	}
	return append(out, sig...)
}

func (f *fixture) signedWorkOrder(mutate func(*decisionpayload.WorkOrderConfig)) []byte {
	f.t.Helper()
	payload, err := f.payload.EncodeWorkOrder(f.order(mutate))
	if err != nil {
		f.t.Fatal(err)
	}
	return f.signPayload(payload)
}

func (f *fixture) verify(raw []byte, now time.Time) (Verified, error) {
	return f.verifier.Verify(Request{Raw: raw, Now: now, TrustBundle: f.bundle})
}

func TestVerifyAcceptsAWorkerBoundSignedWorkOrder(t *testing.T) {
	fx := newFixture(t)
	verified, err := fx.verify(fx.signedWorkOrder(nil), testNow)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified.IsZero() {
		t.Fatal("verified WorkOrder is zero")
	}
	if verified.Order().WorkerIdentity() != testWorker {
		t.Fatalf("worker = %q, want %q", verified.Order().WorkerIdentity(), testWorker)
	}
	if verified.KeyID() != testKeyID || verified.TrustDomain() != testTrustDomain {
		t.Fatalf("key/domain = %q/%q", verified.KeyID(), verified.TrustDomain())
	}
	if !verified.VerifiedAt().Equal(testNow) {
		t.Fatalf("VerifiedAt = %s, want %s", verified.VerifiedAt(), testNow)
	}
	if verified.TrustBundleRevision() != fx.bundle.Revision {
		t.Fatalf("bundle revision = %d, want %d", verified.TrustBundleRevision(), fx.bundle.Revision)
	}
}

func TestVerifyRejectsAnotherWorker(t *testing.T) {
	fx := newFixture(t)
	raw := fx.signedWorkOrder(func(cfg *decisionpayload.WorkOrderConfig) { cfg.WorkerIdentity = "worker-b" })
	verified, err := fx.verify(raw, testNow)
	reason, ok := decisiontrust.FailureReason(err)
	if !ok || reason != decisiontrust.ReasonSubjectMismatch {
		t.Fatalf("reason = %q err=%v, want %q", reason, err, decisiontrust.ReasonSubjectMismatch)
	}
	if !verified.IsZero() {
		t.Fatal("failed verification returned a non-zero Verified")
	}
}

func TestVerifyRejectsOutsideTheWorkOrderWindow(t *testing.T) {
	fx := newFixture(t)
	raw := fx.signedWorkOrder(nil)
	cases := map[string]struct {
		now  time.Time
		want decisiontrust.VerificationReason
	}{
		"before issue": {now: testIssuedAt.Add(-time.Minute), want: decisiontrust.ReasonNotYetValid},
		"after expiry": {now: testExpires.Add(10 * time.Second), want: decisiontrust.ReasonExpired},
	}
	for name, tc := range cases {
		bundle := decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: tc.now.Add(-time.Minute)}
		_, err := fx.verifier.Verify(Request{Raw: raw, Now: tc.now, TrustBundle: bundle})
		reason, ok := decisiontrust.FailureReason(err)
		if !ok || reason != tc.want {
			t.Fatalf("%s: reason = %q err=%v, want %q", name, reason, err, tc.want)
		}
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	fx := newFixture(t)
	raw := fx.signedWorkOrder(nil)
	raw[len(raw)-1] ^= 0xff
	_, err := fx.verify(raw, testNow)
	if _, ok := decisiontrust.FailureReason(err); !ok {
		t.Fatalf("tampered signature error = %v, want a verification failure", err)
	}
}

func TestVerifyRejectsLeasePayload(t *testing.T) {
	fx := newFixture(t)
	lease, err := decisionpayload.NewLease(decisionpayload.LeaseConfig{
		SchemaVersion:      decisionpayload.LeaseSchemaVersion,
		DecisionKind:       authority.DecisionLease,
		DecisionID:         "lease-decision-001",
		AuthorityRole:      authority.RoleLease,
		AuthorityPrincipal: "control-plane/lse-1",
		IssuedAt:           testIssuedAt,
		ExpiresAt:          testExpires,
		PolicyRevision:     testPolicyRev,
		TaskID:             ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest:       contract.SHA256([]byte("taskir")),
		AttemptID:          ids.AttemptID("atm_00000000000000000000000000000001"),
		WorkerIdentity:     testWorker,
		LeaseID:            ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch:         1,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := fx.payload.EncodeLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fx.verify(fx.signPayload(payload), testNow)
	reason, ok := decisiontrust.FailureReason(err)
	if !ok || reason != decisiontrust.ReasonSchemaUnsupported {
		t.Fatalf("lease payload reason = %q err=%v, want %q", reason, err, decisiontrust.ReasonSchemaUnsupported)
	}
}

func TestVerifyRejectsAnotherTrustDomain(t *testing.T) {
	fx := newFixture(t)
	other, err := New(Config{
		Codec: fx.codec, Payload: fx.payload, Boundary: fx.verifier.cfg.Boundary,
		TrustDomain: "other.example", WorkerIdentity: testWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Verify(Request{Raw: fx.signedWorkOrder(nil), Now: testNow, TrustBundle: fx.bundle}); err == nil {
		t.Fatal("Verify() accepted a WorkOrder from another Trust Domain")
	}
}

func TestVerifyRejectsStaleTrustBundle(t *testing.T) {
	fx := newFixture(t)
	stale := decisiontrust.TrustBundleMetadata{Revision: 1, ValidatedAt: testNow.Add(-2 * time.Hour)}
	if _, err := fx.verifier.Verify(Request{Raw: fx.signedWorkOrder(nil), Now: testNow, TrustBundle: stale}); err == nil {
		t.Fatal("Verify() accepted a stale Trust Bundle")
	}
}

func TestVerifyRejectsMissingInputs(t *testing.T) {
	fx := newFixture(t)
	_, err := fx.verifier.Verify(Request{Raw: fx.signedWorkOrder(nil), TrustBundle: fx.bundle})
	if reason, ok := decisiontrust.FailureReason(err); !ok || reason != decisiontrust.ReasonTimeWindowInvalid {
		t.Fatalf("zero now reason = %q err=%v, want %q", reason, err, decisiontrust.ReasonTimeWindowInvalid)
	}
	if _, err := fx.verifier.Verify(Request{Now: testNow, TrustBundle: fx.bundle}); err == nil {
		t.Fatal("Verify() accepted empty bytes")
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	fx := newFixture(t)
	base := Config{Codec: fx.codec, Payload: fx.payload, Boundary: fx.verifier.cfg.Boundary,
		TrustDomain: testTrustDomain, WorkerIdentity: testWorker}
	cases := map[string]func(*Config){
		"codec":    func(c *Config) { c.Codec = nil },
		"payload":  func(c *Config) { c.Payload = nil },
		"boundary": func(c *Config) { c.Boundary = nil },
		"domain":   func(c *Config) { c.TrustDomain = "" },
		"worker":   func(c *Config) { c.WorkerIdentity = "" },
	}
	for name, mutate := range cases {
		cfg := base
		mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: New() error = %v, want %v", name, err, ErrInvalidConfig)
		}
	}
}
