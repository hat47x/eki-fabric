package leaseemission

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func TestEmittedRecordBindsExactSignedEnvelope(t *testing.T) {
	cfg := validEmittedConfig(t)
	record, err := NewEmitted(cfg)
	if err != nil {
		t.Fatalf("NewEmitted() error = %v", err)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if record.Status() != StatusEmitted ||
		record.EnvelopeDigest() != cfg.EnvelopeDigest ||
		record.PayloadDigest() != cfg.PayloadDigest ||
		!bytes.Equal(record.SignedEnvelope(), cfg.SignedEnvelope) {
		t.Fatalf("record = %#v", record)
	}

	cfg.SignedEnvelope[0] ^= 0xff
	got := record.SignedEnvelope()
	got[0] ^= 0xff
	if bytes.Equal(record.SignedEnvelope(), cfg.SignedEnvelope) || bytes.Equal(record.SignedEnvelope(), got) {
		t.Fatal("Record retained caller-owned mutable signed bytes")
	}
}

func TestEmittedRecordRejectsEnvelopeDigestMismatch(t *testing.T) {
	cfg := validEmittedConfig(t)
	cfg.EnvelopeDigest = contract.SHA256([]byte("different"))
	if _, err := NewEmitted(cfg); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("NewEmitted() error = %v, want ErrInvalidRecord", err)
	}
}

func TestAbortedRecordPreservesCloseReasonAndReason(t *testing.T) {
	leaseID := mustLeaseID(t)
	at := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	record, err := NewAborted(AbortedConfig{
		LeaseID: leaseID,
		CloseReason: "policy_revoked",
		Reason: "policy changed before emission",
		CommittedAt: at,
	})
	if err != nil {
		t.Fatalf("NewAborted() error = %v", err)
	}
	if record.Status() != StatusAborted ||
		record.CloseReason() != "policy_revoked" ||
		record.Reason() != "policy changed before emission" ||
		!record.CommittedAt().Equal(at) {
		t.Fatalf("record = %#v", record)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func validEmittedConfig(t *testing.T) EmittedConfig {
	t.Helper()
	signed := []byte("synthetic signed Lease envelope")
	return EmittedConfig{
		LeaseID: mustLeaseID(t),
		DecisionID: "lease-decision-1",
		TrustDomain: "local.example",
		AuthorityPrincipal: "control-plane/lse-1",
		KeyID: decisiontrust.KeyID("kid-lse-1"),
		Algorithm: decisiontrust.AlgorithmEdDSA,
		PayloadDigest: contract.SHA256([]byte("payload")),
		EnvelopeDigest: contract.SHA256(signed),
		SignedEnvelope: signed,
		PolicyRevision: "policy-42",
		PolicyEpoch: 1,
		CommittedAt: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
	}
}

func mustLeaseID(t *testing.T) ids.LeaseID {
	t.Helper()
	id, err := ids.ParseLeaseID("les_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
