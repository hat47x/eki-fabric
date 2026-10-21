package statestoretest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/leaseverify"
)

// Exercise the actual LSE-1 producer and Worker verifier against every Store
// adapter. These checks authenticate bytes; they never execute a Task.
func runLeaseVerificationContract(t *testing.T, newStore LeaseEmissionFactory) {
	t.Helper()
	t.Run("emitted-bytes-retain-all-bindings", func(t *testing.T) {
		fx := newSigningFixture(t, newStore(t))
		reserved := fx.reserve("worker-verify-positive")
		emitted, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if err != nil {
			t.Fatal(err)
		}
		verifier, err := leaseverify.New(fx.workerVerifierConfig(reserved.WorkerIdentity))
		if err != nil {
			t.Fatal(err)
		}
		got, err := verifier.Verify(leaseverify.Request{Raw: emitted.SignedLease, Now: fx.clock.now, TrustBundle: fx.bundle.get()})
		if err != nil {
			t.Fatal(err)
		}
		lease := got.Lease()
		if got.IsZero() || lease.LeaseID() != reserved.ID || lease.TaskID() != reserved.TaskID ||
			lease.TaskIRDigest() != reserved.TaskIRDigest || lease.AttemptID() != reserved.AttemptID ||
			lease.WorkerIdentity() != reserved.WorkerIdentity || lease.FenceEpoch() != reserved.FenceEpoch ||
			lease.RenewalSequence() != reserved.RenewalSequence || lease.PolicyRevision() != reserved.PolicyRevision ||
			got.KeyID() != signingKeyID || got.TrustDomain() != signingTrustDomain ||
			got.TrustBundleRevision() != fx.bundle.get().Revision || !got.VerifiedAt().Equal(fx.clock.now) {
			t.Fatalf("Worker verification lost signed binding: %#v", got)
		}
		// Keep the independently composed decoder/crypto check as an oracle.
		if !lease.Equal(fx.verifyAsWorker(emitted.SignedLease)) {
			t.Fatal("independent verification differs")
		}
	})

	for _, name := range []string{"malformed", "signature-tampered", "payload-tampered", "other-worker", "other-domain", "key-revoked", "key-compromised", "expired", "not-yet-valid", "stale-bundle", "missing-clock", "invalid-bundle", "algorithm-replaced", "kid-replaced", "non-lease-kind", "expiry-at-skew-boundary", "issued-before-skew-boundary"} {
		t.Run(name, func(t *testing.T) {
			fx := newSigningFixture(t, newStore(t))
			reserved := fx.reserve("worker-verify-" + name)
			emitted, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
			if err != nil {
				t.Fatal(err)
			}
			cfg := fx.workerVerifierConfig(reserved.WorkerIdentity)
			req := leaseverify.Request{Raw: append([]byte(nil), emitted.SignedLease...), Now: fx.clock.now, TrustBundle: fx.bundle.get()}
			want := decisiontrust.ReasonSignatureInvalid
			switch name {
			case "malformed":
				req.Raw = []byte{0xff}
				want = decisiontrust.ReasonMalformedCOSE
			case "signature-tampered":
				req.Raw[len(req.Raw)-1] ^= 1
			case "payload-tampered":
				envelope, err := fx.codec.DecodeSign1(req.Raw)
				if err != nil {
					t.Fatal(err)
				}
				payload := envelope.Payload()
				// Decode and re-encode valid closed CBOR with a different Task IR.
				// Reusing the signature must not authenticate the replacement.
				for i := 0; i+2 < len(payload); i++ {
					if payload[i] == 0x58 && payload[i+1] == 0x20 {
						payload[i+2] ^= 1
						break
					}
				}
				if _, err := fx.payload.DecodeLease(payload); err != nil {
					t.Fatal(err)
				}
				req.Raw, err = fx.codec.EncodeSign1(envelope.ProtectedHeader(), payload, envelope.Signature())
				if err != nil {
					t.Fatal(err)
				}
			case "other-worker":
				cfg.WorkerIdentity = "another-worker"
				want = decisiontrust.ReasonSubjectMismatch
			case "other-domain":
				cfg.TrustDomain = "another.example"
				want = decisiontrust.ReasonSubjectMismatch
			case "key-revoked", "key-compromised":
				if name == "key-revoked" {
					err = fx.registry.Revoke(signingKeyID, fx.clock.now)
				} else {
					err = fx.registry.MarkCompromised(signingKeyID, fx.clock.now)
				}
				if err != nil {
					t.Fatal(err)
				}
				want = decisiontrust.ReasonKeyNotActiveForVerify
			case "expired":
				req.Now = reserved.ExpiresAt.Add(time.Minute)
				want = decisiontrust.ReasonExpired
			case "not-yet-valid":
				req.Now = reserved.IssuedAt.Add(-time.Minute)
				req.TrustBundle.ValidatedAt = req.Now
				want = decisiontrust.ReasonNotYetValid
			case "stale-bundle":
				req.TrustBundle.ValidatedAt = req.Now.Add(-2 * time.Hour)
				want = decisiontrust.ReasonTrustBundleStale
			case "missing-clock":
				req.Now = time.Time{}
				want = decisiontrust.ReasonTimeWindowInvalid
			case "invalid-bundle":
				req.TrustBundle = decisiontrust.TrustBundleMetadata{}
				want = decisiontrust.ReasonTrustBundleInvalid
			case "algorithm-replaced", "kid-replaced", "non-lease-kind":
				envelope, err := fx.codec.DecodeSign1(req.Raw)
				if err != nil {
					t.Fatal(err)
				}
				protected, payload, signature := envelope.ProtectedHeader(), envelope.Payload(), envelope.Signature()
				switch name {
				case "algorithm-replaced":
					protected, err = fx.codec.EncodeProtectedHeader(decisiontrust.AlgorithmES256, signingKeyID)
					want = decisiontrust.ReasonAlgorithmNotAllowed
				case "kid-replaced":
					protected, err = fx.codec.EncodeProtectedHeader(decisiontrust.AlgorithmEdDSA, "unknown-key")
					want = decisiontrust.ReasonUnknownKey
				case "non-lease-kind":
					// Re-sign a payload naming another kind with the fixture's
					// Lease key. Even an authentic envelope cannot change the
					// expected closed payload type at the Worker boundary.
					var fields map[uint64]fxcbor.RawMessage
					if err = fxcbor.Unmarshal(payload, &fields); err != nil {
						t.Fatal(err)
					}
					enc, encErr := fxcbor.CoreDetEncOptions().EncMode()
					if encErr != nil {
						t.Fatal(encErr)
					}
					fields[2], err = enc.Marshal(string(authority.DecisionPlacementApproval))
					if err != nil {
						t.Fatal(err)
					}
					payload, err = enc.Marshal(fields)
					if err != nil {
						t.Fatal(err)
					}
					sigStructure, sigErr := fx.codec.EncodeSignature1(protected, payload)
					if sigErr != nil {
						t.Fatal(sigErr)
					}
					signed, signErr := fx.signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
						Key: decisiontrust.SigningRequest{TrustDomain: signingTrustDomain, AuthorityRole: authority.RoleLease,
							AuthorityPrincipal: signingPrincipal, DecisionKind: authority.DecisionLease, Algorithm: decisiontrust.AlgorithmEdDSA},
						PolicyRevision: signingKeyPolicy, SigStructure: sigStructure,
					}, fx.clock.now)
					if signErr != nil {
						t.Fatal(signErr)
					}
					signature = signed.Signature
					want = decisiontrust.ReasonSchemaUnsupported
				}
				if err != nil {
					t.Fatal(err)
				}
				req.Raw, err = fx.codec.EncodeSign1(protected, payload, signature)
				if err != nil {
					t.Fatal(err)
				}
			case "expiry-at-skew-boundary":
				lease := fx.verifyAsWorker(req.Raw)
				req.Now = lease.ExpiresAt().Add(5 * time.Second)
				want = decisiontrust.ReasonExpired
			case "issued-before-skew-boundary":
				lease := fx.verifyAsWorker(req.Raw)
				req.Now = lease.IssuedAt().Add(-5*time.Second - time.Nanosecond)
				req.TrustBundle.ValidatedAt = req.Now
				want = decisiontrust.ReasonNotYetValid
			}
			verifier, err := leaseverify.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := verifier.Verify(req)
			reason, ok := decisiontrust.FailureReason(err)
			if !got.IsZero() || !ok || reason != want {
				t.Fatalf("Verify = %#v, %v; want zero and %s", got, err, want)
			}
		})
	}

	t.Run("verify-only-replay-is-an-observation-not-a-new-grant", func(t *testing.T) {
		fx := newSigningFixture(t, newStore(t))
		reserved := fx.reserve("worker-verify-only-replay")
		emitted, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.registry.SetVerifyOnly(signingKeyID, fx.clock.now); err != nil {
			t.Fatal(err)
		}
		verifier, err := leaseverify.New(fx.workerVerifierConfig(reserved.WorkerIdentity))
		if err != nil {
			t.Fatal(err)
		}
		req := leaseverify.Request{Raw: emitted.SignedLease, Now: fx.clock.now, TrustBundle: fx.bundle.get()}
		first, err := verifier.Verify(req)
		if err != nil || first.IsZero() {
			t.Fatalf("first observation = %#v, %v", first, err)
		}
		req.Now = req.Now.Add(time.Nanosecond)
		second, err := verifier.Verify(req)
		if err != nil || second.IsZero() || !second.Lease().Equal(first.Lease()) || !second.VerifiedAt().Equal(req.Now) || !first.VerifiedAt().Equal(fx.clock.now) {
			t.Fatalf("repeated observation = %#v, %v", second, err)
		}
		if fx.signer.calls.Load() != 1 {
			t.Fatal("verification signed another Lease")
		}
		stored, err := fx.store.LoadLeaseEmission(context.Background(), reserved.ID)
		if err != nil || !reflect.DeepEqual(stored, emitted.Record) {
			t.Fatalf("verification changed durable emission: %#v, %v", stored, err)
		}
		// A previous Verified remains a historical observation. It cannot
		// replace a fresh check after the trusted key registry changes.
		if err := fx.registry.Revoke(signingKeyID, req.Now); err != nil {
			t.Fatal(err)
		}
		got, err := verifier.Verify(req)
		reason, ok := decisiontrust.FailureReason(err)
		if !got.IsZero() || !ok || reason != decisiontrust.ReasonKeyNotActiveForVerify {
			t.Fatalf("post-revoke = %#v, %v", got, err)
		}
	})

	for _, boundary := range []string{"issued-at-skew-boundary", "just-before-expiry-skew-boundary"} {
		t.Run(boundary, func(t *testing.T) {
			fx := newSigningFixture(t, newStore(t))
			reserved := fx.reserve("worker-" + boundary)
			emitted, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: reserved.ID})
			if err != nil {
				t.Fatal(err)
			}
			lease := fx.verifyAsWorker(emitted.SignedLease)
			now := lease.IssuedAt().Add(-5 * time.Second)
			if boundary == "just-before-expiry-skew-boundary" {
				now = lease.ExpiresAt().Add(5*time.Second - time.Nanosecond)
			}
			verifier, err := leaseverify.New(fx.workerVerifierConfig(reserved.WorkerIdentity))
			if err != nil {
				t.Fatal(err)
			}
			bundle := fx.bundle.get()
			bundle.ValidatedAt = now
			got, err := verifier.Verify(leaseverify.Request{Raw: emitted.SignedLease, Now: now, TrustBundle: bundle})
			if err != nil || got.IsZero() || !got.VerifiedAt().Equal(now) {
				t.Fatalf("boundary = %#v, %v", got, err)
			}
		})
	}

	t.Run("invalid-local-configuration", func(t *testing.T) {
		fx := newSigningFixture(t, newStore(t))
		for _, mutate := range []func(*leaseverify.Config){
			func(c *leaseverify.Config) { c.Codec = nil },
			func(c *leaseverify.Config) { c.Payload = nil },
			func(c *leaseverify.Config) { c.Boundary = nil },
			func(c *leaseverify.Config) { c.TrustDomain = "" },
			func(c *leaseverify.Config) { c.WorkerIdentity = "" },
			func(c *leaseverify.Config) { c.WorkerIdentity = string([]byte{0xff}) },
		} {
			cfg := fx.workerVerifierConfig("worker")
			mutate(&cfg)
			if _, err := leaseverify.New(cfg); !errors.Is(err, leaseverify.ErrInvalidConfig) {
				t.Fatalf("New error = %v", err)
			}
		}
		var verifier *leaseverify.Verifier
		got, err := verifier.Verify(leaseverify.Request{})
		if !got.IsZero() || err == nil {
			t.Fatal("nil verifier did not fail closed")
		}
	})
}

func (fx *signingFixture) workerVerifierConfig(worker string) leaseverify.Config {
	return leaseverify.Config{Codec: fx.codec, Payload: fx.payload, Boundary: fx.verifier,
		TrustDomain: signingTrustDomain, WorkerIdentity: worker}
}
