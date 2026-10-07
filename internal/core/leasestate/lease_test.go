package leasestate

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func TestLeaseRecordValidatesAndTracksActivityWindow(t *testing.T) {
	cfg := validConfig(t)
	lease, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := lease.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if lease.ActiveAt(cfg.IssuedAt.Add(-time.Nanosecond)) {
		t.Fatal("Lease active before issued_at")
	}
	if !lease.ActiveAt(cfg.IssuedAt) || !lease.ActiveAt(cfg.ExpiresAt.Add(-time.Nanosecond)) {
		t.Fatal("Lease inactive inside validity window")
	}
	if lease.ActiveAt(cfg.ExpiresAt) {
		t.Fatal("Lease active at expires_at")
	}
}

func TestLeaseRecordRejectsMissingAuthorityBindings(t *testing.T) {
	base := validConfig(t)
	cases := map[string]func(*Config){
		"lease-id":         func(c *Config) { c.ID = "" },
		"task-id":          func(c *Config) { c.TaskID = "" },
		"task-ir":          func(c *Config) { c.TaskIRDigest = contract.Digest{} },
		"attempt-id":       func(c *Config) { c.AttemptID = "" },
		"worker":           func(c *Config) { c.WorkerIdentity = "" },
		"placement-principal": func(c *Config) { c.PlacementAuthorityPrincipal = "" },
		"placement-id":        func(c *Config) { c.PlacementDecisionID = "" },
		"placement-digest": func(c *Config) { c.PlacementPayloadDigest = contract.Digest{} },
		"placement-expiry": func(c *Config) { c.PlacementExpiresAt = time.Time{} },
		"lease-outlives-placement": func(c *Config) { c.PlacementExpiresAt = c.ExpiresAt.Add(-time.Nanosecond) },
		"policy":           func(c *Config) { c.PolicyRevision = "" },
		"fence":            func(c *Config) { c.FenceEpoch = 0 },
		"window":           func(c *Config) { c.ExpiresAt = c.IssuedAt },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if _, err := New(cfg); !errors.Is(err, ErrInvalidLeaseRecord) {
				t.Fatalf("New() error = %v, want ErrInvalidLeaseRecord", err)
			}
		})
	}
}

func validConfig(t *testing.T) Config {
	t.Helper()
	leaseID, err := ids.ParseLeaseID("les_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	return Config{
		ID:                     leaseID,
		TaskID:                 taskID,
		TaskIRDigest:           contract.SHA256([]byte("task-ir")),
		AttemptID:              attemptID,
		WorkerIdentity:         "worker-a",
		PlacementAuthorityPrincipal: "control-plane/plc-1",
		PlacementDecisionID:           "placement-001",
		PlacementPayloadDigest:        contract.SHA256([]byte("placement")),
		PlacementExpiresAt:            issued.Add(2 * time.Minute),
		PolicyRevision:                "policy-42",
		FenceEpoch:             1,
		RenewalSequence:        0,
		IssuedAt:               issued,
		ExpiresAt:              issued.Add(time.Minute),
	}
}
