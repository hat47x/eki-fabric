package resourceprovider

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
)

func digestHex(s string) string { return contract.SHA256([]byte(s)).Hex() }

// testBinding returns the trusted values for a lease. The producer is principal-P0.
func testBinding(lease ids.LeaseID) ResultBinding {
	return ResultBinding{
		Lease:        lease,
		TaskID:       "task-1",
		AttemptID:    "attempt-1",
		WorkerID:     "worker-0",
		OutputDigest: digestHex("claimed output"),
		Dimensions: map[resultaccept.Dimension]string{
			resultaccept.DimensionPrincipal:      "principal-P0",
			resultaccept.DimensionOperator:       "operator-O0",
			resultaccept.DimensionExecutorDigest: digestHex("executor-E0"),
		},
	}
}

// verifierAt returns one verification of the claimed output by a verifier with the
// given principal and operator. Its executor digest is its own.
func verifierAt(id, principal, operator string, check resultaccept.Check, observed string) resultaccept.Verification {
	return resultaccept.Verification{
		VerifierID:     id,
		TaskID:         "task-1",
		AttemptID:      "attempt-1",
		Check:          check,
		ObservedDigest: observed,
		Dimensions: map[resultaccept.Dimension]string{
			resultaccept.DimensionPrincipal:      principal,
			resultaccept.DimensionOperator:       operator,
			resultaccept.DimensionExecutorDigest: digestHex("executor-of-" + id),
		},
	}
}

// raOnePolicy asks for one independent voice that accepts the output under RA-1.
func raOnePolicy() *resultaccept.Policy {
	return &resultaccept.Policy{
		Level:              resultaccept.LevelRA1,
		Threshold:          1,
		DistinctDimensions: []resultaccept.Dimension{resultaccept.DimensionPrincipal, resultaccept.DimensionOperator},
	}
}

// TestProviderSuccessIsNotAcceptedWithoutVerification covers rule (c) at the point
// where a provider success enters resultaccept. A success with no verification is
// held, and the conversion adds no verification of its own.
func TestProviderSuccessIsNotAcceptedWithoutVerification(t *testing.T) {
	lease := mustNewLease(t)
	ev := Event{LeaseID: lease, Sequence: 2, Kind: EventCompleted}
	claim, ok := ClaimFromEvent(ev)
	if !ok {
		t.Fatal("ClaimFromEvent(completed) ok = false, want a claim")
	}

	for name, verifs := range map[string][]resultaccept.Verification{
		"nil verifications":   nil,
		"empty verifications": {},
	} {
		t.Run(name, func(t *testing.T) {
			dec, err := DecideProviderResult(claim, testBinding(lease), verifs, resultaccept.OutputMechanical, raOnePolicy())
			if err != nil {
				t.Fatalf("DecideProviderResult() error = %v", err)
			}
			if dec.Outcome == resultaccept.OutcomeAccepted {
				t.Fatal("a provider success with no verification was accepted")
			}
			if dec.Outcome != resultaccept.OutcomeHeld || dec.Reason != resultaccept.ReasonNoIndependentVerification {
				t.Fatalf("decision = %q (%q), want held for no independent verification", dec.Outcome, dec.Reason)
			}
			if dec.Level != resultaccept.LevelRA0 {
				t.Fatalf("level = %q, want %q", dec.Level, resultaccept.LevelRA0)
			}
			// No verification was supplied, so none may be judged. A conversion that
			// injects its own verification would show up here as an exclusion or verifier.
			if len(dec.Excluded) != 0 || len(dec.Verifiers) != 0 || len(dec.Voices) != 0 {
				t.Fatalf("decision judged evidence that was never supplied: excluded=%v verifiers=%v voices=%v",
					dec.Excluded, dec.Verifiers, dec.Voices)
			}
		})
	}
}

// TestProviderSuccessIsAcceptedOnlyThroughVerification shows that acceptance needs an
// independent verification that resultaccept judges, and that verifications which
// fail the independence or agreement test do not accept the result.
func TestProviderSuccessIsAcceptedOnlyThroughVerification(t *testing.T) {
	lease := mustNewLease(t)
	claim, ok := ClaimFromEvent(Event{LeaseID: lease, Sequence: 2, Kind: EventCompleted})
	if !ok {
		t.Fatal("ClaimFromEvent(completed) ok = false, want a claim")
	}
	claimed := digestHex("claimed output")
	different := digestHex("a different output")

	cases := []struct {
		name        string
		verifs      []resultaccept.Verification
		wantOutcome resultaccept.Outcome
		wantReason  resultaccept.Reason
	}{
		{
			name: "independent validator accepts",
			verifs: []resultaccept.Verification{
				verifierAt("verifier-1", "principal-P1", "operator-O1", resultaccept.CheckValidatorAccepted, claimed),
			},
			wantOutcome: resultaccept.OutcomeAccepted,
			wantReason:  resultaccept.ReasonQuorumMet,
		},
		{
			name: "verifier sharing the producer principal justifies nothing",
			verifs: []resultaccept.Verification{
				verifierAt("verifier-1", "principal-P0", "operator-O1", resultaccept.CheckValidatorAccepted, claimed),
			},
			wantOutcome: resultaccept.OutcomeHeld,
			wantReason:  resultaccept.ReasonNoIndependentVerification,
		},
		{
			name: "an independent re-execution that differs holds the result",
			verifs: []resultaccept.Verification{
				verifierAt("verifier-1", "principal-P1", "operator-O1", resultaccept.CheckValidatorAccepted, claimed),
				verifierAt("verifier-2", "principal-P2", "operator-O2", resultaccept.CheckDiffers, different),
			},
			wantOutcome: resultaccept.OutcomeHeld,
			wantReason:  resultaccept.ReasonVerificationsDisagree,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := DecideProviderResult(claim, testBinding(lease), tc.verifs, resultaccept.OutputMechanical, raOnePolicy())
			if err != nil {
				t.Fatalf("DecideProviderResult() error = %v", err)
			}
			if dec.Outcome != tc.wantOutcome || dec.Reason != tc.wantReason {
				t.Fatalf("decision = %q (%q), want %q (%q)", dec.Outcome, dec.Reason, tc.wantOutcome, tc.wantReason)
			}
			if tc.wantOutcome == resultaccept.OutcomeAccepted {
				if dec.Level != resultaccept.LevelRA1 || len(dec.Voices) == 0 {
					t.Fatalf("accepted at level %q with %d voices, want RA-1 with a voice", dec.Level, len(dec.Voices))
				}
			}
		})
	}
}

// TestResultClaimFromProviderRefusesNonSuccessAndForeignLease checks that the
// conversion produces a claim only for a success on the bound lease.
func TestResultClaimFromProviderRefusesNonSuccessAndForeignLease(t *testing.T) {
	lease := mustNewLease(t)
	other := mustNewLease(t)
	b := testBinding(lease)

	if _, err := ResultClaimFromProvider(ProviderClaim{LeaseID: lease, Sequence: 3, Outcome: ClaimFailed}, b); !errors.Is(err, ErrNotSuccess) {
		t.Fatalf("failed claim error = %v, want ErrNotSuccess", err)
	}
	if _, err := ResultClaimFromProvider(ProviderClaim{LeaseID: lease, Sequence: 1, Outcome: ""}, b); !errors.Is(err, ErrNotSuccess) {
		t.Fatalf("claim with no outcome error = %v, want ErrNotSuccess", err)
	}
	if _, err := ResultClaimFromProvider(ProviderClaim{LeaseID: other, Sequence: 2, Outcome: ClaimSucceeded}, b); !errors.Is(err, ErrClaimLeaseMismatch) {
		t.Fatalf("foreign lease error = %v, want ErrClaimLeaseMismatch", err)
	}

	rc, err := ResultClaimFromProvider(ProviderClaim{LeaseID: lease, Sequence: 2, Outcome: ClaimSucceeded}, b)
	if err != nil {
		t.Fatalf("ResultClaimFromProvider() error = %v", err)
	}
	if rc.TaskID != b.TaskID || rc.AttemptID != b.AttemptID || rc.WorkerID != b.WorkerID || rc.OutputDigest != b.OutputDigest {
		t.Fatalf("result claim = %+v, want the bound task, attempt, worker and digest", rc)
	}
}

// TestResultClaimDoesNotAliasBindingDimensions checks that changing the binding after
// conversion does not change the claim that resultaccept receives.
func TestResultClaimDoesNotAliasBindingDimensions(t *testing.T) {
	lease := mustNewLease(t)
	b := testBinding(lease)
	rc, err := ResultClaimFromProvider(ProviderClaim{LeaseID: lease, Sequence: 2, Outcome: ClaimSucceeded}, b)
	if err != nil {
		t.Fatalf("ResultClaimFromProvider() error = %v", err)
	}
	b.Dimensions[resultaccept.DimensionPrincipal] = "changed-after-conversion"
	if rc.Dimensions[resultaccept.DimensionPrincipal] != "principal-P0" {
		t.Fatal("the result claim shares its dimension map with the binding")
	}
}

func mustNewLease(t *testing.T) ids.LeaseID {
	t.Helper()
	lease, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	return lease
}
