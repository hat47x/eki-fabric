package resultverify

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var (
	claimed = contract.SHA256([]byte("claimed output"))
	other   = contract.SHA256([]byte("other output"))
	taskIR  = contract.SHA256([]byte("taskir"))
)

func subject(t *testing.T) Subject {
	t.Helper()
	taskID, err := ids.NewTaskID()
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return Subject{TaskID: taskID, AttemptID: attemptID, TaskIRDigest: taskIR, OutputDigest: claimed}
}

func observation(s Subject, method Method, observed contract.Digest, verdict Verdict) Observation {
	return Observation{
		VerifierID: "verifier-1", Method: method,
		TaskID: s.TaskID, AttemptID: s.AttemptID, TaskIRDigest: s.TaskIRDigest,
		ObservedDigest: observed, Verdict: verdict,
		Dimensions: map[resultaccept.Dimension]string{resultaccept.DimensionPrincipal: "principal-1"},
	}
}

func TestNormalizeComparesWithTheClaimedDigest(t *testing.T) {
	s := subject(t)
	cases := []struct {
		name      string
		obs       Observation
		check     resultaccept.Check
		observed  contract.Digest
		matches   bool
		noVoiceOK bool
	}{
		{name: "re-execution reproduces the claimed digest", obs: observation(s, MethodReexecution, claimed, VerdictNone),
			check: resultaccept.CheckReproduced, observed: claimed, matches: true},
		{name: "re-execution of a different digest differs", obs: observation(s, MethodReexecution, other, VerdictNone),
			check: resultaccept.CheckDiffers, observed: other},
		{name: "validator accepts the claimed bytes", obs: observation(s, MethodValidator, claimed, VerdictAccepted),
			check: resultaccept.CheckValidatorAccepted, observed: claimed, matches: true},
		{name: "validator rejects the claimed bytes", obs: observation(s, MethodValidator, claimed, VerdictRejected),
			check: resultaccept.CheckValidatorRejected, observed: claimed, matches: true},
		{name: "a validator acceptance of other bytes differs, it is not an acceptance",
			obs:   observation(s, MethodValidator, other, VerdictAccepted),
			check: resultaccept.CheckDiffers, observed: other},
		{name: "stored bytes that do not hash to the claim reject the claimed output (V5)",
			obs:   observation(s, MethodArtifactIntegrity, other, VerdictNone),
			check: resultaccept.CheckValidatorRejected, observed: claimed},
		{name: "matching stored bytes add no voice", obs: observation(s, MethodArtifactIntegrity, claimed, VerdictNone),
			matches: true, noVoiceOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(s, tc.obs)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			if got.Matches != tc.matches {
				t.Fatalf("Matches = %v, want %v", got.Matches, tc.matches)
			}
			if tc.noVoiceOK {
				if got.Verification != nil {
					t.Fatalf("Verification = %+v, want none", got.Verification)
				}
				return
			}
			v := got.Verification
			if v == nil || v.Check != tc.check || v.ObservedDigest != tc.observed.Hex() ||
				v.TaskID != s.TaskID.String() || v.AttemptID != s.AttemptID.String() || v.VerifierID != "verifier-1" {
				t.Fatalf("Verification = %+v, want check %s observed %s", v, tc.check, tc.observed.Hex())
			}
		})
	}
}

func TestNormalizeRefusesObservationsItCannotTrust(t *testing.T) {
	s := subject(t)
	otherTask, err := ids.NewTaskID()
	if err != nil {
		t.Fatal(err)
	}
	otherAttempt, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		mutate func(*Observation)
		want   error
	}{
		"another task":            {func(o *Observation) { o.TaskID = otherTask }, ErrBindingMismatch},
		"another attempt":         {func(o *Observation) { o.AttemptID = otherAttempt }, ErrBindingMismatch},
		"another TaskIR":          {func(o *Observation) { o.TaskIRDigest = other }, ErrBindingMismatch},
		"no verifier":             {func(o *Observation) { o.VerifierID = "" }, ErrInvalidObservation},
		"no digest":               {func(o *Observation) { o.ObservedDigest = contract.Digest{} }, ErrInvalidObservation},
		"unknown method":          {func(o *Observation) { o.Method = "vote" }, ErrInvalidObservation},
		"validator verdict unset": {func(o *Observation) { o.Method = MethodValidator; o.Verdict = VerdictNone }, ErrInvalidObservation},
		"verdict on re-execution": {func(o *Observation) { o.Verdict = VerdictAccepted }, ErrInvalidObservation},
		"unknown verdict":         {func(o *Observation) { o.Method = MethodValidator; o.Verdict = "maybe" }, ErrInvalidObservation},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			obs := observation(s, MethodReexecution, claimed, VerdictNone)
			tc.mutate(&obs)
			if _, err := Normalize(s, obs); !errors.Is(err, tc.want) {
				t.Fatalf("Normalize() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizeAllRefusesTheWholeSetWhenOneObservationIsRefused(t *testing.T) {
	s := subject(t)
	good := observation(s, MethodReexecution, claimed, VerdictNone)
	bad := observation(s, MethodReexecution, other, VerdictNone)
	bad.VerifierID = "verifier-2"
	bad.TaskIRDigest = other
	if _, err := NormalizeAll(s, []Observation{good, bad}); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("NormalizeAll() error = %v, want %v", err, ErrBindingMismatch)
	}
	integrity := observation(s, MethodArtifactIntegrity, claimed, VerdictNone)
	integrity.VerifierID = "integrity"
	got, err := NormalizeAll(s, []Observation{good, integrity})
	if err != nil || len(got) != 1 || got[0].VerifierID != "verifier-1" {
		t.Fatalf("NormalizeAll() = %+v, %v, want only the re-execution", got, err)
	}
}

func TestNormalizeDoesNotShareDimensionsWithTheCaller(t *testing.T) {
	s := subject(t)
	obs := observation(s, MethodReexecution, claimed, VerdictNone)
	got, err := Normalize(s, obs)
	if err != nil {
		t.Fatal(err)
	}
	obs.Dimensions[resultaccept.DimensionPrincipal] = "changed"
	if got.Verification.Dimensions[resultaccept.DimensionPrincipal] != "principal-1" {
		t.Fatal("a later change to the observation altered the normalized verification")
	}
}

func TestSubjectOfNeedsACompletedClaim(t *testing.T) {
	s := subject(t)
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	cfg := resultclaim.ClaimConfig{
		TaskID: s.TaskID, TaskIRDigest: taskIR, AttemptID: s.AttemptID, WorkerIdentity: "worker-a",
		LeaseID: leaseID, FenceEpoch: 1, WorkOrderDecision: "workorder-1",
		ExecutorDigest: contract.SHA256([]byte("executor")), Nonce: []byte("0123456789abcdef"),
		Status: resultclaim.StatusCompleted, OutputDigest: claimed,
		StartedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), EndedAt: time.Date(2026, 10, 25, 0, 0, 1, 0, time.UTC),
	}
	completed, err := resultclaim.NewClaim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := SubjectOf(completed)
	if err != nil || got != s {
		t.Fatalf("SubjectOf() = %+v, %v, want %+v", got, err, s)
	}
	cfg.Status, cfg.ExitCode, cfg.OutputDigest = resultclaim.StatusFailed, 1, contract.Digest{}
	failed, err := resultclaim.NewClaim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubjectOf(failed); !errors.Is(err, ErrNotCompleted) {
		t.Fatalf("SubjectOf(failed) error = %v, want %v", err, ErrNotCompleted)
	}
}
