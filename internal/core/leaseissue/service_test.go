package leaseissue_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"reflect"
	"testing"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/placementverify"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

const (
	testTrustDomain = "local.example"
	testPrincipal   = "control-plane/plc-1"
	testKeyID       = "kid-plc-1"
	testTaskID      = "tsk_00000000000000000000000000000001"
	testAttemptID   = "atm_00000000000000000000000000000001"
	testWorkerID    = "worker-a"
	testPolicyRev   = "policy-42"
)

var (
	testIssuedAt = time.UnixMilli(1_780_000_000_000).UTC()
	testExpires  = testIssuedAt.Add(2 * time.Minute)
	testNow      = testIssuedAt.Add(10 * time.Second)
	testTaskIR   = bytes.Repeat([]byte{0x22}, 32)
)

type staticTrustBundle struct {
	value decisiontrust.TrustBundleMetadata
	err   error
}

func (s staticTrustBundle) CurrentTrustBundle(context.Context) (decisiontrust.TrustBundleMetadata, error) {
	return s.value, s.err
}

type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time {
	if c == nil {
		return time.Time{}
	}
	return c.now
}

type cryptoFixture struct {
	t        *testing.T
	registry *decisiontrust.Registry
	signer   *decisiontrust.ReferenceSoftwareSigner
	codec    *cosecbor.Codec
	payload  *payloadcbor.Decoder
	enc      fxcbor.EncMode
	bundle   decisiontrust.TrustBundleMetadata
	verifier *placementverify.Verifier
}

func newCryptoFixture(t *testing.T) *cryptoFixture {
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
	seed[0] = 17
	private := ed25519.NewKeyFromSeed(seed)
	record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
		ID:                 testKeyID,
		AuthorityRole:      authority.RolePlacementAuthorization,
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
			authority.DecisionPlacementApproval: 5 * time.Minute,
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
	return &cryptoFixture{
		t: t, registry: registry, signer: signer, codec: codec, payload: payload, enc: enc,
		bundle: decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: testNow.Add(-time.Minute)},
		verifier: verifier,
	}
}

func (f *cryptoFixture) signedPlacement() []byte {
	f.t.Helper()
	payload := f.must(f.enc.Marshal(map[uint64]any{
		1: uint64(1),
		2: string(authority.DecisionPlacementApproval),
		3: "plc-decision-001",
		4: map[uint64]any{1: string(authority.RolePlacementAuthorization), 2: testPrincipal},
		5: uint64(testIssuedAt.UnixMilli()),
		6: uint64(testExpires.UnixMilli()),
		7: testPolicyRev,
		8: map[uint64]any{
			1: testTaskID,
			2: map[uint64]any{1: uint64(1), 2: testTaskIR},
			3: testAttemptID,
			4: testWorkerID,
		},
	}))
	protected := f.must(f.enc.Marshal(map[int64]any{
		1:      int64(-8),
		2:      []int64{decisiontrust.COSEHeaderLabelEKIProfile},
		4:      []byte(testKeyID),
		-70000: decisiontrust.SignedDecisionProfileV1,
	}))
	sigStructure, err := f.codec.EncodeSignature1(protected, payload)
	if err != nil {
		f.t.Fatal(err)
	}
	signed, err := f.signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key: decisiontrust.SigningRequest{
			TrustDomain:        testTrustDomain,
			AuthorityRole:      authority.RolePlacementAuthorization,
			AuthorityPrincipal: testPrincipal,
			DecisionKind:       authority.DecisionPlacementApproval,
			Algorithm:          decisiontrust.AlgorithmEdDSA,
		},
		PolicyRevision: "key-policy-1",
		SigStructure:   sigStructure,
	}, testIssuedAt)
	if err != nil {
		f.t.Fatal(err)
	}

	out := []byte{0xd2, 0x84}
	out = append(out, f.must(f.enc.Marshal(protected))...)
	out = append(out, 0xa0)
	out = append(out, f.must(f.enc.Marshal(payload))...)
	out = append(out, f.must(f.enc.Marshal(signed.Signature))...)
	return out
}

func (f *cryptoFixture) must(v []byte, err error) []byte {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}

func TestRequestSurfaceContainsNoCallerSuppliedAuthorityFields(t *testing.T) {
	typ := reflect.TypeOf(leaseissue.Request{})
	got := make([]string, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		got[i] = typ.Field(i).Name
	}
	want := []string{"SignedPlacement", "RequestedLifetime"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Request fields = %v, want only %v", got, want)
	}
}

func TestReserveDerivesAuthorityBindingsFromSignedPlacementAndCurrentState(t *testing.T) {
	fx := newCryptoFixture(t)
	store := readyPlacingState(t, testWorkerID)
	clock := &testClock{now: testNow}
	service := newService(t, fx, store, 30*time.Second, testPolicyRev, clock)

	result, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement:   fx.signedPlacement(),
		RequestedLifetime: time.Minute,
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if result.Replayed {
		t.Fatal("first reservation was marked replayed")
	}
	lease := result.Lease
	if lease.TaskID.String() != testTaskID ||
		lease.AttemptID.String() != testAttemptID ||
		lease.WorkerIdentity != testWorkerID ||
		lease.PlacementTrustDomain != testTrustDomain ||
		lease.PlacementAuthorityPrincipal != testPrincipal ||
		lease.PlacementDecisionID != "plc-decision-001" ||
		lease.PolicyRevision != testPolicyRev ||
		lease.PolicyEpoch != 1 ||
		!lease.PolicyFenced() {
		t.Fatalf("Lease lost signed Placement bindings: %#v", lease)
	}
	if lease.PlacementPayloadDigest.IsZero() {
		t.Fatal("Lease lost exact verified payload digest")
	}
	if !lease.IssuedAt.Equal(testNow) ||
		!lease.ExpiresAt.Equal(testNow.Add(30*time.Second)) {
		t.Fatalf("Lease window = %s..%s, want max-lifetime cap ending %s", lease.IssuedAt, lease.ExpiresAt, testNow.Add(30*time.Second))
	}

	attemptID, _ := ids.ParseAttemptID(testAttemptID)
	attempt, err := store.LoadAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Phase != attemptstate.AttemptPhaseAssigned ||
		attempt.WorkerIdentity != testWorkerID ||
		attempt.LeaseID != lease.ID {
		t.Fatalf("Attempt was not atomically assigned to Lease: %#v", attempt)
	}
}

func TestReserveExactReplayConvergesWithoutNewFence(t *testing.T) {
	fx := newCryptoFixture(t)
	store := readyPlacingState(t, testWorkerID)
	clock := &testClock{now: testNow}
	service := newService(t, fx, store, time.Minute, testPolicyRev, clock)
	raw := fx.signedPlacement()

	first, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: raw, RequestedLifetime: 20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	attemptID, _ := ids.ParseAttemptID(testAttemptID)
	before, err := store.LoadAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatal(err)
	}

	clock.now = testNow.Add(time.Second)
	second, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: raw, RequestedLifetime: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("replay Reserve() error = %v", err)
	}
	after, err := store.LoadAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed ||
		second.Lease.ID != first.Lease.ID ||
		second.Lease.FenceEpoch != first.Lease.FenceEpoch ||
		after.Revision != before.Revision {
		t.Fatalf("replay amplified authority: first=%#v second=%#v before=%#v after=%#v", first, second, before, after)
	}
}

func TestReserveExpiredReplayRequiresNewPlacementDecision(t *testing.T) {
	fx := newCryptoFixture(t)
	store := readyPlacingState(t, testWorkerID)
	clock := &testClock{now: testNow}
	service := newService(t, fx, store, 5*time.Second, testPolicyRev, clock)
	raw := fx.signedPlacement()

	first, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: raw, RequestedLifetime: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Lease.ExpiresAt.Equal(testNow.Add(5 * time.Second)) {
		t.Fatalf("first expiry = %s", first.Lease.ExpiresAt)
	}

	clock.now = testNow.Add(6 * time.Second)
	got, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: raw, RequestedLifetime: 5 * time.Second,
	})
	if !errors.Is(err, leaseissue.ErrPlacementConsumed) {
		t.Fatalf("expired replay error = %v, want ErrPlacementConsumed", err)
	}
	if !got.Replayed || got.Lease.ID != first.Lease.ID || got.Lease.FenceEpoch != first.Lease.FenceEpoch {
		t.Fatalf("expired replay minted or hid authority state: first=%#v got=%#v", first, got)
	}
}

func TestReserveRejectsCurrentPolicyDriftBeforeLeaseMutation(t *testing.T) {
	fx := newCryptoFixture(t)
	store := readyPlacingState(t, testWorkerID)
	clock := &testClock{now: testNow}
	service := newService(t, fx, store, time.Minute, "policy-43", clock)

	_, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: fx.signedPlacement(), RequestedLifetime: time.Minute,
	})
	if err == nil {
		t.Fatal("Reserve() accepted stale Placement policy")
	}

	attemptID, _ := ids.ParseAttemptID(testAttemptID)
	attempt, loadErr := store.LoadAttempt(context.Background(), attemptID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if attempt.Phase != attemptstate.AttemptPhasePlacing || attempt.LeaseID != "" {
		t.Fatalf("policy failure mutated Attempt: %#v", attempt)
	}
}

func TestReserveRejectsAttemptWorkerDriftBeforeLeaseMutation(t *testing.T) {
	fx := newCryptoFixture(t)
	store := readyPlacingState(t, "worker-b")
	clock := &testClock{now: testNow}
	service := newService(t, fx, store, time.Minute, testPolicyRev, clock)

	_, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: fx.signedPlacement(), RequestedLifetime: time.Minute,
	})
	if !errors.Is(err, leaseissue.ErrCurrentState) {
		t.Fatalf("worker drift error = %v, want ErrCurrentState", err)
	}
}

func TestReservePolicyRevokeBetweenReadAndCommitFailsClosed(t *testing.T) {
	fx := newCryptoFixture(t)
	base := readyPlacingState(t, testWorkerID)
	state := &revokeBeforeReserveState{Store: base}
	service, err := leaseissue.New(leaseissue.Config{
		Verifier:      fx.verifier,
		State:         state,
		TrustBundle:   staticTrustBundle{value: fx.bundle},
		Clock:         &testClock{now: testNow},
		MaxLifetime:   time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: fx.signedPlacement(), RequestedLifetime: time.Minute,
	})
	if !errors.Is(err, statestore.ErrPolicyChanged) {
		t.Fatalf("Reserve() error = %v, want ErrPolicyChanged", err)
	}

	attemptID, _ := ids.ParseAttemptID(testAttemptID)
	attempt, loadErr := base.LoadAttempt(context.Background(), attemptID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if attempt.Phase != attemptstate.AttemptPhasePlacing || attempt.LeaseID != "" {
		t.Fatalf("policy race mutated Attempt: %#v", attempt)
	}
	policy, loadErr := base.LoadCurrentPolicy(context.Background())
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if policy.Status != policystate.StatusRevoked || policy.Epoch != 2 {
		t.Fatalf("policy race did not install revoke: %#v", policy)
	}
}

func TestReservePolicyRevokeBlocksExactReplay(t *testing.T) {
	fx := newCryptoFixture(t)
	store := readyPlacingState(t, testWorkerID)
	service := newService(t, fx, store, time.Minute, testPolicyRev, &testClock{now: testNow})
	raw := fx.signedPlacement()

	first, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: raw, RequestedLifetime: 20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.LoadCurrentPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := policystate.Revoke(current, current.Epoch+1, testNow.Add(time.Second), "emergency revoke")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), current.Epoch, revoked); err != nil {
		t.Fatal(err)
	}

	_, err = service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: raw, RequestedLifetime: 20 * time.Second,
	})
	if !errors.Is(err, statestore.ErrPolicyRevoked) {
		t.Fatalf("replay after revoke error = %v, want ErrPolicyRevoked", err)
	}
	got, err := store.LoadLease(context.Background(), first.Lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.Lease.ID || got.FenceEpoch != first.Lease.FenceEpoch {
		t.Fatalf("revoke changed durable Lease history: %#v", got)
	}
}

func TestReserveUsesCurrentTrustBundleOnEveryCall(t *testing.T) {
	fx := newCryptoFixture(t)
	store := readyPlacingState(t, testWorkerID)
	service, err := leaseissue.New(leaseissue.Config{
		Verifier: fx.verifier,
		State: store,
		TrustBundle: staticTrustBundle{value: decisiontrust.TrustBundleMetadata{
			Revision: 4,
			ValidatedAt: testNow.Add(-2 * time.Hour),
		}},
		Clock:       &testClock{now: testNow},
		MaxLifetime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: fx.signedPlacement(), RequestedLifetime: time.Minute,
	}); err == nil {
		t.Fatal("Reserve() accepted a stale current Trust Bundle")
	}
}

func TestReserveCannotUseCallerTimeToReviveExpiredPlacement(t *testing.T) {
	fx := newCryptoFixture(t)
	store := readyPlacingState(t, testWorkerID)
	clock := &testClock{now: testExpires.Add(time.Second)}
	service := newService(t, fx, store, time.Minute, testPolicyRev, clock)

	if _, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: fx.signedPlacement(), RequestedLifetime: time.Minute,
	}); err == nil {
		t.Fatal("Reserve() accepted an expired Placement under the trusted Clock")
	}

	attemptID, _ := ids.ParseAttemptID(testAttemptID)
	attempt, err := store.LoadAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Phase != attemptstate.AttemptPhasePlacing || attempt.LeaseID != "" {
		t.Fatalf("expired Placement mutated Attempt: %#v", attempt)
	}
}

func TestReserveReverifiesKeyStateInsteadOfTrustingOldPlacementBytes(t *testing.T) {
	fx := newCryptoFixture(t)
	raw := fx.signedPlacement()
	if err := fx.registry.MarkCompromised(testKeyID, testNow); err != nil {
		t.Fatal(err)
	}

	store := readyPlacingState(t, testWorkerID)
	clock := &testClock{now: testNow}
	service := newService(t, fx, store, time.Minute, testPolicyRev, clock)

	if _, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: raw, RequestedLifetime: time.Minute,
	}); err == nil {
		t.Fatal("Reserve() trusted Placement bytes after the signing key became compromised")
	}

	attemptID, _ := ids.ParseAttemptID(testAttemptID)
	attempt, err := store.LoadAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Phase != attemptstate.AttemptPhasePlacing || attempt.LeaseID != "" {
		t.Fatalf("compromised-key failure mutated Attempt: %#v", attempt)
	}
}

func TestReserveCapsLeaseAtPlacementExpiry(t *testing.T) {
	fx := newCryptoFixture(t)
	store := readyPlacingState(t, testWorkerID)
	clock := &testClock{now: testNow}
	service := newService(t, fx, store, 10*time.Minute, testPolicyRev, clock)

	result, err := service.Reserve(context.Background(), leaseissue.Request{
		SignedPlacement: fx.signedPlacement(),
		RequestedLifetime: 10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Lease.ExpiresAt.Equal(testExpires) {
		t.Fatalf("Lease expiry = %s, want Placement expiry %s", result.Lease.ExpiresAt, testExpires)
	}
}

func newService(t *testing.T, fx *cryptoFixture, store *memory.Store, maxLifetime time.Duration, policy string, clock *testClock) *leaseissue.Service {
	t.Helper()
	setPolicy(t, store, policy)
	service, err := leaseissue.New(leaseissue.Config{
		Verifier:      fx.verifier,
		State:         store,
		TrustBundle:   staticTrustBundle{value: fx.bundle},
		Clock:         clock,
		MaxLifetime:   maxLifetime,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func setPolicy(t *testing.T, store statestore.PolicyStore, revision string) policystate.State {
	t.Helper()
	current, err := store.LoadCurrentPolicy(context.Background())
	if errors.Is(err, statestore.ErrPolicyUnavailable) {
		initial, err := policystate.NewActive(1, revision, testIssuedAt.Add(-2*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), 0, initial); err != nil {
			t.Fatal(err)
		}
		return initial
	}
	if err != nil {
		t.Fatal(err)
	}
	if current.Status == policystate.StatusActive && current.Revision == revision {
		return current
	}
	activatedAt := current.ActivatedAt.Add(time.Second)
	if current.Status == policystate.StatusRevoked && !current.RevokedAt.Before(activatedAt) {
		activatedAt = current.RevokedAt.Add(time.Second)
	}
	next, err := policystate.NewActive(current.Epoch+1, revision, activatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), current.Epoch, next); err != nil {
		t.Fatal(err)
	}
	return next
}

type revokeBeforeReserveState struct {
	*memory.Store
	done bool
	err  error
}

func (s *revokeBeforeReserveState) ReserveLease(ctx context.Context, reservation statestore.LeaseReservation) (statestore.LeaseReservationResult, error) {
	if !s.done {
		s.done = true
		current, err := s.LoadCurrentPolicy(ctx)
		if err != nil {
			s.err = err
		} else {
			revoked, revokeErr := policystate.Revoke(current, current.Epoch+1, testNow, "revoke between read and commit")
			if revokeErr != nil {
				s.err = revokeErr
			} else {
				s.err = s.CompareAndSwapPolicy(ctx, current.Epoch, revoked)
			}
		}
	}
	if s.err != nil {
		return statestore.LeaseReservationResult{}, s.err
	}
	return s.Store.ReserveLease(ctx, reservation)
}

func readyPlacingState(t *testing.T, worker string) *memory.Store {
	t.Helper()
	store := memory.New()
	setPolicy(t, store, testPolicyRev)
	taskID, err := ids.ParseTaskID(testTaskID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := ids.ParseAttemptID(testAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	taskDigest := contract.SHA256(testTaskIR)
	task, err := taskstate.NewTask(taskID, taskDigest, testIssuedAt.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = task.UpdatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(context.Background(), task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}

	attempt, err := attemptstate.New(attemptID, taskID, taskDigest, 1, testIssuedAt.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	placing := attempt
	placing.Phase = attemptstate.AttemptPhasePlacing
	placing.WorkerIdentity = worker
	placing.Revision++
	placing.UpdatedAt = attempt.UpdatedAt.Add(time.Second)
	if err := store.CompareAndSwapAttempt(context.Background(), attempt.ID, attempt.Revision, placing); err != nil {
		t.Fatal(err)
	}
	return store
}
