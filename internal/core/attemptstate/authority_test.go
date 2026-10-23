package attemptstate

import "testing"

func TestEndsExecutionAuthorityNamesEveryCloseReason(t *testing.T) {
	want := map[CloseReason]bool{
		CloseReasonResultAccepted:            true,
		CloseReasonResultRejected:            true,
		CloseReasonExecutionFailureConfirmed: true,
		CloseReasonReclaimed:                 true,
		CloseReasonCancelled:                 true,
		CloseReasonDeadline:                  true,
		CloseReasonBudgetExhausted:           true,
		// These do not show that the Worker stopped.
		CloseReasonNone:                  false,
		CloseReasonWorkerLost:            false,
		CloseReasonLeaseExpired:          false,
		CloseReasonSuperseded:            false,
		CloseReasonUnknownExternalEffect: false,
		CloseReasonPolicyRevoked:         false,
		CloseReasonStaleAttempt:          false,
	}
	for reason, ends := range want {
		if reason.EndsExecutionAuthority() != ends {
			t.Fatalf("%q.EndsExecutionAuthority() = %v, want %v", reason, !ends, ends)
		}
	}
	// A close reason added later is not silently treated as ending authority.
	if CloseReason("a_future_reason").EndsExecutionAuthority() {
		t.Fatal("an unknown close reason ends execution authority")
	}
}

func TestAuthorityEndingCloseReasonsMatchesThePredicateAndIsACopy(t *testing.T) {
	listed := AuthorityEndingCloseReasons()
	if len(listed) != 7 {
		t.Fatalf("listed %d reasons: %v", len(listed), listed)
	}
	for _, reason := range listed {
		if !reason.EndsExecutionAuthority() {
			t.Fatalf("%q is listed but does not end authority", reason)
		}
	}
	listed[0] = CloseReasonWorkerLost
	if AuthorityEndingCloseReasons()[0] == CloseReasonWorkerLost {
		t.Fatal("the list is not a copy")
	}
}

func TestOnlyAnEndWithoutAResultIsAnInterruption(t *testing.T) {
	want := map[CloseReason]bool{
		CloseReasonReclaimed: true, CloseReasonExecutionFailureConfirmed: true, CloseReasonWorkerLost: true,
		CloseReasonLeaseExpired: true, CloseReasonDeadline: true, CloseReasonBudgetExhausted: true,
		CloseReasonNone: false, CloseReasonResultAccepted: false, CloseReasonResultRejected: false,
		CloseReasonCancelled: false, CloseReasonSuperseded: false, CloseReasonUnknownExternalEffect: false,
		CloseReasonPolicyRevoked: false, CloseReasonStaleAttempt: false,
	}
	for reason, interrupted := range want {
		if reason.IsInterruption() != interrupted {
			t.Fatalf("%q.IsInterruption() = %v, want %v", reason, !interrupted, interrupted)
		}
	}
	if CloseReason("a_future_reason").IsInterruption() {
		t.Fatal("an unknown close reason is an interruption")
	}
}
