package resultacceptance

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultverify"
)

// observe builds one raw verifier report about the fixture's Attempt.
func (f *fixture) observe(id, principal, operator string, method resultverify.Method, observed contract.Digest, verdict resultverify.Verdict) resultverify.Observation {
	return resultverify.Observation{
		VerifierID: id, Method: method,
		TaskID: f.task.ID, AttemptID: f.attempt.ID, TaskIRDigest: f.task.TaskIRDigest,
		ObservedDigest: observed, Verdict: verdict,
		Dimensions: map[resultaccept.Dimension]string{
			resultaccept.DimensionPrincipal:      principal,
			resultaccept.DimensionOperator:       operator,
			resultaccept.DimensionExecutorDigest: digestOf("executor-of-" + id),
		},
	}
}

func (f *fixture) twoReexecutions() []resultverify.Observation {
	return []resultverify.Observation{
		f.observe("verifier-1", "principal-P1", "operator-O1", resultverify.MethodReexecution, outputDigest, resultverify.VerdictNone),
		f.observe("verifier-2", "principal-P2", "operator-O2", resultverify.MethodReexecution, outputDigest, resultverify.VerdictNone),
	}
}

func TestObservationsThatReproduceTheClaimAcceptIt(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	result, err := f.service.Accept(context.Background(), Request{AttemptID: f.attempt.ID, Observations: f.twoReexecutions()})
	if err != nil || result.Outcome() != resultaccept.OutcomeAccepted || result.Accepted.Level() != resultaccept.LevelRA2 {
		t.Fatalf("Accept() = %+v, %v", result, err)
	}
}

// V5: the Worker claims an output digest that the stored bytes do not have.
// The integrity check named as a reject authority rejects the claim, even
// though two re-executions agree with the claimed digest.
func TestStoredBytesThatDoNotMatchTheClaimRejectIt(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	observations := append(f.twoReexecutions(),
		f.observe("authority-1", "principal-P9", "operator-O9", resultverify.MethodArtifactIntegrity,
			contract.SHA256([]byte("bytes the worker actually stored")), resultverify.VerdictNone))
	result, err := f.service.Accept(context.Background(), Request{AttemptID: f.attempt.ID, Observations: observations})
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Outcome() != resultaccept.OutcomeRejected || result.Decision.Reason != resultaccept.ReasonAuthorityRejected {
		t.Fatalf("result = %+v, want an authority rejection", result)
	}
	attempt, err := f.store.LoadAttempt(context.Background(), f.attempt.ID)
	if err != nil || attempt.Phase != attemptstate.AttemptPhaseClosed || attempt.CloseReason != attemptstate.CloseReasonResultRejected {
		t.Fatalf("Attempt = %+v, %v, want closed as rejected", attempt, err)
	}
	if _, ok, err := f.store.LoadAcceptedResult(context.Background(), f.task.ID); err != nil || ok {
		t.Fatalf("an accepted result exists: %v, %v", ok, err)
	}
}

// A mismatch from a verifier the policy does not name never accepts the
// claim, but it is not final either: the Attempt stays in verification.
func TestAMismatchWithoutAuthorityHoldsTheClaim(t *testing.T) {
	cases := map[string]resultverify.Observation{}
	f := newFixture(t, fixtureOptions{})
	other := contract.SHA256([]byte("another output"))
	cases["integrity check not named by the policy"] = f.observe("integrity-9", "principal-P9", "operator-O9",
		resultverify.MethodArtifactIntegrity, other, resultverify.VerdictNone)
	cases["re-execution of another digest"] = f.observe("verifier-3", "principal-P3", "operator-O3",
		resultverify.MethodReexecution, other, resultverify.VerdictNone)
	cases["validator that accepted other bytes"] = f.observe("verifier-3", "principal-P3", "operator-O3",
		resultverify.MethodValidator, other, resultverify.VerdictAccepted)
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, fixtureOptions{})
			extra.TaskID, extra.AttemptID, extra.TaskIRDigest = f.task.ID, f.attempt.ID, f.task.TaskIRDigest
			result, err := f.service.Accept(context.Background(),
				Request{AttemptID: f.attempt.ID, Observations: append(f.twoReexecutions(), extra)})
			if err != nil || result.Outcome() != resultaccept.OutcomeHeld ||
				result.Decision.Reason != resultaccept.ReasonVerificationsDisagree {
				t.Fatalf("Accept() = %+v, %v, want held for disagreement", result, err)
			}
			f.assertUntouched(t)
		})
	}
}

func TestAnObservationAboutOtherWorkRefusesTheWholeRequest(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	observations := f.twoReexecutions()
	observations[1].TaskIRDigest = contract.SHA256([]byte("another TaskIR"))
	if _, err := f.service.Accept(context.Background(), Request{AttemptID: f.attempt.ID, Observations: observations}); !errors.Is(err, resultverify.ErrBindingMismatch) {
		t.Fatalf("Accept() error = %v, want %v", err, resultverify.ErrBindingMismatch)
	}
	f.assertUntouched(t)
}
