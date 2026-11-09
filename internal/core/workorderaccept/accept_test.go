package workorderaccept

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workorderverify"
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

	testTask    = wo.Task
	testAttempt = wo.Attempt
	testTaskIR  = wo.TaskIR
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
	signing *wo.Signing
	bundle  decisiontrust.TrustBundleMetadata
	guard   *fakeGuard
	service *Service
	lease   LeaseEvidence
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	signing := wo.NewSigning(t, 31)
	verifier, err := workorderverify.New(workorderverify.Config{
		Codec: signing.Codec, Payload: signing.Payload, Boundary: signing.Boundary,
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
		DecisionID:     wo.LeaseDecisionID,
		PayloadDigest:  wo.LeaseDigest,
		TaskID:         testTask,
		TaskIRDigest:   testTaskIR,
		AttemptID:      testAttempt,
		WorkerIdentity: testWorker,
		FenceEpoch:     7,
		ExpiresAt:      testIssuedAt.Add(time.Minute),
	}
	return &fixture{
		t: t, signing: signing,
		bundle:  decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: testNow.Add(-time.Minute)},
		guard:   guard,
		service: service,
		lease:   lease,
	}
}

// request signs the default WorkOrder after mutate and pairs it with evidence
// that matches the default WorkOrder's references.
func (f *fixture) request(mutate func(*decisionpayload.WorkOrderConfig)) Request {
	f.t.Helper()
	return Request{
		Raw:         f.signing.SignedWorkOrder(mutate),
		TrustBundle: f.bundle,
		Lease:       f.lease,
		Placement: PlacementEvidence{
			DecisionID: "placement-001", PayloadDigest: wo.PlacementDigest,
			TaskID: testTask, TaskIRDigest: testTaskIR, AttemptID: testAttempt, WorkerIdentity: testWorker,
		},
		Executor: ExecutorEvidence{
			DecisionID: "executor-001", PayloadDigest: wo.ExecutorDigest,
			ExecutorDigest: wo.ExecutableDigest,
		},
	}
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
