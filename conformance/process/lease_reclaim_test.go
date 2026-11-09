package process

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/leaserecall"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

// leaseRecordOf is the registry entry of the Lease that seedClaimFor reserved.
// seedClaimFor issues the Lease one second before `now` and lets it run one hour.
func leaseRecordOf(claim resultclaim.Claim, now time.Time) leasestate.LeaseRecord {
	return leasestate.LeaseRecord{
		ID: claim.LeaseID(), TaskID: claim.TaskID(), TaskIRDigest: claim.TaskIRDigest(),
		AttemptID: claim.AttemptID(), WorkerIdentity: claim.WorkerIdentity(),
		FenceEpoch: claim.FenceEpoch(),
		IssuedAt:   now.Add(-time.Second), ExpiresAt: now.Add(time.Hour),
	}
}

// withLeaseRegistry adds lease_registry to the config that writeControlConfig wrote.
func (e *env) withLeaseRegistry(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(e.cfgPath)
	must(t, err)
	var cfg map[string]any
	must(t, json.Unmarshal(raw, &cfg))
	cfg["lease_registry"] = path
	raw, err = json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(e.cfgPath, raw, 0o600))
}

// TestControlClosesAttemptOfExpiredLeaseWithoutClaim runs eki-control as its own
// process over a state file that holds two dispatched Attempts whose Leases are in
// the lease registry. No Worker runs, so no ResultClaim arrives for either.
//
//   - The Lease that expired two hours ago is past RecallGrace: the Attempt is
//     closed as lease_expired and the entry leaves the registry.
//   - The Lease that expired two seconds ago is inside RecallGrace: the Attempt
//     stays dispatched and the entry stays in the registry.
//
// This shows that the recall loop is wired into the binary (#745). It does not
// show what happens to the Task afterwards, nor external effects: the Task model
// has no marker for them, and every expired Attempt is closed as retryable.
func TestControlClosesAttemptOfExpiredLeaseWithoutClaim(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC()
	oldNow := now.Add(-3 * time.Hour)               // Lease expired two hours ago
	freshNow := now.Add(-time.Hour - 2*time.Second) // Lease expired two seconds ago
	state := openState(t, e.controlDB)
	policy := seedPolicy(t, state, oldNow)
	expired := seedClaim(t, state, policy, 1, oldNow)
	inGrace := seedClaim(t, state, policy, 2, freshNow)
	must(t, state.Close())

	registryPath := filepath.Join(e.dir, "lease-registry.json")
	registry, err := leaserecall.OpenFile(registryPath)
	must(t, err)
	must(t, registry.Open(leaseRecordOf(expired, oldNow)))
	must(t, registry.Open(leaseRecordOf(inGrace, freshNow)))

	e.writeControlConfig(t)
	e.withLeaseRegistry(t, registryPath)
	e.startControl(t)

	waitFor(t, "expired Attempt closed", func() (bool, string) {
		v := viewOf(t, e.controlDB, expired)
		return v.phase == attemptstate.AttemptPhaseClosed, string(v.phase)
	})

	s := openState(t, e.controlDB)
	closed, err := s.LoadAttempt(context.Background(), expired.AttemptID())
	must(t, err)
	if closed.CloseReason != attemptstate.CloseReasonLeaseExpired {
		t.Fatalf("close reason = %q, want %q", closed.CloseReason, attemptstate.CloseReasonLeaseExpired)
	}
	_, claimed, err := s.LoadResultClaim(context.Background(), expired.AttemptID())
	must(t, err)
	must(t, s.Close())
	if claimed {
		t.Fatal("a ResultClaim appeared for an Attempt no Worker ran")
	}

	// The in-grace Attempt is not closed, and its Lease is still listed.
	if v := viewOf(t, e.controlDB, inGrace); v.phase != attemptstate.AttemptPhaseDispatched {
		t.Fatalf("Attempt inside the grace period = %q, want dispatched", v.phase)
	}
	raw, err := os.ReadFile(registryPath)
	must(t, err)
	if strings.Contains(string(raw), expired.LeaseID().String()) {
		t.Fatalf("reclaimed Lease is still in the registry:\n%s", raw)
	}
	if !strings.Contains(string(raw), inGrace.LeaseID().String()) {
		t.Fatalf("Lease inside the grace period left the registry:\n%s", raw)
	}
}
