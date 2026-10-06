package placementverify_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/placementverify"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

const (
	trustDomain = "local.example"
	principal   = "control-plane/plc-1"
	keyID       = "kid-plc-1"
	taskID      = "tsk_00000000000000000000000000000001"
	attemptID   = "atm_00000000000000000000000000000001"
	workerID    = "worker-a"
	policyRev   = "policy-42"
)

var (
	issuedAt  = time.UnixMilli(1_780_000_000_000).UTC()
	expiresAt = issuedAt.Add(2 * time.Minute)
	now       = issuedAt.Add(10 * time.Second)
	taskIR    = bytes.Repeat([]byte{0x22}, 32)
)

// fixture is a complete signing and verification setup built from the real
// adapters, so these tests exercise the actual composition.
type fixture struct {
	t        *testing.T
	registry *decisiontrust.Registry
	signer   *decisiontrust.ReferenceSoftwareSigner
	policy   decisiontrust.VerificationPolicy
	codec    *cosecbor.Codec
	payload  *payloadcbor.Decoder
	enc      fxcbor.EncMode
	bundle   decisiontrust.TrustBundleMetadata
}

type keyOptions struct {
	role  authority.Role
	skip  bool // do not register the key at all
	state string
}

func newFixture(t *testing.T, opts ...func(*keyOptions)) *fixture {
	t.Helper()
	ko := keyOptions{role: authority.RolePlacementAuthorization}
	for _, o := range opts {
		o(&ko)
	}

	codec, err := cosecbor.New()
	if err != nil {
		t.Fatalf("cosecbor.New() error = %v", err)
	}
	payload, err := payloadcbor.New()
	if err != nil {
		t.Fatalf("payloadcbor.New() error = %v", err)
	}
	enc, err := fxcbor.CoreDetEncOptions().EncMode()
	if err != nil {
		t.Fatalf("EncMode() error = %v", err)
	}

	registry := decisiontrust.NewRegistry(issuedAt.Add(-time.Hour))
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 9
	private := ed25519.NewKeyFromSeed(seed)

	record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
		ID:                 keyID,
		AuthorityRole:      ko.role,
		AuthorityPrincipal: principal,
		TrustDomain:        trustDomain,
		Algorithm:          decisiontrust.AlgorithmEdDSA,
		KeyType:            decisiontrust.KeyTypeOKP,
		Curve:              decisiontrust.CurveEd25519,
		PublicKey:          private.Public(),
		SigningNotBefore:   issuedAt.Add(-time.Hour),
		SigningNotAfter:    issuedAt.Add(24 * time.Hour),
		PolicyRevision:     "key-policy-1",
	}, issuedAt.Add(-time.Hour))
	if err != nil {
		t.Fatalf("NewKeyRecord() error = %v", err)
	}
	if !ko.skip {
		if err := registry.Register(record, issuedAt.Add(-time.Hour)); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
		if err := registry.Activate(record.ID(), issuedAt.Add(-time.Hour)); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}
		if err := registry.SetPrimary(record.ID(), issuedAt.Add(-time.Hour)); err != nil {
			t.Fatalf("SetPrimary() error = %v", err)
		}
	}

	signer, err := decisiontrust.NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatalf("NewReferenceSoftwareSigner() error = %v", err)
	}
	if !ko.skip {
		if err := signer.RegisterPrivateKey(record.ID(), private); err != nil {
			t.Fatalf("RegisterPrivateKey() error = %v", err)
		}
	}

	policy, err := decisiontrust.NewVerificationPolicy(decisiontrust.VerificationPolicyConfig{
		MaxClockSkew:            5 * time.Second,
		MaxTrustBundleStaleness: time.Hour,
		AllowedAlgorithms:       []decisiontrust.Algorithm{decisiontrust.AlgorithmEdDSA},
		MaxDecisionLifetime:     map[authority.DecisionKind]time.Duration{authority.DecisionPlacementApproval: 5 * time.Minute},
	})
	if err != nil {
		t.Fatalf("NewVerificationPolicy() error = %v", err)
	}

	return &fixture{
		t: t, registry: registry, signer: signer, policy: policy,
		codec: codec, payload: payload, enc: enc,
		bundle: decisiontrust.TrustBundleMetadata{Revision: 3, ValidatedAt: now.Add(-time.Minute)},
	}
}

func (f *fixture) verifier() *placementverify.Verifier {
	f.t.Helper()
	boundary, err := decisiontrust.NewVerifierBoundary(f.registry, f.policy)
	if err != nil {
		f.t.Fatalf("NewVerifierBoundary() error = %v", err)
	}
	return f.verifierWith(boundary, trustDomain)
}

func (f *fixture) verifierWith(boundary *decisiontrust.VerifierBoundary, domain string) *placementverify.Verifier {
	f.t.Helper()
	v, err := placementverify.New(placementverify.Config{
		Codec: f.codec, Payload: f.payload, Boundary: boundary, TrustDomain: domain,
	})
	if err != nil {
		f.t.Fatalf("placementverify.New() error = %v", err)
	}
	return v
}

func (f *fixture) must(v []byte, err error) []byte {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}

type payloadOptions struct {
	worker    string
	issued    time.Time
	expires   time.Time
	principal string
}

func (f *fixture) payloadBytes(o payloadOptions) []byte {
	f.t.Helper()
	if o.worker == "" {
		o.worker = workerID
	}
	if o.issued.IsZero() {
		o.issued = issuedAt
	}
	if o.expires.IsZero() {
		o.expires = expiresAt
	}
	if o.principal == "" {
		o.principal = principal
	}
	return f.must(f.enc.Marshal(map[uint64]any{
		1: uint64(1),
		2: string(authority.DecisionPlacementApproval),
		3: "plc-decision-001",
		4: map[uint64]any{1: string(authority.RolePlacementAuthorization), 2: o.principal},
		5: uint64(o.issued.UnixMilli()),
		6: uint64(o.expires.UnixMilli()),
		7: policyRev,
		8: map[uint64]any{
			1: taskID,
			2: map[uint64]any{1: uint64(1), 2: taskIR},
			3: attemptID,
			4: o.worker,
		},
	}))
}

func (f *fixture) protectedBytes() []byte {
	f.t.Helper()
	return f.must(f.enc.Marshal(map[int64]any{
		1:      int64(-8),
		2:      []int64{decisiontrust.COSEHeaderLabelEKIProfile},
		4:      []byte(keyID),
		-70000: decisiontrust.SignedDecisionProfileV1,
	}))
}

// sign produces a COSE_Sign1 over payload, signed by the fixture key.
func (f *fixture) sign(payload []byte) []byte {
	f.t.Helper()
	protected := f.protectedBytes()
	sigStructure, err := f.codec.EncodeSignature1(protected, payload)
	if err != nil {
		f.t.Fatalf("EncodeSignature1() error = %v", err)
	}
	result, err := f.signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key: decisiontrust.SigningRequest{
			TrustDomain:        trustDomain,
			AuthorityRole:      authority.RolePlacementAuthorization,
			AuthorityPrincipal: principal,
			DecisionKind:       authority.DecisionPlacementApproval,
			Algorithm:          decisiontrust.AlgorithmEdDSA,
		},
		PolicyRevision: "key-policy-1",
		SigStructure:   sigStructure,
	}, issuedAt)
	if err != nil {
		f.t.Fatalf("SignCOSESigStructure() error = %v", err)
	}
	return f.envelope(protected, payload, result.Signature)
}

func (f *fixture) envelope(protected, payload, signature []byte) []byte {
	f.t.Helper()
	out := []byte{0xd2, 0x84}
	out = append(out, f.must(f.enc.Marshal(protected))...)
	out = append(out, 0xa0)
	out = append(out, f.must(f.enc.Marshal(payload))...)
	out = append(out, f.must(f.enc.Marshal(signature))...)
	return out
}

func (f *fixture) request(raw []byte) placementverify.Request {
	return placementverify.Request{Raw: raw, Now: now, TrustBundle: f.bundle}
}

func wantReason(t *testing.T, err error, want decisiontrust.VerificationReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("succeeded, want failure %s", want)
	}
	got, ok := decisiontrust.FailureReason(err)
	if !ok {
		t.Fatalf("error %v is not a VerificationFailure", err)
	}
	if got != want {
		t.Fatalf("reason = %s, want %s (err=%v)", got, want, err)
	}
}

func TestVerifyAcceptsAValidSignedApproval(t *testing.T) {
	f := newFixture(t)
	got, err := f.verifier().Verify(f.request(f.sign(f.payloadBytes(payloadOptions{}))))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got.IsZero() {
		t.Fatal("Verify() returned the zero Verified without an error")
	}
	if got.KeyID() != keyID || got.TrustBundleRevision() != 3 || !got.VerifiedAt().Equal(now) {
		t.Fatalf("unexpected Verified: key=%s bundle=%d at=%s", got.KeyID(), got.TrustBundleRevision(), got.VerifiedAt())
	}
	if got.Approval().TaskID().String() != taskID || got.Approval().WorkerIdentity() != workerID {
		t.Fatalf("approval did not carry the signed subject: %#v", got.Approval())
	}
}

func TestVerifyRejectsATamperedPayload(t *testing.T) {
	f := newFixture(t)
	signed := f.sign(f.payloadBytes(payloadOptions{}))

	// Swap the Worker in the signed bytes for another valid identity of the
	// same length. The payload still decodes and is still deterministic, so
	// only the signature can catch it.
	tampered := bytes.Replace(signed, []byte("worker-a"), []byte("worker-b"), 1)
	if bytes.Equal(tampered, signed) {
		t.Fatal("test bug: payload was not changed")
	}
	_, err := f.verifier().Verify(f.request(tampered))
	wantReason(t, err, decisiontrust.ReasonSignatureInvalid)
}

func TestVerifyRejectsASignatureFromAnotherKey(t *testing.T) {
	f := newFixture(t)
	payload := f.payloadBytes(payloadOptions{})
	protected := f.protectedBytes()

	sigStructure, err := f.codec.EncodeSignature1(protected, payload)
	if err != nil {
		t.Fatalf("EncodeSignature1() error = %v", err)
	}
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 77
	forged := ed25519.Sign(ed25519.NewKeyFromSeed(seed), sigStructure)

	_, err = f.verifier().Verify(f.request(f.envelope(protected, payload, forged)))
	wantReason(t, err, decisiontrust.ReasonSignatureInvalid)
}

func TestVerifyFailsClosedAtEachEarlierStage(t *testing.T) {
	f := newFixture(t)
	valid := f.sign(f.payloadBytes(payloadOptions{}))
	v := f.verifier()

	t.Run("empty input", func(t *testing.T) {
		_, err := v.Verify(f.request(nil))
		wantReason(t, err, decisiontrust.ReasonMalformedCOSE)
	})
	t.Run("not tagged", func(t *testing.T) {
		_, err := v.Verify(f.request(valid[1:]))
		wantReason(t, err, decisiontrust.ReasonMalformedCOSE)
	})
	t.Run("zero time", func(t *testing.T) {
		req := f.request(valid)
		req.Now = time.Time{}
		_, err := v.Verify(req)
		wantReason(t, err, decisiontrust.ReasonTimeWindowInvalid)
	})
	t.Run("payload that is a valid map but not a Placement Approval", func(t *testing.T) {
		other := f.must(f.enc.Marshal(map[uint64]any{1: uint64(1), 2: "lease"}))
		_, err := v.Verify(f.request(f.sign(other)))
		wantReason(t, err, decisiontrust.ReasonSchemaUnsupported)
	})
}

func TestVerifyEnforcesTimeAndTrustBundlePolicy(t *testing.T) {
	f := newFixture(t)
	valid := f.sign(f.payloadBytes(payloadOptions{}))
	v := f.verifier()

	t.Run("expired", func(t *testing.T) {
		req := f.request(valid)
		req.Now = expiresAt.Add(time.Minute)
		req.TrustBundle.ValidatedAt = req.Now.Add(-time.Minute)
		_, err := v.Verify(req)
		wantReason(t, err, decisiontrust.ReasonExpired)
	})
	t.Run("not yet valid", func(t *testing.T) {
		req := f.request(valid)
		req.Now = issuedAt.Add(-time.Minute)
		req.TrustBundle.ValidatedAt = req.Now.Add(-time.Minute)
		_, err := v.Verify(req)
		wantReason(t, err, decisiontrust.ReasonNotYetValid)
	})
	t.Run("stale trust bundle", func(t *testing.T) {
		req := f.request(valid)
		req.TrustBundle.ValidatedAt = now.Add(-3 * time.Hour)
		_, err := v.Verify(req)
		wantReason(t, err, decisiontrust.ReasonTrustBundleStale)
	})
	t.Run("zero trust bundle", func(t *testing.T) {
		req := f.request(valid)
		req.TrustBundle = decisiontrust.TrustBundleMetadata{}
		_, err := v.Verify(req)
		wantReason(t, err, decisiontrust.ReasonTrustBundleInvalid)
	})
	t.Run("lifetime over policy", func(t *testing.T) {
		long := f.sign(f.payloadBytes(payloadOptions{expires: issuedAt.Add(time.Hour)}))
		_, err := v.Verify(f.request(long))
		wantReason(t, err, decisiontrust.ReasonStaleDecision)
	})
}

func TestVerifyBindsTheKeyToTheClaimedAuthority(t *testing.T) {
	t.Run("unknown key", func(t *testing.T) {
		f := newFixture(t, func(o *keyOptions) { o.skip = true })
		// Sign with a key the registry has never seen: build the envelope by
		// hand because the fixture signer has no key either.
		payload := f.payloadBytes(payloadOptions{})
		raw := f.envelope(f.protectedBytes(), payload, make([]byte, 64))
		_, err := f.verifier().Verify(f.request(raw))
		wantReason(t, err, decisiontrust.ReasonUnknownKey)
	})

	t.Run("key held by a different role", func(t *testing.T) {
		f := newFixture(t, func(o *keyOptions) { o.role = authority.RoleLease })
		payload := f.payloadBytes(payloadOptions{})
		// Signing as PLC-1 is refused for a LSE-1 key, so use a placeholder
		// signature; the role mismatch must be caught before crypto.
		raw := f.envelope(f.protectedBytes(), payload, make([]byte, 64))
		_, err := f.verifier().Verify(f.request(raw))
		if err == nil {
			t.Fatal("a Lease-role key vouched for a Placement Approval")
		}
		if reason, _ := decisiontrust.FailureReason(err); reason == decisiontrust.ReasonSignatureInvalid {
			t.Fatalf("role mismatch was only caught at the signature stage: %v", err)
		}
	})

	t.Run("payload names a different principal than the key", func(t *testing.T) {
		f := newFixture(t)
		// The signature is valid for the key, but the payload claims another
		// authority principal. The registry record must not match.
		raw := f.sign(f.payloadBytes(payloadOptions{principal: "control-plane/other"}))
		_, err := f.verifier().Verify(f.request(raw))
		if err == nil {
			t.Fatal("a key vouched for a principal it does not belong to")
		}
		if reason, _ := decisiontrust.FailureReason(err); reason == decisiontrust.ReasonSignatureInvalid {
			t.Fatalf("principal mismatch was only caught at the signature stage: %v", err)
		}
	})

	t.Run("relying trust domain differs from the key", func(t *testing.T) {
		f := newFixture(t)
		raw := f.sign(f.payloadBytes(payloadOptions{}))
		boundary, err := decisiontrust.NewVerifierBoundary(f.registry, f.policy)
		if err != nil {
			t.Fatalf("NewVerifierBoundary() error = %v", err)
		}
		_, err = f.verifierWith(boundary, "other.example").Verify(f.request(raw))
		wantReason(t, err, decisiontrust.ReasonSubjectMismatch)
	})

	t.Run("revoked key", func(t *testing.T) {
		f := newFixture(t)
		raw := f.sign(f.payloadBytes(payloadOptions{}))
		if err := f.registry.Revoke(keyID, issuedAt.Add(time.Second)); err != nil {
			t.Fatalf("Revoke() error = %v", err)
		}
		_, err := f.verifier().Verify(f.request(raw))
		if err == nil {
			t.Fatal("a revoked key vouched for a decision issued after revocation")
		}
	})

	t.Run("compromised key", func(t *testing.T) {
		f := newFixture(t)
		raw := f.sign(f.payloadBytes(payloadOptions{}))
		if err := f.registry.MarkCompromised(keyID, issuedAt.Add(-time.Second)); err != nil {
			t.Fatalf("MarkCompromised() error = %v", err)
		}
		_, err := f.verifier().Verify(f.request(raw))
		if err == nil {
			t.Fatal("a compromised key vouched for a decision")
		}
	})
}

func TestNewRequiresEveryDependency(t *testing.T) {
	f := newFixture(t)
	boundary, err := decisiontrust.NewVerifierBoundary(f.registry, f.policy)
	if err != nil {
		t.Fatalf("NewVerifierBoundary() error = %v", err)
	}
	full := placementverify.Config{Codec: f.codec, Payload: f.payload, Boundary: boundary, TrustDomain: trustDomain}

	for name, mutate := range map[string]func(*placementverify.Config){
		"codec":        func(c *placementverify.Config) { c.Codec = nil },
		"payload":      func(c *placementverify.Config) { c.Payload = nil },
		"boundary":     func(c *placementverify.Config) { c.Boundary = nil },
		"trust domain": func(c *placementverify.Config) { c.TrustDomain = "" },
	} {
		cfg := full
		mutate(&cfg)
		if _, err := placementverify.New(cfg); !errors.Is(err, placementverify.ErrInvalidConfig) {
			t.Errorf("%s: New() error = %v, want ErrInvalidConfig", name, err)
		}
	}
	if _, err := placementverify.New(full); err != nil {
		t.Fatalf("New(full) error = %v", err)
	}
}

func TestNilVerifierFailsClosed(t *testing.T) {
	var v *placementverify.Verifier
	if _, err := v.Verify(placementverify.Request{Raw: []byte{1}, Now: now}); err == nil {
		t.Fatal("nil Verifier verified something")
	}
}

// --- Revalidate ---

func verified(t *testing.T) placementverify.Verified {
	t.Helper()
	f := newFixture(t)
	got, err := f.verifier().Verify(f.request(f.sign(f.payloadBytes(payloadOptions{}))))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	return got
}

func currentState(t *testing.T) placementverify.Current {
	t.Helper()
	digest, err := contract.ParseSHA256(hex.EncodeToString(taskIR))
	if err != nil {
		t.Fatalf("ParseSHA256() error = %v", err)
	}
	record, err := taskstate.NewTask(ids.TaskID(taskID), digest, issuedAt)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	record.Phase = taskstate.TaskPhaseReady
	record.Revision = 2
	return placementverify.Current{
		Task:           record,
		AttemptID:      ids.AttemptID(attemptID),
		WorkerIdentity: workerID,
		PolicyRevision: policyRev,
		Now:            now,
	}
}

func TestRevalidateAcceptsMatchingCurrentState(t *testing.T) {
	if err := verified(t).Revalidate(currentState(t)); err != nil {
		t.Fatalf("Revalidate() error = %v", err)
	}
}

func TestRevalidateAcceptsAnActiveTask(t *testing.T) {
	cur := currentState(t)
	cur.Task.Phase = taskstate.TaskPhaseActive
	if err := verified(t).Revalidate(cur); err != nil {
		t.Fatalf("Revalidate() error = %v", err)
	}
}

func TestRevalidateRejectsAnyChangedBinding(t *testing.T) {
	v := verified(t)
	otherDigest := contract.SHA256([]byte("a different task"))

	cases := []struct {
		name   string
		mutate func(*placementverify.Current)
		want   decisiontrust.VerificationReason
	}{
		{"different task", func(c *placementverify.Current) { c.Task.ID = "tsk_00000000000000000000000000000002" }, decisiontrust.ReasonSubjectMismatch},
		{"task IR changed", func(c *placementverify.Current) { c.Task.TaskIRDigest = otherDigest }, decisiontrust.ReasonSubjectMismatch},
		{"different attempt", func(c *placementverify.Current) { c.AttemptID = "atm_00000000000000000000000000000002" }, decisiontrust.ReasonSubjectMismatch},
		{"different worker", func(c *placementverify.Current) { c.WorkerIdentity = "worker-b" }, decisiontrust.ReasonSubjectMismatch},
		{"policy revision moved on", func(c *placementverify.Current) { c.PolicyRevision = "policy-43" }, decisiontrust.ReasonPolicyRevoked},
		{"task is terminal", func(c *placementverify.Current) {
			c.Task.Phase = taskstate.TaskPhaseTerminal
			c.Task.TerminalOutcome = taskstate.TerminalOutcomeCancelled
		}, decisiontrust.ReasonStaleDecision},
		{"task is held", func(c *placementverify.Current) { c.Task.Phase = taskstate.TaskPhaseHeld }, decisiontrust.ReasonStaleDecision},
		{"task is admitted only", func(c *placementverify.Current) { c.Task.Phase = taskstate.TaskPhaseAdmitted }, decisiontrust.ReasonStaleDecision},
		{"cancellation requested", func(c *placementverify.Current) { c.Task.CancellationRequested = true }, decisiontrust.ReasonStaleDecision},
		{"approval expired by now", func(c *placementverify.Current) { c.Now = expiresAt }, decisiontrust.ReasonExpired},
		{"approval not yet valid", func(c *placementverify.Current) { c.Now = issuedAt.Add(-time.Second) }, decisiontrust.ReasonNotYetValid},
		{"zero time", func(c *placementverify.Current) { c.Now = time.Time{} }, decisiontrust.ReasonTimeWindowInvalid},
		{"invalid task record", func(c *placementverify.Current) { c.Task = taskstate.TaskRecord{} }, decisiontrust.ReasonSubjectMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur := currentState(t)
			tc.mutate(&cur)
			wantReason(t, v.Revalidate(cur), tc.want)
		})
	}
}

func TestZeroVerifiedCannotBeRevalidated(t *testing.T) {
	var zero placementverify.Verified
	if !zero.IsZero() {
		t.Fatal("zero Verified reports it is not zero")
	}
	wantReason(t, zero.Revalidate(currentState(t)), decisiontrust.ReasonSchemaUnsupported)
}
