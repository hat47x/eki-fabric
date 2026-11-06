package clientapi

import (
	"fmt"
	"net/http"
	"testing"
)

// The quota is freed by the Client acting, not by waiting: cancelling a Task it no
// longer needs lets the next submission through (#282).
func TestCancellingATaskFreesTheQuota(t *testing.T) {
	e := newEnv(t)

	var first string
	for i := 0; i < 3; i++ {
		res := e.submit(aliceToken, fmt.Sprintf("key-quota-%03d", i), validRequest)
		if res.status != http.StatusAccepted {
			t.Fatalf("submit %d status = %d", i, res.status)
		}
		if i == 0 {
			first = decode(t, res)["task_id"].(string)
		}
	}
	wantProblem(t, e.submit(aliceToken, "key-quota-full", validRequest), http.StatusTooManyRequests, "TASK_QUOTA_EXCEEDED")

	// Cancelling one Task gives the capacity back to the same Client.
	if res := e.cancel(aliceToken, first, "key-quota-cancel"); res.status != http.StatusOK && res.status != http.StatusAccepted {
		t.Fatalf("cancel = %d %s", res.status, res.body)
	}
	if res := e.submit(aliceToken, "key-quota-after", validRequest); res.status != http.StatusAccepted {
		t.Fatalf("submit after a cancel = %d %s, want 202", res.status, res.body)
	}
}

// A cancel by another principal does not free the first one's quota: the limit is
// per principal, not per deployment.
func TestAnotherPrincipalCannotFreeTheQuota(t *testing.T) {
	e := newEnv(t)

	var first string
	for i := 0; i < 3; i++ {
		res := e.submit(aliceToken, fmt.Sprintf("key-quota-%03d", i), validRequest)
		if res.status != http.StatusAccepted {
			t.Fatalf("submit %d status = %d", i, res.status)
		}
		if i == 0 {
			first = decode(t, res)["task_id"].(string)
		}
	}
	wantProblem(t, e.submit(aliceToken, "key-quota-full", validRequest), http.StatusTooManyRequests, "TASK_QUOTA_EXCEEDED")

	wantProblem(t, e.cancel(bobToken, first, "key-quota-foreign"), http.StatusNotFound, "TASK_NOT_FOUND")
	wantProblem(t, e.submit(aliceToken, "key-quota-again", validRequest), http.StatusTooManyRequests, "TASK_QUOTA_EXCEEDED")
}
