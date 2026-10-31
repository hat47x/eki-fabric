package placementissue

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/placementverify"
)

const (
	testTrustDomain = "example.org"
	testPrincipal   = "control-plane/plc-1"
	testKeyPolicy   = "key-policy-1"
	testKeyID       = "kid-plc-1"
	testBundleRev   = uint64(3)
)

type testClock struct{ now *time.Time }

func (c testClock) Now() time.Time { return *c.now }

type testBundle struct{ at time.Time }

func (b testBundle) CurrentTrustBundle(context.Context) (decisiontrust.TrustBundleMetadata, error) {
	return decisiontrust.TrustBundleMetadata{Revision: testBundleRev, ValidatedAt: b.at}, nil
}

type testRig struct {
	issuer   *Issuer
	verifier *placementverify.Verifier
	clock    testClock
	bundle   testBundle
}

func newTestRig(t *testing.T, now time.Time) *testRig {
	t.Helper()
	codec, err := cosecbor.New()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := payloadcbor.New()
	if err != nil {
		t.Fatal(err)
	}
	registry := decisiontrust.NewRegistry(now)
	signer, err := decisiontrust.NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatal(err)
	}
	keySeed := make([]byte, ed25519.SeedSize)
	keySeed[0] = 7
	private := ed25519.NewKeyFromSeed(keySeed)
	record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
		ID: decisiontrust.KeyID(testKeyID), AuthorityRole: authority.RolePlacementAuthorization,
		AuthorityPrincipal: testPrincipal, TrustDomain: testTrustDomain,
		Algorithm: decisiontrust.AlgorithmEdDSA, KeyType: decisiontrust.KeyTypeOKP,
		Curve: decisiontrust.CurveEd25519, PublicKey: private.Public(),
		SigningNotBefore: now, SigningNotAfter: now.Add(25 * time.Hour), PolicyRevision: testKeyPolicy,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return registry.Register(record, now) },
		func() error { return registry.Activate(record.ID(), now) },
		func() error { return registry.SetPrimary(record.ID(), now) },
		func() error { return signer.RegisterPrivateKey(record.ID(), private) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := decisiontrust.NewVerificationPolicy(decisiontrust.VerificationPolicyConfig{
		MaxClockSkew:            5 * time.Second,
		MaxTrustBundleStaleness: time.Hour,
		AllowedAlgorithms:       []decisiontrust.Algorithm{decisiontrust.AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionPlacementApproval: 10 * time.Minute,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := decisiontrust.NewVerifierBoundary(registry, policy)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := placementverify.New(placementverify.Config{
		Codec: codec, Payload: payload, Boundary: boundary, TrustDomain: testTrustDomain,
	})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := New(Config{
		Signer: signer, Keys: registry, Verifier: boundary, Payload: payload, Envelope: codec,
		TrustBundle: testBundle{at: now}, Clock: testClock{now: &now},
		TrustDomain: testTrustDomain, AuthorityPrincipal: testPrincipal,
		Algorithm: decisiontrust.AlgorithmEdDSA, KeyPolicyRevision: testKeyPolicy,
		MaxValidity: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return &testRig{issuer: issuer, verifier: verifier, clock: testClock{now: &now}, bundle: testBundle{at: now}}
}

func testRequest() IssueRequest {
	taskID, _ := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	attemptID, _ := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	return IssueRequest{
		TaskID:         taskID,
		TaskIRDigest:   contract.SHA256([]byte("task-ir")),
		AttemptID:      attemptID,
		WorkerIdentity: "worker-a",
		PolicyRevision: "policy-42",
	}
}

func TestIssuerSignsPlacementTheConsumerVerifierAccepts(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	rig := newTestRig(t, now)

	result, err := rig.issuer.Issue(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !result.Approval.Valid() {
		t.Fatalf("Approval = %#v, want a closed value with the derived digest", result.Approval)
	}
	if result.Approval.DecisionID() != PlacementDecisionID(result.Approval.AttemptID()) {
		t.Fatalf("DecisionID = %q, want the deterministic per-Attempt ID", result.Approval.DecisionID())
	}
	if result.Approval.PayloadDigest().String() != result.PayloadDigest.String() {
		t.Fatalf("Approval payload digest = %s, want %s", result.Approval.PayloadDigest(), result.PayloadDigest)
	}

	verified, err := rig.verifier.Verify(placementverify.Request{
		Raw: result.SignedPlacement, Now: now, TrustBundle: decisiontrust.TrustBundleMetadata{Revision: testBundleRev, ValidatedAt: now},
	})
	if err != nil {
		t.Fatalf("placementverify.Verify() error = %v", err)
	}
	approval := verified.Approval()
	if !approval.Equal(result.Approval) ||
		approval.PayloadDigest().String() != result.PayloadDigest.String() ||
		verified.KeyID() != result.KeyID {
		t.Fatalf("verified Approval = %#v, want the issued value under key %s", approval, result.KeyID)
	}
	if approval.WorkerIdentity() != "worker-a" || approval.TaskID() != result.Approval.TaskID() ||
		approval.AttemptID() != result.Approval.AttemptID() || approval.PolicyRevision() != "policy-42" {
		t.Fatalf("verified Approval lost request bindings: %#v", approval)
	}
}

func TestIssuerYieldsTheSameDecisionIDOnRetry(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	rig := newTestRig(t, now)

	first, err := rig.issuer.Issue(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	*rig.clock.now = now.Add(time.Minute)
	second, err := rig.issuer.Issue(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Issue() retry error = %v", err)
	}
	if first.Approval.DecisionID() != second.Approval.DecisionID() {
		t.Fatalf("retry DecisionID = %q, want %q", second.Approval.DecisionID(), first.Approval.DecisionID())
	}
	if bytesEqual(first.SignedPlacement, second.SignedPlacement) {
		t.Fatal("retry reproduced byte-identical authority from a shifted window; a retry must re-sign the same Decision, not replay bytes")
	}
}

func TestIssuerCapsTheRequestedWindow(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	rig := newTestRig(t, now)

	req := testRequest()
	req.RequestedValidity = time.Hour
	result, err := rig.issuer.Issue(context.Background(), req)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := result.Approval.ExpiresAt().Sub(result.Approval.IssuedAt()); got != 5*time.Minute {
		t.Fatalf("window = %v, want the 5m maximum", got)
	}

	req.RequestedValidity = 90 * time.Second
	shrunk, err := rig.issuer.Issue(context.Background(), req)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := shrunk.Approval.ExpiresAt().Sub(shrunk.Approval.IssuedAt()); got != 90*time.Second {
		t.Fatalf("window = %v, want the 90s request", got)
	}
}

func TestIssuerRejectsInvalidRequests(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	rig := newTestRig(t, now)

	tests := []struct {
		name   string
		mutate func(*IssueRequest)
	}{
		{name: "task", mutate: func(r *IssueRequest) { r.TaskID = "tsk_bad" }},
		{name: "taskir", mutate: func(r *IssueRequest) { r.TaskIRDigest = contract.Digest{} }},
		{name: "attempt", mutate: func(r *IssueRequest) { r.AttemptID = "atm_bad" }},
		{name: "worker", mutate: func(r *IssueRequest) { r.WorkerIdentity = "" }},
		{name: "policy", mutate: func(r *IssueRequest) { r.PolicyRevision = "" }},
		{name: "negative validity", mutate: func(r *IssueRequest) { r.RequestedValidity = -time.Second }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testRequest()
			tt.mutate(&req)
			if _, err := rig.issuer.Issue(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Issue() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(zero config) error = %v, want ErrInvalidConfig", err)
	}
	var nilIssuer *Issuer
	if _, err := nilIssuer.Issue(context.Background(), testRequest()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil Issue() error = %v, want ErrInvalidConfig", err)
	}
}

func bytesEqual(a, b []byte) bool {
	return len(a) == len(b) && string(a) == string(b)
}
