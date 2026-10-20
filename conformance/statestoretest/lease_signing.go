package statestoretest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// RunLeaseSigningContract drives the real LSE-1 Emitter against a State Store
// and checks the #78 signing-boundary rows: revoke during signing, key loss
// before commit, crash and replay, and a reservation that outlives a policy
// revoke. The signing side uses the reference COSE / CBOR codecs and an
// in-process Ed25519 key, so every Store adapter is judged on the same bytes.
func RunLeaseSigningContract(t *testing.T, newStore LeaseEmissionFactory) {
	t.Helper()

	t.Run("emits-verifiable-lease-bound-to-reservation", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-happy")

		result, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if err != nil {
			t.Fatalf("Emit() error = %v", err)
		}
		if result.Replayed || result.Record.Status() != leaseemission.StatusEmitted {
			t.Fatalf("Emit() = %#v", result)
		}
		if result.Record.PolicyEpoch() != reserved.PolicyEpoch ||
			result.Record.PolicyRevision() != reserved.PolicyRevision ||
			result.Record.DecisionID() != leaseissue.LeaseDecisionID(reserved.ID) {
			t.Fatalf("EMITTED record lost policy / decision binding: %#v", result.Record)
		}
		lease := fx.verifyAsWorker(result.SignedLease)
		if lease.LeaseID() != reserved.ID ||
			lease.TaskID() != reserved.TaskID ||
			lease.TaskIRDigest() != reserved.TaskIRDigest ||
			lease.AttemptID() != reserved.AttemptID ||
			lease.WorkerIdentity() != reserved.WorkerIdentity ||
			lease.FenceEpoch() != reserved.FenceEpoch ||
			lease.PolicyRevision() != reserved.PolicyRevision ||
			lease.AuthorityPrincipal() != signingPrincipal ||
			lease.IssuedAt().Before(reserved.IssuedAt) ||
			lease.ExpiresAt().After(reserved.ExpiresAt) {
			t.Fatalf("signed Lease is not bound to the reservation:\nlease=%#v\nreserved=%#v", lease, reserved)
		}
		stored, err := store.LoadLeaseEmission(context.Background(), reserved.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored.SignedEnvelope(), result.SignedLease) {
			t.Fatal("returned bytes differ from the durable EMITTED bytes")
		}
	})

	t.Run("policy-revoked-mid-signing-aborts-without-authority", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-revoke-mid")
		fx.signer.afterSign = func() { fx.revokePolicy("revoked while LSE-1 was signing") }

		result, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		fx.assertAborted(reserved, result, err, attemptstate.CloseReasonPolicyRevoked)
		if fx.signer.calls.Load() != 1 {
			t.Fatalf("signer calls = %d, want 1", fx.signer.calls.Load())
		}
		fx.assertNextFenceAdvances(reserved)
	})

	t.Run("policy-revoked-after-final-check-is-refused-by-store", func(t *testing.T) {
		// The revoke lands after the Emitter's last read but before the
		// EMITTED commit. Only the State Store can see it, and it must win.
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-revoke-commit")
		hooked := &commitHookState{EmissionState: store}
		hooked.beforeCommit = func() { fx.revokePolicy("revoked at the linearization point") }
		emitter := fx.newEmitter(hooked)

		result, err := emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		fx.assertAborted(reserved, result, err, attemptstate.CloseReasonPolicyRevoked)
	})

	t.Run("key-revoked-after-signing-aborts-before-commit", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-key-revoked")
		fx.signer.afterSign = func() {
			if err := fx.registry.Revoke(signingKeyID, leaseNow); err != nil {
				t.Fatal(err)
			}
		}

		result, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		fx.assertAborted(reserved, result, err, attemptstate.CloseReasonPolicyRevoked)
	})

	t.Run("key-revoked-at-final-check-aborts-before-commit", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-key-final")
		fx.keys.beforeCall = func(call int64) {
			// Call 1 selects the key before signing; call 2 is the last
			// check before the EMITTED commit.
			if call == 2 {
				if err := fx.registry.MarkCompromised(signingKeyID, leaseNow); err != nil {
					t.Fatal(err)
				}
			}
		}

		result, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		fx.assertAborted(reserved, result, err, attemptstate.CloseReasonPolicyRevoked)
	})

	t.Run("crash-before-commit-keeps-reserved-and-retry-emits-once", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-crash-before")
		crashing := &commitHookState{EmissionState: store, crashBeforeCommit: true}

		if _, err := fx.newEmitter(crashing).Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID}); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("crashing Emit() error = %v, want simulated crash", err)
		}
		if _, err := store.LoadLeaseEmission(context.Background(), reserved.ID); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("signature before a failed commit became durable: %v", err)
		}
		attempt, err := store.LoadAttempt(context.Background(), reserved.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.Phase != attemptstate.AttemptPhaseAssigned || attempt.LeaseID != reserved.ID {
			t.Fatalf("crash before commit changed the reservation: %#v", attempt)
		}

		first, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if err != nil || first.Replayed {
			t.Fatalf("retry after crash = %#v err=%v", first, err)
		}
		signs := fx.signer.calls.Load()
		again, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if err != nil || !again.Replayed || !bytes.Equal(again.SignedLease, first.SignedLease) {
			t.Fatalf("replay after emission = %#v err=%v", again, err)
		}
		if fx.signer.calls.Load() != signs {
			t.Fatal("replay of an EMITTED Lease signed again")
		}
	})

	t.Run("crash-after-commit-replays-exact-stored-bytes", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-crash-after")
		lost := &commitHookState{EmissionState: store, crashAfterCommit: true}

		if _, err := fx.newEmitter(lost).Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID}); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("Emit() with lost commit response error = %v", err)
		}
		stored, err := store.LoadLeaseEmission(context.Background(), reserved.ID)
		if err != nil || stored.Status() != leaseemission.StatusEmitted {
			t.Fatalf("committed emission = %#v err=%v", stored, err)
		}
		signs := fx.signer.calls.Load()

		// Even after the policy is revoked, an already EMITTED Lease replays
		// the same bytes and is never re-signed into a new Decision.
		fx.revokePolicy("revoke after emission")
		replay, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if err != nil || !replay.Replayed {
			t.Fatalf("replay = %#v err=%v", replay, err)
		}
		if !bytes.Equal(replay.SignedLease, stored.SignedEnvelope()) {
			t.Fatal("replay returned bytes other than the durable EMITTED bytes")
		}
		if fx.signer.calls.Load() != signs {
			t.Fatal("replay signed again")
		}
	})

	t.Run("reservation-surviving-revoke-is-never-promoted", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-survives-revoke")
		placementReplay := fx.lastReservation

		// Revoke P1 and activate P2. The reservation made under P1 is still
		// durable and its Attempt is still ASSIGNED.
		fx.revokePolicy("emergency revoke")
		fx.activatePolicy("policy-43")

		result, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		fx.assertAborted(reserved, result, err, attemptstate.CloseReasonPolicyRevoked)
		if fx.signer.calls.Load() != 0 {
			t.Fatal("stale reservation reached the signer")
		}

		// The same Placement cannot mint a fresh Lease either.
		placementReplay.LeaseID = mustLeaseID(t)
		if _, err := store.ReserveLease(context.Background(), placementReplay); err == nil {
			t.Fatal("old Placement minted a new Lease after the reservation was aborted")
		}
		assertLeaseNotReserved(t, store, placementReplay.LeaseID)

		// Exact retry of Emit converges on the durable ABORTED record.
		again, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if !errors.Is(err, leaseissue.ErrLeaseAborted) || !again.Replayed {
			t.Fatalf("Emit() after abort = %#v err=%v", again, err)
		}
	})

	t.Run("cancelled-task-aborts", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-cancelled")
		task, err := store.LoadTask(context.Background(), reserved.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		next := task
		next.CancellationRequested = true
		next.Revision++
		next.UpdatedAt = task.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, next); err != nil {
			t.Fatal(err)
		}

		result, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		fx.assertAborted(reserved, result, err, attemptstate.CloseReasonCancelled)
	})

	t.Run("policy-unavailable-fails-closed-without-abort", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-policy-unavailable")
		unavailable := &commitHookState{EmissionState: store, policyErr: statestore.ErrPolicyUnavailable}

		if _, err := fx.newEmitter(unavailable).Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID}); !errors.Is(err, statestore.ErrPolicyUnavailable) {
			t.Fatalf("Emit() error = %v, want ErrPolicyUnavailable", err)
		}
		if _, err := store.LoadLeaseEmission(context.Background(), reserved.ID); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("unavailable policy produced a durable outcome: %v", err)
		}
		if fx.signer.calls.Load() != 0 {
			t.Fatal("unavailable policy reached the signer")
		}
	})

	t.Run("stale-trust-bundle-fails-closed-without-abort", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-stale-bundle")
		fx.bundle.set(decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: leaseNow.Add(-2 * time.Hour)})

		if _, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID}); err == nil {
			t.Fatal("stale Trust Bundle emitted a Lease")
		}
		if _, err := store.LoadLeaseEmission(context.Background(), reserved.ID); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("stale Trust Bundle produced a durable outcome: %v", err)
		}

		fx.bundle.set(decisiontrust.TrustBundleMetadata{Revision: 4, ValidatedAt: leaseNow})
		if _, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID}); err != nil {
			t.Fatalf("retry after Trust Bundle refresh error = %v", err)
		}
	})

	t.Run("concurrent-emitters-converge-on-one-decision", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-concurrent")

		const workers = 4
		results := make([]leaseissue.EmitResult, workers)
		errs := make([]error, workers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i], errs[i] = fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
			}(i)
		}
		close(start)
		wg.Wait()

		stored, err := store.LoadLeaseEmission(context.Background(), reserved.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i := range results {
			if errs[i] != nil {
				t.Fatalf("emitter %d error = %v", i, errs[i])
			}
			if !bytes.Equal(results[i].SignedLease, stored.SignedEnvelope()) {
				t.Fatalf("emitter %d returned bytes other than the durable EMITTED bytes", i)
			}
		}
	})
}

// RunLeaseSigningDurabilityContract checks that the signing boundary survives
// a State Store restart: a crash before commit leaves a retryable RESERVED
// Lease, and an EMITTED Lease replays the same bytes after reopen.
func RunLeaseSigningDurabilityContract(t *testing.T, factory DurableLeaseEmissionFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("DurableLeaseEmissionFactory.Open is nil")
	}

	t.Run("crash-restart-replay", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-restart")
		crashing := &commitHookState{EmissionState: store, crashBeforeCommit: true}
		if _, err := fx.newEmitter(crashing).Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID}); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("crashing Emit() error = %v", err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		fx.rebind(reopened)
		first, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if err != nil || first.Replayed {
			t.Fatalf("Emit() after restart = %#v err=%v", first, err)
		}
		if err := closeReopened(); err != nil {
			t.Fatal(err)
		}

		again, closeAgain := factory.Open(t, location)
		defer closeAgain()
		fx.rebind(again)
		replay, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if err != nil || !replay.Replayed || !bytes.Equal(replay.SignedLease, first.SignedLease) {
			t.Fatalf("replay after second restart = %#v err=%v", replay, err)
		}
	})

	t.Run("abort-survives-restart", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		fx := newSigningFixture(t, store)
		reserved := fx.reserve("sign-abort-restart")
		fx.revokePolicy("revoke before signing")
		if _, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID}); !errors.Is(err, leaseissue.ErrLeaseAborted) {
			t.Fatalf("Emit() error = %v, want ErrLeaseAborted", err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		defer closeReopened()
		fx.rebind(reopened)
		result, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if !errors.Is(err, leaseissue.ErrLeaseAborted) || result.Record.Status() != leaseemission.StatusAborted || len(result.SignedLease) != 0 {
			t.Fatalf("Emit() after restart = %#v err=%v", result, err)
		}
	})
}

const (
	signingTrustDomain = "local.example"
	signingPrincipal   = "control-plane/lse-1"
	signingKeyID       = decisiontrust.KeyID("kid-lse-1")
	signingKeyPolicy   = "key-policy-1"
)

var errSimulatedCrash = errors.New("simulated crash at the emission commit")

type signingFixture struct {
	t        testing.TB
	store    LeaseEmissionTestStore
	registry *decisiontrust.Registry
	signer   *countingSigner
	keys     *hookedKeys
	verifier *decisiontrust.VerifierBoundary
	codec    *cosecbor.Codec
	payload  *payloadcbor.Decoder
	bundle   *mutableBundle
	clock    fixedClock
	emitter  *leaseissue.Emitter

	lastReservation statestore.LeaseReservation
}

func newSigningFixture(t testing.TB, store LeaseEmissionTestStore) *signingFixture {
	t.Helper()
	codec, err := cosecbor.New()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := payloadcbor.New()
	if err != nil {
		t.Fatal(err)
	}

	keyTime := leaseNow.Add(-time.Hour)
	registry := decisiontrust.NewRegistry(keyTime)
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 78
	private := ed25519.NewKeyFromSeed(seed)
	record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
		ID:                 signingKeyID,
		AuthorityRole:      authority.RoleLease,
		AuthorityPrincipal: signingPrincipal,
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
		func() error { return registry.Register(record, keyTime) },
		func() error { return registry.Activate(record.ID(), keyTime) },
		func() error { return registry.SetPrimary(record.ID(), keyTime) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	inner, err := decisiontrust.NewReferenceSoftwareSigner(registry)
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
			authority.DecisionLease: 10 * time.Minute,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := decisiontrust.NewVerifierBoundary(registry, policy)
	if err != nil {
		t.Fatal(err)
	}

	fx := &signingFixture{
		t:        t,
		store:    store,
		registry: registry,
		signer:   &countingSigner{inner: inner},
		keys:     &hookedKeys{inner: registry},
		verifier: verifier,
		codec:    codec,
		payload:  payload,
		bundle:   &mutableBundle{value: decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: leaseNow}},
		clock:    fixedClock{now: leaseNow.Add(time.Second)},
	}
	fx.emitter = fx.newEmitter(store)
	return fx
}

func (fx *signingFixture) newEmitter(state leaseissue.EmissionState) *leaseissue.Emitter {
	fx.t.Helper()
	emitter, err := leaseissue.NewEmitter(leaseissue.EmitterConfig{
		State:              state,
		Signer:             fx.signer,
		Keys:               fx.keys,
		Verifier:           fx.verifier,
		Payload:            fx.payload,
		Envelope:           fx.codec,
		TrustBundle:        fx.bundle,
		Clock:              fx.clock,
		TrustDomain:        signingTrustDomain,
		AuthorityPrincipal: signingPrincipal,
		Algorithm:          decisiontrust.AlgorithmEdDSA,
		KeyPolicyRevision:  signingKeyPolicy,
	})
	if err != nil {
		fx.t.Fatal(err)
	}
	return emitter
}

// rebind points the fixture at a reopened store while keeping the same key.
func (fx *signingFixture) rebind(store LeaseEmissionTestStore) {
	fx.store = store
	fx.emitter = fx.newEmitter(store)
}

func (fx *signingFixture) reserve(seed string) leasestate.LeaseRecord {
	fx.t.Helper()
	task := readyTask(fx.t, fx.store, seed)
	reservation := leaseReservation(fx.t, fx.store, task, "placement-"+seed, leaseNow, leaseNow.Add(time.Minute))
	reserved, err := fx.store.ReserveLease(context.Background(), reservation)
	if err != nil {
		fx.t.Fatal(err)
	}
	fx.lastReservation = reservation
	return reserved.Lease
}

func (fx *signingFixture) revokePolicy(reason string) {
	fx.t.Helper()
	current, err := fx.store.LoadCurrentPolicy(context.Background())
	if err != nil {
		fx.t.Fatal(err)
	}
	revoked, err := policystate.Revoke(current, current.Epoch+1, leaseNow, reason)
	if err != nil {
		fx.t.Fatal(err)
	}
	if err := fx.store.CompareAndSwapPolicy(context.Background(), current.Epoch, revoked); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *signingFixture) activatePolicy(revision string) {
	fx.t.Helper()
	current, err := fx.store.LoadCurrentPolicy(context.Background())
	if err != nil {
		fx.t.Fatal(err)
	}
	next, err := policystate.NewActive(current.Epoch+1, revision, leaseNow)
	if err != nil {
		fx.t.Fatal(err)
	}
	if err := fx.store.CompareAndSwapPolicy(context.Background(), current.Epoch, next); err != nil {
		fx.t.Fatal(err)
	}
}

// assertAborted checks the full ABORTED outcome: no signed bytes leave the
// Emitter, the durable record is ABORTED with the expected reason, and the
// Attempt is closed so re-placement can proceed.
func (fx *signingFixture) assertAborted(reserved leasestate.LeaseRecord, result leaseissue.EmitResult, err error, reason attemptstate.CloseReason) {
	fx.t.Helper()
	if !errors.Is(err, leaseissue.ErrLeaseAborted) {
		fx.t.Fatalf("Emit() error = %v, want ErrLeaseAborted", err)
	}
	if len(result.SignedLease) != 0 {
		fx.t.Fatal("aborted Emit returned signed Lease bytes")
	}
	stored, loadErr := fx.store.LoadLeaseEmission(context.Background(), reserved.ID)
	if loadErr != nil {
		fx.t.Fatal(loadErr)
	}
	if stored.Status() != leaseemission.StatusAborted || stored.CloseReason() != string(reason) {
		fx.t.Fatalf("durable outcome = %q/%q, want aborted/%q", stored.Status(), stored.CloseReason(), reason)
	}
	if len(stored.SignedEnvelope()) != 0 {
		fx.t.Fatal("ABORTED record carries signed bytes")
	}
	attempt, loadErr := fx.store.LoadAttempt(context.Background(), reserved.AttemptID)
	if loadErr != nil {
		fx.t.Fatal(loadErr)
	}
	if attempt.Phase != attemptstate.AttemptPhaseClosed || attempt.CloseReason != reason {
		fx.t.Fatalf("Attempt after abort = %s/%s, want closed/%s", attempt.Phase, attempt.CloseReason, reason)
	}
}

// assertNextFenceAdvances re-places the same Task under the still-current
// policy and checks that FenceEpoch did not roll back over the abort.
func (fx *signingFixture) assertNextFenceAdvances(reserved leasestate.LeaseRecord) {
	fx.t.Helper()
	fx.activatePolicy("policy-after-" + reserved.ID.String()[len(reserved.ID.String())-8:])
	task, err := fx.store.LoadTask(context.Background(), reserved.TaskID)
	if err != nil {
		fx.t.Fatal(err)
	}
	policy, err := fx.store.LoadCurrentPolicy(context.Background())
	if err != nil {
		fx.t.Fatal(err)
	}
	next := leaseReservation(fx.t, fx.store, task, "placement-next-"+reserved.ID.String(), leaseNow.Add(2*time.Second), leaseNow.Add(time.Minute))
	next.PolicyRevision = policy.Revision
	next.ExpectedPolicyEpoch = policy.Epoch
	second, err := fx.store.ReserveLease(context.Background(), next)
	if err != nil {
		fx.t.Fatalf("re-placement after abort error = %v", err)
	}
	if second.Lease.FenceEpoch <= reserved.FenceEpoch {
		fx.t.Fatalf("FenceEpoch rolled back: aborted=%d next=%d", reserved.FenceEpoch, second.Lease.FenceEpoch)
	}
}

// verifyAsWorker decodes and verifies signed bytes the way a relying party
// does, independently of the Emitter.
func (fx *signingFixture) verifyAsWorker(signed []byte) decisionpayload.Lease {
	fx.t.Helper()
	envelope, err := fx.codec.DecodeSign1(signed)
	if err != nil {
		fx.t.Fatalf("DecodeSign1() error = %v", err)
	}
	lease, err := fx.payload.DecodeLease(envelope.Payload())
	if err != nil {
		fx.t.Fatalf("DecodeLease() error = %v", err)
	}
	sig, err := fx.codec.EncodeSignature1(envelope.ProtectedHeader(), envelope.Payload())
	if err != nil {
		fx.t.Fatal(err)
	}
	if _, err := fx.verifier.VerifyCryptographicSignature(decisiontrust.CryptographicVerificationRequest{
		PreCrypto: decisiontrust.PreCryptoVerificationRequest{
			Key: decisiontrust.VerificationRequest{
				KeyID:              envelope.KeyID(),
				TrustDomain:        signingTrustDomain,
				AuthorityRole:      lease.AuthorityRole(),
				AuthorityPrincipal: lease.AuthorityPrincipal(),
				DecisionKind:       lease.DecisionKind(),
				ProtectedAlgorithm: envelope.Algorithm(),
				IssuedAt:           lease.IssuedAt(),
			},
			ExpiresAt:   lease.ExpiresAt(),
			Now:         fx.clock.now,
			TrustBundle: fx.bundle.get(),
		},
		SigStructure: sig,
		Signature:    envelope.Signature(),
	}); err != nil {
		fx.t.Fatalf("signed Lease does not verify: %v", err)
	}
	return lease
}

type countingSigner struct {
	inner     leaseissue.LeaseSigner
	afterSign func()
	calls     atomic.Int64
}

func (s *countingSigner) SignCOSESigStructure(req decisiontrust.COSESigningRequest, now time.Time) (decisiontrust.SignatureResult, error) {
	s.calls.Add(1)
	result, err := s.inner.SignCOSESigStructure(req, now)
	if s.afterSign != nil {
		s.afterSign()
	}
	return result, err
}

type hookedKeys struct {
	inner      leaseissue.SigningKeySource
	beforeCall func(call int64)
	calls      atomic.Int64
}

func (k *hookedKeys) ResolveSigningKey(req decisiontrust.SigningRequest, now time.Time) (decisiontrust.KeyRecord, error) {
	call := k.calls.Add(1)
	if k.beforeCall != nil {
		k.beforeCall(call)
	}
	return k.inner.ResolveSigningKey(req, now)
}

type mutableBundle struct {
	mu    sync.Mutex
	value decisiontrust.TrustBundleMetadata
}

func (b *mutableBundle) set(value decisiontrust.TrustBundleMetadata) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.value = value
}

func (b *mutableBundle) get() decisiontrust.TrustBundleMetadata {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.value
}

func (b *mutableBundle) CurrentTrustBundle(context.Context) (decisiontrust.TrustBundleMetadata, error) {
	return b.get(), nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// commitHookState wraps a store to inject a revoke at the commit boundary,
// simulate a process crash before or after the durable commit, or make the
// policy source unavailable.
type commitHookState struct {
	leaseissue.EmissionState
	beforeCommit      func()
	crashBeforeCommit bool
	crashAfterCommit  bool
	policyErr         error
}

func (s *commitHookState) LoadCurrentPolicy(ctx context.Context) (policystate.State, error) {
	if s.policyErr != nil {
		return policystate.State{}, s.policyErr
	}
	return s.EmissionState.LoadCurrentPolicy(ctx)
}

func (s *commitHookState) CommitLeaseEmission(ctx context.Context, commit statestore.LeaseEmissionCommit) (statestore.LeaseEmissionResult, error) {
	if s.beforeCommit != nil {
		s.beforeCommit()
	}
	if s.crashBeforeCommit {
		return statestore.LeaseEmissionResult{}, errSimulatedCrash
	}
	result, err := s.EmissionState.CommitLeaseEmission(ctx, commit)
	if err == nil && s.crashAfterCommit {
		return statestore.LeaseEmissionResult{}, errSimulatedCrash
	}
	return result, err
}
