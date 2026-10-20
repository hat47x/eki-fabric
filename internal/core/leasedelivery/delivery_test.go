package leasedelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

var baseTime = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type fakeStore struct {
	lease    leasestate.LeaseRecord
	emission leaseemission.Record
	leaseErr error
	emitErr  error
}

func (s fakeStore) LoadLease(context.Context, ids.LeaseID) (leasestate.LeaseRecord, error) {
	return s.lease, s.leaseErr
}

func (s fakeStore) LoadLeaseEmission(context.Context, ids.LeaseID) (leaseemission.Record, error) {
	return s.emission, s.emitErr
}

type fakeTransport struct {
	calls   []Envelope
	sendErr error
}

func (t *fakeTransport) SendLease(_ context.Context, envelope Envelope) error {
	t.calls = append(t.calls, envelope)
	return t.sendErr
}

func newLease(t *testing.T, policyEpoch uint64) leasestate.LeaseRecord {
	t.Helper()
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatalf("NewLeaseID() error = %v", err)
	}
	taskID, err := ids.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() error = %v", err)
	}
	attemptID, err := ids.NewAttemptID()
	if err != nil {
		t.Fatalf("NewAttemptID() error = %v", err)
	}
	record, err := leasestate.New(leasestate.Config{
		ID:                          leaseID,
		TaskID:                      taskID,
		TaskIRDigest:                contract.SHA256([]byte("task-ir")),
		AttemptID:                   attemptID,
		WorkerIdentity:              "worker-a",
		PlacementTrustDomain:        "example.org",
		PlacementAuthorityPrincipal: "plc-1",
		PlacementDecisionID:         "decision-1",
		PlacementPayloadDigest:      contract.SHA256([]byte("placement")),
		PlacementExpiresAt:          baseTime.Add(2 * time.Hour),
		PolicyRevision:              "P1",
		PolicyEpoch:                 policyEpoch,
		FenceEpoch:                  1,
		RenewalSequence:             1,
		IssuedAt:                    baseTime,
		ExpiresAt:                   baseTime.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("leasestate.New() error = %v", err)
	}
	return record
}

func newEmission(t *testing.T, lease leasestate.LeaseRecord, policyEpoch uint64) leaseemission.Record {
	t.Helper()
	signed := []byte{0x84, 0x43, 0xa1, 0x01, 0x26, 0x40, 0x40}
	record, err := leaseemission.NewEmitted(leaseemission.EmittedConfig{
		LeaseID:            lease.ID,
		DecisionID:         "decision-lease-1",
		TrustDomain:        "example.org",
		AuthorityPrincipal: "lse-1",
		KeyID:              decisiontrust.KeyID("key-1"),
		Algorithm:          decisiontrust.AlgorithmEdDSA,
		PayloadDigest:      contract.SHA256([]byte("lease-payload")),
		EnvelopeDigest:     contract.SHA256(signed),
		SignedEnvelope:     signed,
		PolicyRevision:     "P1",
		PolicyEpoch:        policyEpoch,
		CommittedAt:        baseTime,
	})
	if err != nil {
		t.Fatalf("leaseemission.NewEmitted() error = %v", err)
	}
	return record
}

func newAborted(t *testing.T, lease leasestate.LeaseRecord) leaseemission.Record {
	t.Helper()
	record, err := leaseemission.NewAborted(leaseemission.AbortedConfig{
		LeaseID:     lease.ID,
		CloseReason: "policy_revoked",
		Reason:      "the policy was revoked before emission",
		CommittedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("leaseemission.NewAborted() error = %v", err)
	}
	return record
}

func newService(t *testing.T, store Store, transport Transport, now time.Time) *Service {
	t.Helper()
	service, err := New(store, transport, fakeClock{now: now})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

func TestSendDeliversThePersistedEmittedBytes(t *testing.T) {
	lease := newLease(t, 5)
	emission := newEmission(t, lease, 5)
	transport := &fakeTransport{}
	service := newService(t, fakeStore{lease: lease, emission: emission}, transport, baseTime.Add(time.Minute))

	if err := service.Send(context.Background(), lease.ID); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(transport.calls) != 1 {
		t.Fatalf("transport calls = %d, want 1", len(transport.calls))
	}
	got := transport.calls[0]
	if got.LeaseID != lease.ID || got.WorkerIdentity != lease.WorkerIdentity {
		t.Fatalf("envelope = %+v, want the stored Lease route", got)
	}
	if string(got.SignedLease) != string(emission.SignedEnvelope()) {
		t.Fatalf("delivered bytes = %x, want the persisted EMITTED bytes", got.SignedLease)
	}
}

func TestSendRefusesNothingButEmitted(t *testing.T) {
	lease := newLease(t, 5)
	cases := map[string]leaseemission.Record{
		"aborted": newAborted(t, lease),
	}
	for name, emission := range cases {
		transport := &fakeTransport{}
		service := newService(t, fakeStore{lease: lease, emission: emission}, transport, baseTime.Add(time.Minute))
		err := service.Send(context.Background(), lease.ID)
		if !errors.Is(err, ErrNotDeliverable) {
			t.Fatalf("%s: Send() error = %v, want %v", name, err, ErrNotDeliverable)
		}
		if len(transport.calls) != 0 {
			t.Fatalf("%s: transport was called for a non-EMITTED Lease", name)
		}
	}

	transport := &fakeTransport{}
	service := newService(t, fakeStore{lease: lease}, transport, baseTime.Add(time.Minute))
	if err := service.Send(context.Background(), lease.ID); !errors.Is(err, ErrNotDeliverable) {
		t.Fatalf("zero emission: Send() error = %v, want %v", err, ErrNotDeliverable)
	}
	if len(transport.calls) != 0 {
		t.Fatal("transport was called without an EMITTED record")
	}
}

func TestSendRefusesWhenTheLeaseWindowIsNotOpen(t *testing.T) {
	lease := newLease(t, 5)
	emission := newEmission(t, lease, 5)

	for name, now := range map[string]time.Time{
		"before issue": lease.IssuedAt.Add(-time.Nanosecond),
		"at expiry":    lease.ExpiresAt,
		"after expiry": lease.ExpiresAt.Add(time.Hour),
	} {
		transport := &fakeTransport{}
		service := newService(t, fakeStore{lease: lease, emission: emission}, transport, now)
		if err := service.Send(context.Background(), lease.ID); !errors.Is(err, ErrNotDeliverable) {
			t.Fatalf("%s: Send() error = %v, want %v", name, err, ErrNotDeliverable)
		}
		if len(transport.calls) != 0 {
			t.Fatalf("%s: transport was called outside the Lease window", name)
		}
	}
}

func TestSendRefusesWhenEmissionAndReservationDiffer(t *testing.T) {
	lease := newLease(t, 5)
	emission := newEmission(t, lease, 6) // same Lease, different policy epoch
	transport := &fakeTransport{}
	service := newService(t, fakeStore{lease: lease, emission: emission}, transport, baseTime.Add(time.Minute))

	if err := service.Send(context.Background(), lease.ID); !errors.Is(err, ErrNotDeliverable) {
		t.Fatalf("Send() error = %v, want %v", err, ErrNotDeliverable)
	}
	if len(transport.calls) != 0 {
		t.Fatal("transport was called with mismatched emission and reservation")
	}
}

func TestSendReportsUnknownOutcomeInsteadOfRetrying(t *testing.T) {
	lease := newLease(t, 5)
	emission := newEmission(t, lease, 5)
	transport := &fakeTransport{sendErr: errors.New("connection reset")}
	service := newService(t, fakeStore{lease: lease, emission: emission}, transport, baseTime.Add(time.Minute))

	err := service.Send(context.Background(), lease.ID)
	if !errors.Is(err, ErrDeliveryUnknown) {
		t.Fatalf("Send() error = %v, want %v", err, ErrDeliveryUnknown)
	}
	if len(transport.calls) != 1 {
		t.Fatalf("transport calls = %d, want exactly 1 (no automatic retry)", len(transport.calls))
	}
}

func TestSendIsReplayableWithTheSameBytes(t *testing.T) {
	lease := newLease(t, 5)
	emission := newEmission(t, lease, 5)
	transport := &fakeTransport{}
	service := newService(t, fakeStore{lease: lease, emission: emission}, transport, baseTime.Add(time.Minute))

	for i := 0; i < 2; i++ {
		if err := service.Send(context.Background(), lease.ID); err != nil {
			t.Fatalf("Send() #%d error = %v", i+1, err)
		}
	}
	if len(transport.calls) != 2 {
		t.Fatalf("transport calls = %d, want 2", len(transport.calls))
	}
	if string(transport.calls[0].SignedLease) != string(transport.calls[1].SignedLease) {
		t.Fatal("replay delivered different bytes; the bytes must come from the durable record")
	}
}

func TestSendRejectsInvalidInputAndConfiguration(t *testing.T) {
	lease := newLease(t, 5)
	emission := newEmission(t, lease, 5)

	service := newService(t, fakeStore{lease: lease, emission: emission}, &fakeTransport{}, baseTime.Add(time.Minute))
	if err := service.Send(context.Background(), ids.LeaseID("not-a-lease")); !errors.Is(err, ErrNotDeliverable) {
		t.Fatalf("invalid Lease ID error = %v, want %v", err, ErrNotDeliverable)
	}
	// A nil context is a caller bug; it must fail closed instead of reaching
	// the transport.
	if err := service.Send(nil, lease.ID); !errors.Is(err, ErrNotDeliverable) {
		t.Fatalf("nil context error = %v, want %v", err, ErrNotDeliverable)
	}

	if _, err := New(nil, &fakeTransport{}, fakeClock{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(nil store) error = %v, want %v", err, ErrInvalidConfig)
	}
	if _, err := New(fakeStore{}, nil, fakeClock{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(nil transport) error = %v, want %v", err, ErrInvalidConfig)
	}
	if _, err := New(fakeStore{}, &fakeTransport{}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(nil clock) error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestSendPropagatesStoreFailure(t *testing.T) {
	loadErr := errors.New("state store unavailable")
	service := newService(t, fakeStore{leaseErr: loadErr, emitErr: loadErr}, &fakeTransport{}, baseTime.Add(time.Minute))
	if err := service.Send(context.Background(), mustLeaseID(t)); !errors.Is(err, loadErr) {
		t.Fatalf("Send() error = %v, want the store error", err)
	}
}

func mustLeaseID(t *testing.T) ids.LeaseID {
	t.Helper()
	id, err := ids.NewLeaseID()
	if err != nil {
		t.Fatalf("NewLeaseID() error = %v", err)
	}
	return id
}
