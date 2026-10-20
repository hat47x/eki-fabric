package statestoretest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasedelivery"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/leaseverify"
)

type leaseTransportFunc func(context.Context, leasedelivery.Envelope) error

func (f leaseTransportFunc) SendLease(ctx context.Context, packet leasedelivery.Envelope) error {
	return f(ctx, packet)
}

type deliveryStore struct {
	leasedelivery.Store
	loadEmission func(context.Context, ids.LeaseID) (leaseemission.Record, error)
	loadLease    func(context.Context, ids.LeaseID) (leasestate.LeaseRecord, error)
}

func (s deliveryStore) LoadLeaseEmission(ctx context.Context, id ids.LeaseID) (leaseemission.Record, error) {
	if s.loadEmission != nil {
		return s.loadEmission(ctx, id)
	}
	return s.Store.LoadLeaseEmission(ctx, id)
}
func (s deliveryStore) LoadLease(ctx context.Context, id ids.LeaseID) (leasestate.LeaseRecord, error) {
	if s.loadLease != nil {
		return s.loadLease(ctx, id)
	}
	return s.Store.LoadLease(ctx, id)
}

func runLeaseDeliveryContract(t *testing.T, newStore LeaseEmissionFactory) {
	t.Helper()
	t.Run("persisted-bytes-reach-worker-and-retry-never-resigns", func(t *testing.T) {
		store := newStore(t)
		fx := newSigningFixture(t, store)
		lease := fx.reserve("delivery-positive")
		emitted, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: lease.ID})
		if err != nil {
			t.Fatal(err)
		}
		verifier, err := leaseverify.New(fx.workerVerifierConfig(lease.WorkerIdentity))
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		transportFailure := errors.New("acknowledgement lost")
		service, err := leasedelivery.New(store, leaseTransportFunc(func(ctx context.Context, packet leasedelivery.Envelope) error {
			calls++
			if packet.LeaseID != lease.ID || packet.WorkerIdentity != lease.WorkerIdentity || !bytes.Equal(packet.SignedLease, emitted.SignedLease) {
				t.Fatal("delivery changed the persisted route or bytes")
			}
			verified, err := verifier.Verify(leaseverify.Request{Raw: packet.SignedLease, Now: fx.clock.now, TrustBundle: fx.bundle.get()})
			if err != nil || verified.Lease().TaskID() != lease.TaskID {
				t.Fatalf("Worker verification: %v", err)
			}
			// A transport owns its copy; mutation must not corrupt the Store.
			packet.SignedLease[0] ^= 1
			if calls == 1 {
				return transportFailure
			}
			return nil
		}), fx.clock)
		if err != nil {
			t.Fatal(err)
		}
		err = service.Send(context.Background(), lease.ID)
		if !errors.Is(err, leasedelivery.ErrDeliveryUnknown) || !errors.Is(err, transportFailure) || calls != 1 {
			t.Fatalf("first send = %v, calls=%d", err, calls)
		}
		if err := service.Send(context.Background(), lease.ID); err != nil {
			t.Fatal(err)
		}
		if calls != 2 || fx.signer.calls.Load() != 1 {
			t.Fatal("delivery retried internally or re-signed")
		}
		record, err := store.LoadLeaseEmission(context.Background(), lease.ID)
		if err != nil || !bytes.Equal(record.SignedEnvelope(), emitted.SignedLease) {
			t.Fatal("transport mutation changed durable bytes")
		}
	})

	for _, name := range []string{"reserved", "aborted", "expired", "future", "missing-clock", "cancelled", "cancelled-during-load", "nil-context", "invalid-id", "store-error", "invalid-emission", "wrong-emission", "invalid-reservation", "unfenced", "policy-binding"} {
		t.Run(name, func(t *testing.T) {
			store := newStore(t)
			fx := newSigningFixture(t, store)
			lease := fx.reserve("delivery-" + name)
			if name == "aborted" {
				if err := fx.registry.Revoke(signingKeyID, fx.clock.now); err != nil {
					t.Fatal(err)
				}
			}
			if name != "reserved" {
				_, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: lease.ID})
				if name == "aborted" {
					if !errors.Is(err, leaseissue.ErrLeaseAborted) {
						t.Fatal(err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			wrapped := deliveryStore{Store: store}
			clock := fx.clock
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			id := lease.ID
			switch name {
			case "expired":
				clock.now = lease.ExpiresAt
			case "future":
				clock.now = lease.IssuedAt.Add(-time.Second)
			case "missing-clock":
				clock.now = time.Time{}
			case "cancelled":
				cancel()
			case "nil-context":
				ctx = nil
			case "invalid-id":
				id = ids.LeaseID("invalid")
			case "store-error":
				wrapped.loadEmission = func(context.Context, ids.LeaseID) (leaseemission.Record, error) {
					return leaseemission.Record{}, errors.New("store unavailable")
				}
			case "invalid-emission":
				wrapped.loadEmission = func(context.Context, ids.LeaseID) (leaseemission.Record, error) { return leaseemission.Record{}, nil }
			case "wrong-emission":
				other := fx.reserve("delivery-other")
				result, err := fx.emitter.Emit(context.Background(), leaseissue.EmitRequest{LeaseID: other.ID})
				if err != nil {
					t.Fatal(err)
				}
				wrapped.loadEmission = func(context.Context, ids.LeaseID) (leaseemission.Record, error) { return result.Record, nil }
			case "cancelled-during-load", "invalid-reservation", "unfenced", "policy-binding":
				wrapped.loadLease = func(context.Context, ids.LeaseID) (leasestate.LeaseRecord, error) {
					v := lease
					switch name {
					case "cancelled-during-load":
						cancel()
					case "invalid-reservation":
						v.ID = ""
					case "unfenced":
						v.PolicyEpoch = 0
					case "policy-binding":
						v.PolicyEpoch++
					}
					return v, nil
				}
			}
			calls := 0
			service, err := leasedelivery.New(wrapped, leaseTransportFunc(func(context.Context, leasedelivery.Envelope) error { calls++; return nil }), clock)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Send(ctx, id); err == nil || calls != 0 {
				t.Fatalf("rejected delivery error=%v calls=%d", err, calls)
			}
		})
	}
	t.Run("invalid-configuration", func(t *testing.T) {
		if _, err := leasedelivery.New(nil, nil, nil); !errors.Is(err, leasedelivery.ErrInvalidConfig) {
			t.Fatal(err)
		}
		var service *leasedelivery.Service
		if err := service.Send(context.Background(), ""); !errors.Is(err, leasedelivery.ErrInvalidConfig) {
			t.Fatal(err)
		}
	})
}
