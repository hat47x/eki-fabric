package statestoretest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/leasedelivery"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/leaseverify"
)

// This transport exists only in the conformance harness. A loopback TLS socket
// is not an adopted Worker protocol, peer identity, fencing, or execution proof.
func runLeaseHTTPDeliveryContract(t *testing.T, newStore LeaseEmissionFactory) {
	t.Helper()
	for _, name := range []string{"accepted", "signature-tampered", "wrong-worker", "expired-at-receiver", "response-lost"} {
		t.Run(name, func(t *testing.T) {
			store := newStore(t)
			fx := newSigningFixture(t, store)
			lease := fx.reserve("http-delivery-" + name)
			emitted, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: lease.ID})
			if err != nil {
				t.Fatal(err)
			}
			cfg := fx.workerVerifierConfig(lease.WorkerIdentity)
			now := fx.clock.now
			var wantReason decisiontrust.VerificationReason
			switch name {
			case "signature-tampered":
				wantReason = decisiontrust.ReasonSignatureInvalid
			case "wrong-worker":
				cfg.WorkerIdentity = "other-local-worker"
				wantReason = decisiontrust.ReasonSubjectMismatch
			case "expired-at-receiver":
				// The fixture permits five seconds of receiver clock skew.
				now = lease.ExpiresAt.Add(time.Minute)
				wantReason = decisiontrust.ReasonExpired
			}
			verifier, err := leaseverify.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			type observation struct {
				raw      []byte
				verified leaseverify.Verified
				err      error
			}
			observed := make(chan observation, 2)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, readErr := io.ReadAll(io.LimitReader(r.Body, int64(len(emitted.SignedLease)+1)))
				if readErr != nil {
					observed <- observation{raw: raw, err: readErr}
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				verified, verifyErr := verifier.Verify(leaseverify.Request{Raw: raw, Now: now, TrustBundle: fx.bundle.get()})
				if name == "response-lost" {
					// Verification happened, but the sender receives no HTTP ack.
					conn, _, hijackErr := w.(http.Hijacker).Hijack()
					if hijackErr != nil {
						observed <- observation{raw: raw, err: hijackErr}
						return
					}
					_ = conn.Close()
					observed <- observation{raw: raw, verified: verified, err: verifyErr}
					return
				}
				observed <- observation{raw: raw, verified: verified, err: verifyErr}
				if verifyErr != nil {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			client := server.Client()
			client.Timeout = 5 * time.Second
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }
			t.Cleanup(client.CloseIdleConnections)
			calls := 0
			service, err := leasedelivery.New(store, leaseTransportFunc(func(ctx context.Context, packet leasedelivery.Envelope) error {
				calls++
				if packet.LeaseID != lease.ID || packet.WorkerIdentity != lease.WorkerIdentity || !bytes.Equal(packet.SignedLease, emitted.SignedLease) {
					return errors.New("persisted route or bytes changed")
				}
				if name == "signature-tampered" {
					packet.SignedLease[len(packet.SignedLease)-1] ^= 1
				}
				// Endpoint and receiver identity are local fixture configuration.
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(packet.SignedLease))
				if err != nil {
					return err
				}
				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					return fmt.Errorf("test receiver status %d", resp.StatusCode)
				}
				return nil
			}), fx.clock)
			if err != nil {
				t.Fatal(err)
			}
			attempts := 1
			if name == "response-lost" {
				attempts = 2 // Explicit retry can deliver twice; it never re-signs.
			}
			for i := 1; i <= attempts; i++ {
				err := service.Send(context.Background(), lease.ID)
				if name == "accepted" {
					if err != nil {
						t.Fatal(err)
					}
				} else if !errors.Is(err, leasedelivery.ErrDeliveryUnknown) {
					t.Fatalf("Send error = %v, want unknown outcome", err)
				}
				if calls != i {
					t.Fatalf("transport calls = %d, want %d", calls, i)
				}
				select {
				case got := <-observed:
					if wantReason != "" {
						reason, ok := decisiontrust.FailureReason(got.err)
						if !ok || reason != wantReason || !got.verified.IsZero() {
							t.Fatalf("receiver reason=%v error=%v verified=%v", reason, got.err, got.verified)
						}
					} else if got.err != nil || got.verified.IsZero() || !got.verified.Lease().Equal(fx.verifyAsWorker(emitted.SignedLease)) {
						t.Fatalf("receiver verification = %v, error=%v", got.verified, got.err)
					}
					if name != "signature-tampered" && !bytes.Equal(got.raw, emitted.SignedLease) {
						t.Fatal("socket receiver got different bytes")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("receiver observation missing")
				}
			}
			if fx.signer.calls.Load() != 1 {
				t.Fatal("delivery re-signed the Lease")
			}
			stored, err := store.LoadLeaseEmission(context.Background(), lease.ID)
			if err != nil || !bytes.Equal(stored.SignedEnvelope(), emitted.SignedLease) {
				t.Fatalf("durable bytes changed: %v", err)
			}
		})
	}
}
