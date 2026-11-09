package workorderverify

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	wo "github.com/hat47x/eki-fabric/internal/testfixtures/workorder"
)

const (
	testTrustDomain = wo.TrustDomain
	testWorker      = wo.Worker
	testPrincipal   = wo.Principal
	testPolicyRev   = wo.PolicyRevision
)

var (
	testIssuedAt = wo.IssuedAt
	testExpires  = wo.Expires
	testNow      = wo.Now
	testKeyID    = wo.KeyID
)

type fixture struct {
	t        *testing.T
	signing  *wo.Signing
	bundle   decisiontrust.TrustBundleMetadata
	verifier *Verifier
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	signing := wo.NewSigning(t, 23)
	verifier, err := New(Config{
		Codec: signing.Codec, Payload: signing.Payload, Boundary: signing.Boundary,
		TrustDomain: testTrustDomain, WorkerIdentity: testWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{
		t: t, signing: signing,
		bundle:   decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: testNow.Add(-time.Minute)},
		verifier: verifier,
	}
}

func (f *fixture) order(mutate func(*decisionpayload.WorkOrderConfig)) decisionpayload.WorkOrder {
	f.t.Helper()
	return wo.Order(f.t, mutate)
}

func (f *fixture) signPayload(payload []byte) []byte {
	f.t.Helper()
	return f.signing.SignPayload(payload)
}

func (f *fixture) signedWorkOrder(mutate func(*decisionpayload.WorkOrderConfig)) []byte {
	f.t.Helper()
	return f.signing.SignedWorkOrder(mutate)
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
	payload, err := fx.signing.Payload.EncodeLease(lease)
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
		Codec: fx.signing.Codec, Payload: fx.signing.Payload, Boundary: fx.verifier.cfg.Boundary,
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
	base := Config{Codec: fx.signing.Codec, Payload: fx.signing.Payload, Boundary: fx.verifier.cfg.Boundary,
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
