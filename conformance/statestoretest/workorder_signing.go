package statestoretest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	replaymemory "github.com/hat47x/eki-fabric/internal/adapters/reference/replayguard/memory"
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/workorderaccept"
	"github.com/hat47x/eki-fabric/internal/core/workorderemission"
	"github.com/hat47x/eki-fabric/internal/core/workorderissue"
	"github.com/hat47x/eki-fabric/internal/core/workorderverify"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// WorkOrderSigningTestStore is a State Store that can reserve and emit a
// Lease and then emit or abort the WOR-1 WorkOrder for it.
type WorkOrderSigningTestStore interface {
	LeaseEmissionTestStore
	statestore.WorkOrderEmissionStore
}

type WorkOrderSigningFactory func(t testing.TB) WorkOrderSigningTestStore

type DurableWorkOrderSigningFactory struct {
	Open func(t testing.TB, location string) (store WorkOrderSigningTestStore, close func() error)
}

// RunWorkOrderSigningContract drives the real WOR-1 Issuer against a State
// Store. The happy path hands the issued bytes to the real Worker-side
// boundaries (workorderverify -> workorderaccept -> agent/startgate). Every
// lost premise must end in a durable ABORTED record with no signed bytes and
// no change to the Lease, Attempt, or FenceEpoch. Every transient failure
// must record nothing.
func RunWorkOrderSigningContract(t *testing.T, newStore WorkOrderSigningFactory) {
	t.Helper()

	t.Run("issued-workorder-passes-worker-verify-accept-startgate", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, leaseResult := fx.emittedLease("wo-happy")

		result, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		if err != nil {
			t.Fatalf("Issue() error = %v", err)
		}
		if result.Replayed || result.Record.Status() != workorderemission.StatusEmitted {
			t.Fatalf("Issue() = %#v", result)
		}
		stored, err := store.LoadWorkOrderEmission(context.Background(), lease.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored.SignedEnvelope(), result.SignedWorkOrder) {
			t.Fatal("returned bytes differ from the durable EMITTED bytes")
		}
		if result.Record.DecisionID() != workorderissue.WorkOrderDecisionID(lease.ID) ||
			result.Record.FenceEpoch() != lease.FenceEpoch ||
			result.Record.PolicyEpoch() != lease.PolicyEpoch ||
			result.Record.WorkerIdentity() != lease.WorkerIdentity ||
			result.Record.ExpiresAt().After(lease.ExpiresAt) {
			t.Fatalf("EMITTED record lost its Lease binding: %#v", result.Record)
		}

		admission := fx.acceptAsWorker(result.SignedWorkOrder, lease, leaseResult)
		order := fx.decode(result.SignedWorkOrder)
		if order.TaskID() != lease.TaskID ||
			order.TaskIRDigest() != lease.TaskIRDigest ||
			order.AttemptID() != lease.AttemptID ||
			order.WorkerIdentity() != lease.WorkerIdentity ||
			order.PolicyRevision() != lease.PolicyRevision ||
			order.PlacementRef().DecisionID() != lease.PlacementDecisionID ||
			order.PlacementRef().PayloadDigest() != lease.PlacementPayloadDigest ||
			order.LeaseRef().DecisionID() != leaseResult.Record.DecisionID() ||
			order.LeaseRef().PayloadDigest() != leaseResult.Record.PayloadDigest() ||
			order.ExpiresAt().After(lease.ExpiresAt) ||
			len(order.ReadGrants()) != 0 || len(order.WriteGrants()) != 0 {
			t.Fatalf("signed WorkOrder is not bound to the EMITTED Lease: %#v", order)
		}
		if admission.FenceEpoch() != lease.FenceEpoch || admission.OrderDecisionID() != order.DecisionID() {
			t.Fatalf("Admission = %#v", admission)
		}

		gate, err := startgate.New(startgate.Config{Clock: fx.workerClock})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := gate.Check(startgate.Request{
			Admission: admission,
			Governor: governor.Decision{AcceptNewLeases: true, Ceilings: map[governor.Resource]int{
				governor.ResourceCPU: 50, governor.ResourceMemory: 50,
			}},
			Machine: startgate.MachineProfile{CPUMillis: 8000, MemoryBytes: 8 << 30},
		})
		if err != nil {
			t.Fatalf("startgate.Check() error = %v", err)
		}
		if plan.IsZero() || plan.AttemptID() != lease.AttemptID || plan.Deadline().After(lease.ExpiresAt) {
			t.Fatalf("Plan = %#v", plan)
		}

		// The same signed bytes cannot be admitted twice.
		if _, err := fx.accept.Accept(context.Background(), fx.acceptRequest(result.SignedWorkOrder, lease, leaseResult)); !errors.Is(err, workorderaccept.ErrReplay) {
			t.Fatalf("second Accept() error = %v, want ErrReplay", err)
		}
	})

	t.Run("stale-policy-aborts", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-stale-policy")
		fx.lse.revokePolicy("emergency revoke after Lease emission")
		fx.lse.activatePolicy("policy-wo-next")

		result, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		fx.assertAborted(lease, result, err, workorderemission.AbortPolicyRevoked)
		if fx.signer.calls.Load() != 0 {
			t.Fatal("stale policy reached the WOR-1 signer")
		}
	})

	t.Run("policy-revoked-mid-signing-aborts", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-revoke-mid")
		fx.signer.afterSign = func() { fx.lse.revokePolicy("revoked while WOR-1 was signing") }

		result, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		fx.assertAborted(lease, result, err, workorderemission.AbortPolicyRevoked)
	})

	t.Run("policy-revoked-at-commit-is-refused-by-store", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-revoke-commit")
		hooked := &workOrderHookState{IssueState: store}
		hooked.beforeCommit = func() { fx.lse.revokePolicy("revoked at the linearization point") }

		result, err := fx.newIssuer(hooked).Issue(context.Background(), fx.request(lease))
		fx.assertAborted(lease, result, err, workorderemission.AbortPolicyRevoked)
	})

	t.Run("revoked-key-aborts", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-key-revoked")
		if err := fx.lse.registry.Revoke(workOrderKeyID, leaseNow); err != nil {
			t.Fatal(err)
		}

		result, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		fx.assertAborted(lease, result, err, workorderemission.AbortKeyUnauthorized)
		if fx.signer.calls.Load() != 0 {
			t.Fatal("revoked key reached the WOR-1 signer")
		}
	})

	t.Run("key-compromised-at-final-check-aborts", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-key-final")
		fx.keys.beforeCall = func(call int64) {
			// Call 1 selects the key before signing; call 2 is the last check
			// before the EMITTED commit.
			if call == 2 {
				if err := fx.lse.registry.MarkCompromised(workOrderKeyID, leaseNow); err != nil {
					t.Fatal(err)
				}
			}
		}

		result, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		fx.assertAborted(lease, result, err, workorderemission.AbortKeyUnauthorized)
	})

	t.Run("expired-lease-aborts", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-expired")
		fx.issuerClock.set(lease.ExpiresAt)

		result, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		fx.assertAborted(lease, result, err, workorderemission.AbortLeaseExpired)
	})

	t.Run("superseded-lease-aborts", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-superseded")
		attempt, err := store.LoadAttempt(context.Background(), lease.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		closed := attempt
		closed.Phase = attemptstate.AttemptPhaseClosed
		closed.CloseReason = attemptstate.CloseReasonSuperseded
		closed.Revision++
		closed.UpdatedAt = attempt.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, closed); err != nil {
			t.Fatal(err)
		}

		result, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		fx.assertAborted(lease, result, err, workorderemission.AbortLeaseSuperseded)
	})

	t.Run("mismatched-worker-aborts", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-worker")
		req := fx.request(lease)
		req.WorkerIdentity = "worker-b"

		result, err := fx.issuer.Issue(context.Background(), req)
		fx.assertAborted(lease, result, err, workorderemission.AbortWorkerMismatch)
		if fx.signer.calls.Load() != 0 {
			t.Fatal("mismatched Worker reached the WOR-1 signer")
		}
	})

	t.Run("reserved-lease-records-nothing", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease := fx.lse.reserve("wo-reserved-only")

		if _, err := fx.issuer.Issue(context.Background(), fx.request(lease)); !errors.Is(err, workorderissue.ErrLeaseNotEmitted) {
			t.Fatalf("Issue() error = %v, want ErrLeaseNotEmitted", err)
		}
		fx.assertNothingRecorded(lease)
	})

	t.Run("policy-unavailable-records-nothing", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-policy-unavailable")
		unavailable := &workOrderHookState{IssueState: store, policyErr: statestore.ErrPolicyUnavailable}

		if _, err := fx.newIssuer(unavailable).Issue(context.Background(), fx.request(lease)); !errors.Is(err, statestore.ErrPolicyUnavailable) {
			t.Fatalf("Issue() error = %v, want ErrPolicyUnavailable", err)
		}
		fx.assertNothingRecorded(lease)
		if fx.signer.calls.Load() != 0 {
			t.Fatal("unavailable policy reached the WOR-1 signer")
		}
	})

	t.Run("stale-trust-bundle-records-nothing", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-stale-bundle")
		fx.lse.bundle.set(decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: leaseNow.Add(-2 * time.Hour)})

		if _, err := fx.issuer.Issue(context.Background(), fx.request(lease)); err == nil {
			t.Fatal("stale Trust Bundle issued a WorkOrder")
		}
		fx.assertNothingRecorded(lease)

		fx.lse.bundle.set(decisiontrust.TrustBundleMetadata{Revision: 4, ValidatedAt: leaseNow})
		if _, err := fx.issuer.Issue(context.Background(), fx.request(lease)); err != nil {
			t.Fatalf("retry after Trust Bundle refresh error = %v", err)
		}
	})

	t.Run("crash-before-commit-records-nothing-and-retry-emits-once", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-crash-before")
		crashing := &workOrderHookState{IssueState: store, crashBeforeCommit: true}

		if _, err := fx.newIssuer(crashing).Issue(context.Background(), fx.request(lease)); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("crashing Issue() error = %v, want simulated crash", err)
		}
		fx.assertNothingRecorded(lease)

		first, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		if err != nil || first.Replayed {
			t.Fatalf("retry after crash = %#v err=%v", first, err)
		}
		signs := fx.signer.calls.Load()
		again, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		if err != nil || !again.Replayed || !bytes.Equal(again.SignedWorkOrder, first.SignedWorkOrder) {
			t.Fatalf("replay after emission = %#v err=%v", again, err)
		}
		if fx.signer.calls.Load() != signs {
			t.Fatal("replay of an EMITTED WorkOrder signed again")
		}
	})

	t.Run("crash-after-commit-replays-exact-stored-bytes", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-crash-after")
		lost := &workOrderHookState{IssueState: store, crashAfterCommit: true}

		if _, err := fx.newIssuer(lost).Issue(context.Background(), fx.request(lease)); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("Issue() with lost commit response error = %v", err)
		}
		stored, err := store.LoadWorkOrderEmission(context.Background(), lease.ID)
		if err != nil || stored.Status() != workorderemission.StatusEmitted {
			t.Fatalf("committed emission = %#v err=%v", stored, err)
		}
		signs := fx.signer.calls.Load()

		// After a policy revoke an EMITTED WorkOrder still replays the same
		// bytes and is never re-signed into a new Decision.
		fx.lse.revokePolicy("revoke after WorkOrder emission")
		replay, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		if err != nil || !replay.Replayed || !bytes.Equal(replay.SignedWorkOrder, stored.SignedEnvelope()) {
			t.Fatalf("replay = %#v err=%v", replay, err)
		}
		if fx.signer.calls.Load() != signs {
			t.Fatal("replay signed again")
		}
	})

	t.Run("replay-with-different-terms-releases-no-bytes", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-replay-conflict")
		if _, err := fx.issuer.Issue(context.Background(), fx.request(lease)); err != nil {
			t.Fatal(err)
		}
		req := fx.request(lease)
		req.Limits.CPUMillis++
		result, err := fx.issuer.Issue(context.Background(), req)
		if !errors.Is(err, workorderissue.ErrRequestConflict) || len(result.SignedWorkOrder) != 0 {
			t.Fatalf("Issue() with different terms = %#v err=%v, want ErrRequestConflict", result, err)
		}
	})

	t.Run("concurrent-issuers-converge-on-one-decision", func(t *testing.T) {
		store := newStore(t)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-concurrent")

		const workers = 4
		results := make([]workorderissue.IssueResult, workers)
		errs := make([]error, workers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i], errs[i] = fx.issuer.Issue(context.Background(), fx.request(lease))
			}(i)
		}
		close(start)
		wg.Wait()

		stored, err := store.LoadWorkOrderEmission(context.Background(), lease.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i := range results {
			if errs[i] != nil {
				t.Fatalf("issuer %d error = %v", i, errs[i])
			}
			if !bytes.Equal(results[i].SignedWorkOrder, stored.SignedEnvelope()) {
				t.Fatalf("issuer %d returned bytes other than the durable EMITTED bytes", i)
			}
		}
	})
}

// RunWorkOrderSigningDurabilityContract checks that WOR-1 survives a State
// Store restart: a crash before commit leaves nothing, an EMITTED WorkOrder
// replays the same bytes after reopen, and ABORTED stays ABORTED.
func RunWorkOrderSigningDurabilityContract(t *testing.T, factory DurableWorkOrderSigningFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("DurableWorkOrderSigningFactory.Open is nil")
	}

	t.Run("crash-restart-replay", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-restart")
		crashing := &workOrderHookState{IssueState: store, crashBeforeCommit: true}
		if _, err := fx.newIssuer(crashing).Issue(context.Background(), fx.request(lease)); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("crashing Issue() error = %v", err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		fx.rebind(reopened)
		fx.assertNothingRecorded(lease)
		first, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		if err != nil || first.Replayed {
			t.Fatalf("Issue() after restart = %#v err=%v", first, err)
		}
		if err := closeReopened(); err != nil {
			t.Fatal(err)
		}

		again, closeAgain := factory.Open(t, location)
		defer closeAgain()
		fx.rebind(again)
		signs := fx.signer.calls.Load()
		replay, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		if err != nil || !replay.Replayed || !bytes.Equal(replay.SignedWorkOrder, first.SignedWorkOrder) {
			t.Fatalf("replay after second restart = %#v err=%v", replay, err)
		}
		if fx.signer.calls.Load() != signs {
			t.Fatal("replay after restart signed again")
		}
	})

	t.Run("abort-survives-restart", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		fx := newWorkOrderFixture(t, store)
		lease, _ := fx.emittedLease("wo-abort-restart")
		fx.lse.revokePolicy("revoke before WorkOrder signing")
		if _, err := fx.issuer.Issue(context.Background(), fx.request(lease)); !errors.Is(err, workorderissue.ErrWorkOrderAborted) {
			t.Fatalf("Issue() error = %v, want ErrWorkOrderAborted", err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		defer closeReopened()
		fx.rebind(reopened)
		result, err := fx.issuer.Issue(context.Background(), fx.request(lease))
		if !errors.Is(err, workorderissue.ErrWorkOrderAborted) ||
			result.Record.Status() != workorderemission.StatusAborted ||
			result.Record.AbortReason() != workorderemission.AbortPolicyRevoked ||
			len(result.SignedWorkOrder) != 0 {
			t.Fatalf("Issue() after restart = %#v err=%v", result, err)
		}
	})
}

const (
	workOrderPrincipal = "control-plane/wor-1"
	workOrderKeyID     = decisiontrust.KeyID("kid-wor-1")
	workOrderWorker    = "worker-a"
)

type workOrderFixture struct {
	t           testing.TB
	store       WorkOrderSigningTestStore
	lse         *signingFixture
	signer      *countingSigner
	keys        *hookedKeys
	verifier    *decisiontrust.VerifierBoundary
	issuerClock *mutableClock
	workerClock fixedClock
	issuer      *workorderissue.Issuer
	accept      *workorderaccept.Service
}

// newWorkOrderFixture reuses the LSE-1 signing fixture to reserve and emit
// Leases, and registers a separate WOR-1 key in the same Registry.
func newWorkOrderFixture(t testing.TB, store WorkOrderSigningTestStore) *workOrderFixture {
	t.Helper()
	lse := newSigningFixture(t, store)

	keyTime := leaseNow.Add(-time.Hour)
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 57
	private := ed25519.NewKeyFromSeed(seed)
	record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
		ID:                 workOrderKeyID,
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: workOrderPrincipal,
		TrustDomain:        signingTrustDomain,
		Algorithm:          decisiontrust.AlgorithmEdDSA,
		KeyType:            decisiontrust.KeyTypeOKP,
		Curve:              decisiontrust.CurveEd25519,
		PublicKey:          private.Public(),
		SigningNotBefore:   keyTime,
		SigningNotAfter:    leaseNow.Add(24 * time.Hour),
		PolicyRevision:     signingKeyPolicy,
	}, keyTime)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return lse.registry.Register(record, keyTime) },
		func() error { return lse.registry.Activate(record.ID(), keyTime) },
		func() error { return lse.registry.SetPrimary(record.ID(), keyTime) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	inner, err := decisiontrust.NewReferenceSoftwareSigner(lse.registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.RegisterPrivateKey(record.ID(), private); err != nil {
		t.Fatal(err)
	}
	policy, err := decisiontrust.NewVerificationPolicy(decisiontrust.VerificationPolicyConfig{
		MaxClockSkew:            5 * time.Second,
		MaxTrustBundleStaleness: time.Hour,
		AllowedAlgorithms:       []decisiontrust.Algorithm{decisiontrust.AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionLease:     10 * time.Minute,
			authority.DecisionWorkOrder: 5 * time.Minute,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := decisiontrust.NewVerifierBoundary(lse.registry, policy)
	if err != nil {
		t.Fatal(err)
	}

	fx := &workOrderFixture{
		t:           t,
		store:       store,
		lse:         lse,
		signer:      &countingSigner{inner: inner},
		keys:        &hookedKeys{inner: lse.registry},
		verifier:    verifier,
		issuerClock: &mutableClock{now: leaseNow.Add(2 * time.Second)},
		workerClock: fixedClock{now: leaseNow.Add(3 * time.Second)},
	}
	fx.issuer = fx.newIssuer(store)
	fx.accept = fx.newAccept()
	return fx
}

func (fx *workOrderFixture) newIssuer(state workorderissue.IssueState) *workorderissue.Issuer {
	fx.t.Helper()
	issuer, err := workorderissue.New(workorderissue.Config{
		State:              state,
		Signer:             fx.signer,
		Keys:               fx.keys,
		Verifier:           fx.verifier,
		Payload:            fx.lse.payload,
		Envelope:           fx.lse.codec,
		TrustBundle:        fx.lse.bundle,
		Clock:              fx.issuerClock,
		TrustDomain:        signingTrustDomain,
		AuthorityPrincipal: workOrderPrincipal,
		Algorithm:          decisiontrust.AlgorithmEdDSA,
		KeyPolicyRevision:  signingKeyPolicy,
		MaxLifetime:        30 * time.Second,
	})
	if err != nil {
		fx.t.Fatal(err)
	}
	return issuer
}

func (fx *workOrderFixture) newAccept() *workorderaccept.Service {
	fx.t.Helper()
	verify, err := workorderverify.New(workorderverify.Config{
		Codec:          fx.lse.codec,
		Payload:        fx.lse.payload,
		Boundary:       fx.verifier,
		TrustDomain:    signingTrustDomain,
		WorkerIdentity: workOrderWorker,
	})
	if err != nil {
		fx.t.Fatal(err)
	}
	guard, err := replaymemory.New(fx.workerClock, 16)
	if err != nil {
		fx.t.Fatal(err)
	}
	service, err := workorderaccept.New(workorderaccept.Config{Verifier: verify, Replay: guard, Clock: fx.workerClock})
	if err != nil {
		fx.t.Fatal(err)
	}
	return service
}

// rebind points the fixture at a reopened store while keeping the same keys.
func (fx *workOrderFixture) rebind(store WorkOrderSigningTestStore) {
	fx.store = store
	fx.lse.rebind(store)
	fx.issuer = fx.newIssuer(store)
}

// emittedLease reserves a Lease and emits it through the real LSE-1 Emitter.
func (fx *workOrderFixture) emittedLease(seed string) (leasestate.LeaseRecord, leaseissue.EmitResult) {
	fx.t.Helper()
	lease := fx.lse.reserve(seed)
	result, err := fx.lse.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: lease.ID})
	if err != nil {
		fx.t.Fatalf("LSE-1 Emit() error = %v", err)
	}
	if result.Record.Status() != leaseemission.StatusEmitted {
		fx.t.Fatalf("LSE-1 Emit() = %#v", result)
	}
	return lease, result
}

var (
	workOrderExecutorDecision = "executor-approval-1"
	workOrderExecutorPayload  = contract.SHA256([]byte("executor-approval-payload"))
	workOrderExecutorDigest   = contract.SHA256([]byte("executor-artifact"))
)

func (fx *workOrderFixture) request(lease leasestate.LeaseRecord) workorderissue.IssueRequest {
	return workorderissue.IssueRequest{
		LeaseID:        lease.ID,
		WorkerIdentity: lease.WorkerIdentity,
		ExecutorApproval: workorderissue.ExecutorApproval{
			DecisionID:     workOrderExecutorDecision,
			PayloadDigest:  workOrderExecutorPayload,
			ExecutorDigest: workOrderExecutorDigest,
		},
		Limits: decisionpayload.ResourceLimits{
			CPUMillis: 2000, MemoryBytes: 1 << 30, WallTimeMillis: 60_000,
		},
		Sandbox: decisionpayload.SandboxPolicy{Profile: "sandbox/container/v1"},
	}
}

func (fx *workOrderFixture) decode(signed []byte) decisionpayload.WorkOrder {
	fx.t.Helper()
	envelope, err := fx.lse.codec.DecodeSign1(signed)
	if err != nil {
		fx.t.Fatal(err)
	}
	order, err := fx.lse.payload.DecodeWorkOrder(envelope.Payload())
	if err != nil {
		fx.t.Fatal(err)
	}
	return order
}

// acceptRequest builds the Worker's own evidence. The Lease evidence comes
// from verifying the signed Lease as a Worker, not from the WorkOrder bytes.
func (fx *workOrderFixture) acceptRequest(signed []byte, lease leasestate.LeaseRecord, leaseResult leaseissue.EmitResult) workorderaccept.Request {
	fx.t.Helper()
	verifiedLease := fx.lse.verifyAsWorker(leaseResult.SignedLease)
	return workorderaccept.Request{
		Raw:         signed,
		TrustBundle: fx.lse.bundle.get(),
		Lease: workorderaccept.LeaseEvidence{
			DecisionID:     verifiedLease.DecisionID(),
			PayloadDigest:  leaseResult.Record.PayloadDigest(),
			TaskID:         verifiedLease.TaskID(),
			TaskIRDigest:   verifiedLease.TaskIRDigest(),
			AttemptID:      verifiedLease.AttemptID(),
			WorkerIdentity: verifiedLease.WorkerIdentity(),
			FenceEpoch:     verifiedLease.FenceEpoch(),
			ExpiresAt:      verifiedLease.ExpiresAt(),
		},
		Placement: workorderaccept.PlacementEvidence{
			DecisionID:     lease.PlacementDecisionID,
			PayloadDigest:  lease.PlacementPayloadDigest,
			TaskID:         lease.TaskID,
			TaskIRDigest:   lease.TaskIRDigest,
			AttemptID:      lease.AttemptID,
			WorkerIdentity: lease.WorkerIdentity,
		},
		Executor: workorderaccept.ExecutorEvidence{
			DecisionID:     workOrderExecutorDecision,
			PayloadDigest:  workOrderExecutorPayload,
			ExecutorDigest: workOrderExecutorDigest,
		},
	}
}

func (fx *workOrderFixture) acceptAsWorker(signed []byte, lease leasestate.LeaseRecord, leaseResult leaseissue.EmitResult) workorderaccept.Admission {
	fx.t.Helper()
	admission, err := fx.accept.Accept(context.Background(), fx.acceptRequest(signed, lease, leaseResult))
	if err != nil {
		fx.t.Fatalf("workorderaccept.Accept() error = %v", err)
	}
	return admission
}

// assertAborted checks the full ABORTED outcome: no signed bytes leave the
// Issuer, the durable record is ABORTED with the expected reason, the Lease,
// Lease emission, and Attempt are unchanged, FenceEpoch is not rewound, and a
// retry converges on the same ABORTED record.
func (fx *workOrderFixture) assertAborted(lease leasestate.LeaseRecord, result workorderissue.IssueResult, err error, reason workorderemission.AbortReason) {
	fx.t.Helper()
	attemptBefore, loadErr := fx.store.LoadAttempt(context.Background(), lease.AttemptID)
	if loadErr != nil {
		fx.t.Fatal(loadErr)
	}
	if reason != workorderemission.AbortLeaseSuperseded &&
		(attemptBefore.Phase != attemptstate.AttemptPhaseAssigned || attemptBefore.LeaseID != lease.ID) {
		fx.t.Fatalf("WorkOrder abort changed the Attempt: %#v", attemptBefore)
	}
	if !errors.Is(err, workorderissue.ErrWorkOrderAborted) {
		fx.t.Fatalf("Issue() error = %v, want ErrWorkOrderAborted", err)
	}
	if len(result.SignedWorkOrder) != 0 {
		fx.t.Fatal("aborted Issue returned signed WorkOrder bytes")
	}
	stored, loadErr := fx.store.LoadWorkOrderEmission(context.Background(), lease.ID)
	if loadErr != nil {
		fx.t.Fatal(loadErr)
	}
	if stored.Status() != workorderemission.StatusAborted || stored.AbortReason() != reason {
		fx.t.Fatalf("durable outcome = %q/%q, want aborted/%q", stored.Status(), stored.AbortReason(), reason)
	}
	if len(stored.SignedEnvelope()) != 0 {
		fx.t.Fatal("ABORTED record carries signed bytes")
	}
	current, loadErr := fx.store.LoadLease(context.Background(), lease.ID)
	if loadErr != nil {
		fx.t.Fatal(loadErr)
	}
	if current.FenceEpoch != lease.FenceEpoch {
		fx.t.Fatalf("FenceEpoch changed by WorkOrder abort: %d -> %d", lease.FenceEpoch, current.FenceEpoch)
	}
	leaseRecord, loadErr := fx.store.LoadLeaseEmission(context.Background(), lease.ID)
	if loadErr != nil || leaseRecord.Status() != leaseemission.StatusEmitted {
		fx.t.Fatalf("Lease emission after WorkOrder abort = %#v err=%v", leaseRecord, loadErr)
	}

	again, err := fx.issuer.Issue(context.Background(), fx.request(lease))
	if !errors.Is(err, workorderissue.ErrWorkOrderAborted) || !again.Replayed || len(again.SignedWorkOrder) != 0 {
		fx.t.Fatalf("Issue() after abort = %#v err=%v", again, err)
	}
	attemptAfter, loadErr := fx.store.LoadAttempt(context.Background(), lease.AttemptID)
	if loadErr != nil {
		fx.t.Fatal(loadErr)
	}
	if attemptAfter.Revision != attemptBefore.Revision || attemptAfter.Phase != attemptBefore.Phase {
		fx.t.Fatalf("WorkOrder abort replay changed the Attempt: %#v -> %#v", attemptBefore, attemptAfter)
	}
}

func (fx *workOrderFixture) assertNothingRecorded(lease leasestate.LeaseRecord) {
	fx.t.Helper()
	if _, err := fx.store.LoadWorkOrderEmission(context.Background(), lease.ID); !errors.Is(err, statestore.ErrNotFound) {
		fx.t.Fatalf("a transient failure produced a durable WorkOrder outcome: %v", err)
	}
}

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// workOrderHookState wraps a store to inject a revoke at the commit boundary,
// simulate a process crash before or after the durable commit, or make the
// policy source unavailable.
type workOrderHookState struct {
	workorderissue.IssueState
	beforeCommit      func()
	crashBeforeCommit bool
	crashAfterCommit  bool
	policyErr         error
}

func (s *workOrderHookState) LoadCurrentPolicy(ctx context.Context) (policystate.State, error) {
	if s.policyErr != nil {
		return policystate.State{}, s.policyErr
	}
	return s.IssueState.LoadCurrentPolicy(ctx)
}

func (s *workOrderHookState) CommitWorkOrderEmission(ctx context.Context, commit statestore.WorkOrderEmissionCommit) (statestore.WorkOrderEmissionResult, error) {
	if s.beforeCommit != nil {
		s.beforeCommit()
	}
	if s.crashBeforeCommit {
		return statestore.WorkOrderEmissionResult{}, errSimulatedCrash
	}
	result, err := s.IssueState.CommitWorkOrderEmission(ctx, commit)
	if err == nil && s.crashAfterCommit {
		return statestore.WorkOrderEmissionResult{}, errSimulatedCrash
	}
	return result, err
}
