package resultclaim

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var (
	baseTime    = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	testTask    = ids.TaskID("tsk_00000000000000000000000000000001")
	testAttempt = ids.AttemptID("atm_00000000000000000000000000000001")
	testLease   = ids.LeaseID("les_00000000000000000000000000000001")
)

func claimConfig() ClaimConfig {
	return ClaimConfig{
		TaskID:            testTask,
		TaskIRDigest:      contract.SHA256([]byte("taskir")),
		AttemptID:         testAttempt,
		WorkerIdentity:    "worker-a",
		LeaseID:           testLease,
		FenceEpoch:        7,
		WorkOrderDecision: "wor-decision-001",
		ExecutorDigest:    contract.SHA256([]byte("executable")),
		Nonce:             []byte("0123456789abcdef"),
		Status:            StatusCompleted,
		ExitCode:          0,
		OutputBytes:       512,
		OutputDigest:      contract.SHA256([]byte("output")),
		StartedAt:         baseTime,
		EndedAt:           baseTime.Add(time.Minute),
	}
}

func mustClaim(t *testing.T, mutate func(*ClaimConfig)) Claim {
	t.Helper()
	cfg := claimConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	claim, err := NewClaim(cfg)
	if err != nil {
		t.Fatalf("NewClaim() error = %v", err)
	}
	return claim
}

func TestNewClaimAcceptsTheClosedShape(t *testing.T) {
	claim := mustClaim(t, nil)
	if !claim.Valid() {
		t.Fatal("claim is not valid")
	}
	if claim.TaskID() != testTask || claim.AttemptID() != testAttempt || claim.FenceEpoch() != 7 {
		t.Fatalf("claim = %+v", claim)
	}
	if claim.Status() != StatusCompleted || claim.OutputBytes() != 512 {
		t.Fatalf("status/output = %q/%d", claim.Status(), claim.OutputBytes())
	}
}

func TestNewClaimRejectsInvalidShapes(t *testing.T) {
	cases := map[string]func(*ClaimConfig){
		"task":                               func(c *ClaimConfig) { c.TaskID = "" },
		"task ir":                            func(c *ClaimConfig) { c.TaskIRDigest = contract.Digest{} },
		"attempt":                            func(c *ClaimConfig) { c.AttemptID = "" },
		"worker":                             func(c *ClaimConfig) { c.WorkerIdentity = "" },
		"lease":                              func(c *ClaimConfig) { c.LeaseID = "" },
		"fence":                              func(c *ClaimConfig) { c.FenceEpoch = 0 },
		"work order":                         func(c *ClaimConfig) { c.WorkOrderDecision = "" },
		"executor":                           func(c *ClaimConfig) { c.ExecutorDigest = contract.Digest{} },
		"short nonce":                        func(c *ClaimConfig) { c.Nonce = make([]byte, MinNonceBytes-1) },
		"long nonce":                         func(c *ClaimConfig) { c.Nonce = make([]byte, MaxNonceBytes+1) },
		"unknown status":                     func(c *ClaimConfig) { c.Status = Status("done") },
		"zero start":                         func(c *ClaimConfig) { c.StartedAt = time.Time{} },
		"end before start":                   func(c *ClaimConfig) { c.EndedAt = c.StartedAt.Add(-time.Second) },
		"completed exit":                     func(c *ClaimConfig) { c.ExitCode = 1 },
		"completed without an output digest": func(c *ClaimConfig) { c.OutputDigest = contract.Digest{} },
		"failed with an output digest":       func(c *ClaimConfig) { c.Status = StatusFailed; c.ExitCode = 1 },
		"stuck with an output digest":        func(c *ClaimConfig) { c.Status = StatusRunnerStuck },
	}
	for name, mutate := range cases {
		cfg := claimConfig()
		mutate(&cfg)
		if _, err := NewClaim(cfg); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("%s: NewClaim() error = %v, want %v", name, err, ErrInvalidClaim)
		}
	}
}

func TestClaimEqualAndDigest(t *testing.T) {
	first := mustClaim(t, nil)
	second := mustClaim(t, nil)
	if !first.Equal(second) {
		t.Fatal("identical claims are not Equal")
	}
	if first.Digest() != second.Digest() {
		t.Fatal("identical claims have different digests")
	}
	changed := mustClaim(t, func(c *ClaimConfig) { c.OutputBytes = 513 })
	if first.Equal(changed) {
		t.Fatal("claims with different output are Equal")
	}
	if first.Digest() == changed.Digest() {
		t.Fatal("claims with different output have the same digest")
	}
	otherStatus := mustClaim(t, func(c *ClaimConfig) {
		c.Status = StatusFailed
		c.ExitCode = 1
		c.OutputDigest = contract.Digest{}
	})
	if first.Digest() == otherStatus.Digest() {
		t.Fatal("status is not bound into the digest")
	}
}

func TestClaimBindsTheOutputDigest(t *testing.T) {
	first := mustClaim(t, nil)
	if first.OutputDigest() != contract.SHA256([]byte("output")) {
		t.Fatalf("OutputDigest() = %s", first.OutputDigest())
	}
	other := mustClaim(t, func(c *ClaimConfig) { c.OutputDigest = contract.SHA256([]byte("another output")) })
	if first.Equal(other) {
		t.Fatal("claims that name different outputs are Equal")
	}
	if first.Digest() == other.Digest() {
		t.Fatal("the output digest is not bound into the claim digest")
	}
}

// A claim without an output digest hashes the way it did before the field
// existed, so a row stored earlier still verifies against its recorded digest.
func TestClaimDigestIgnoresAnAbsentOutputDigest(t *testing.T) {
	failed := mustClaim(t, func(c *ClaimConfig) {
		c.Status = StatusFailed
		c.ExitCode = 1
		c.OutputDigest = contract.Digest{}
	})
	again := mustClaim(t, func(c *ClaimConfig) {
		c.Status = StatusFailed
		c.ExitCode = 1
		c.OutputDigest = contract.Digest{}
	})
	if failed.Digest() != again.Digest() || !failed.OutputDigest().IsZero() {
		t.Fatal("a claim without an output digest is not stable")
	}
	completed := mustClaim(t, nil)
	if completed.Digest() == failed.Digest() {
		t.Fatal("a completed claim shares a digest with a failed one")
	}
}

func TestDecideFirstClaimClosesAnOpenAttempt(t *testing.T) {
	claim := mustClaim(t, func(c *ClaimConfig) { c.Status = StatusFailed; c.ExitCode = 1; c.OutputDigest = contract.Digest{} })
	decision, err := Decide(claim, Existing{AttemptID: testAttempt, AttemptOpen: true, CurrentFence: 7})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Kind != KindNew || !decision.Record || !decision.CloseAttempt {
		t.Fatalf("decision = %+v, want a new closing claim", decision)
	}
}

func TestDecideDoesNotCloseOnACompletedClaim(t *testing.T) {
	claim := mustClaim(t, nil)
	decision, err := Decide(claim, Existing{AttemptID: testAttempt, AttemptOpen: true, CurrentFence: 7})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Kind != KindNew || !decision.Record || decision.CloseAttempt {
		t.Fatalf("decision = %+v, want a new claim without a close", decision)
	}
}

func TestDecideCollapsesAnExactReplay(t *testing.T) {
	claim := mustClaim(t, nil)
	decision, err := Decide(claim, Existing{
		AttemptID: testAttempt, AttemptOpen: false, CurrentFence: 7, Recorded: &claim,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Kind != KindDuplicate || decision.Record || decision.CloseAttempt {
		t.Fatalf("decision = %+v, want a duplicate", decision)
	}
}

func TestDecideRetriesTheCloseAfterACrash(t *testing.T) {
	claim := mustClaim(t, func(c *ClaimConfig) { c.Status = StatusReclaimed; c.ExitCode = 0; c.OutputDigest = contract.Digest{} })
	decision, err := Decide(claim, Existing{
		AttemptID: testAttempt, AttemptOpen: true, CurrentFence: 7, Recorded: &claim,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Kind != KindDuplicate || decision.Record || !decision.CloseAttempt {
		t.Fatalf("decision = %+v, want the recorded claim to retry the close", decision)
	}
}

func TestDecideRejectsAConflictingClaim(t *testing.T) {
	recorded := mustClaim(t, nil)
	conflict := mustClaim(t, func(c *ClaimConfig) { c.OutputBytes = 1 })
	if _, err := Decide(conflict, Existing{
		AttemptID: testAttempt, AttemptOpen: true, CurrentFence: 7, Recorded: &recorded,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Decide() error = %v, want %v", err, ErrConflict)
	}
}

func TestDecideRecordsAStaleClaimWithoutClosing(t *testing.T) {
	claim := mustClaim(t, nil)
	decision, err := Decide(claim, Existing{AttemptID: testAttempt, AttemptOpen: true, CurrentFence: 9})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Kind != KindStale || !decision.Record || decision.CloseAttempt {
		t.Fatalf("decision = %+v, want a recorded stale claim", decision)
	}
}

func TestDecideRecordsALateClaimWithoutReopening(t *testing.T) {
	claim := mustClaim(t, nil)
	decision, err := Decide(claim, Existing{AttemptID: testAttempt, AttemptOpen: false, CurrentFence: 7})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Kind != KindLate || !decision.Record || decision.CloseAttempt {
		t.Fatalf("decision = %+v, want a recorded late claim", decision)
	}
}

func TestDecideRejectsAnotherAttemptOrAnUnconstructedClaim(t *testing.T) {
	claim := mustClaim(t, nil)
	other := ids.AttemptID("atm_00000000000000000000000000000002")
	if _, err := Decide(claim, Existing{AttemptID: other, AttemptOpen: true}); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("other attempt error = %v, want %v", err, ErrInvalidClaim)
	}
	if _, err := Decide(Claim{}, Existing{AttemptID: testAttempt, AttemptOpen: true}); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("zero claim error = %v, want %v", err, ErrInvalidClaim)
	}
}

func TestStatusPolicy(t *testing.T) {
	closing := []Status{StatusFailed, StatusReclaimed, StatusDeadlineExceeded, StatusCancelled, StatusOutputExceeded}
	for _, status := range closing {
		if !status.ClosesAttempt() {
			t.Fatalf("%q should close the Attempt", status)
		}
	}
	if StatusCompleted.ClosesAttempt() {
		t.Fatal("completed must move to Result Verification, not close the Attempt")
	}
	if StatusRunnerStuck.ClosesAttempt() {
		t.Fatal("runner_stuck must not close the Attempt before the platform stops it")
	}
	if !StatusCompleted.NeedsVerification() {
		t.Fatal("completed must go to Result Verification")
	}
	for _, status := range []Status{StatusFailed, StatusReclaimed, StatusRunnerStuck} {
		if status.NeedsVerification() {
			t.Fatalf("%q must not go to Result Verification", status)
		}
	}
}
