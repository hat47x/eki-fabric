package workorderemission

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func validEmitted(t *testing.T) EmittedConfig {
	t.Helper()
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	signed := []byte("signed-work-order")
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	return EmittedConfig{
		LeaseID:            leaseID,
		DecisionID:         "work-order-decision:" + leaseID.String(),
		WorkOrderID:        "work-order:" + leaseID.String(),
		WorkerIdentity:     "worker-a",
		TrustDomain:        "local.example",
		AuthorityPrincipal: "control-plane/wor-1",
		KeyID:              decisiontrust.KeyID("kid-wor-1"),
		Algorithm:          decisiontrust.AlgorithmEdDSA,
		PayloadDigest:      contract.SHA256([]byte("payload")),
		EnvelopeDigest:     contract.SHA256(signed),
		SignedEnvelope:     signed,
		PolicyRevision:     "policy-42",
		PolicyEpoch:        1,
		FenceEpoch:         1,
		ExpiresAt:          now.Add(time.Minute),
		CommittedAt:        now,
	}
}

func TestEmittedRecordValidates(t *testing.T) {
	record, err := NewEmitted(validEmitted(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	signed := record.SignedEnvelope()
	signed[0] ^= 0xff
	if string(record.SignedEnvelope()) != "signed-work-order" {
		t.Fatal("SignedEnvelope() exposed the stored bytes")
	}
}

func TestEmittedRecordRejectsBrokenBinding(t *testing.T) {
	cases := map[string]func(*EmittedConfig){
		"envelope digest": func(c *EmittedConfig) { c.EnvelopeDigest = contract.SHA256([]byte("other")) },
		"worker":          func(c *EmittedConfig) { c.WorkerIdentity = "" },
		"fence":           func(c *EmittedConfig) { c.FenceEpoch = 0 },
		"policy epoch":    func(c *EmittedConfig) { c.PolicyEpoch = 0 },
		"expired":         func(c *EmittedConfig) { c.ExpiresAt = c.CommittedAt },
	}
	for name, mutate := range cases {
		cfg := validEmitted(t)
		mutate(&cfg)
		if _, err := NewEmitted(cfg); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("%s: NewEmitted() error = %v, want ErrInvalidRecord", name, err)
		}
	}
}

func TestAbortedRecordRequiresKnownReason(t *testing.T) {
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	record, err := NewAborted(AbortedConfig{LeaseID: leaseID, AbortReason: AbortLeaseSuperseded, Reason: "closed", CommittedAt: now})
	if err != nil || record.Validate() != nil || len(record.SignedEnvelope()) != 0 {
		t.Fatalf("NewAborted() = %#v err=%v", record, err)
	}
	if _, err := NewAborted(AbortedConfig{LeaseID: leaseID, AbortReason: "cancelled", Reason: "x", CommittedAt: now}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("unknown reason error = %v, want ErrInvalidRecord", err)
	}
}
