package verticalslice_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/control/resultacceptance"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
)

const aliceResultToken = "alice-token-0123456789abcdef"

func (w *world) clientGetResult(taskID ids.TaskID) (int, map[string]any, string) {
	w.t.Helper()
	auth, err := clientapi.NewStaticTokens([]clientapi.TokenEntry{
		{Principal: "alice", Role: clientapi.RoleClient, TokenSHA256: clientapi.HashToken(aliceResultToken)},
	})
	if err != nil {
		w.t.Fatal(err)
	}
	h, err := clientapi.NewHandler(clientapi.Config{Service: w.admissionService(10), Authenticator: auth})
	if err != nil {
		w.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/tasks/"+taskID.String()+"/result", nil)
	req.Header.Set("Authorization", "Bearer "+aliceResultToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	raw, _ := io.ReadAll(rec.Result().Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		w.t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	return rec.Code, body, string(raw)
}

// A Client reads the outcome through the Client API. The result appears only
// when the durable AcceptedResult exists, and it carries the accepted output
// digest but names no Worker, Lease,
// verifier, claim digest or policy. A rejected claim never shows as a result.
func TestClientReadsTheResultOnlyAfterAcceptance(t *testing.T) {
	w := newWorld(t)
	task := w.submit("client-result")

	if code, body, raw := w.clientGetResult(task); code != http.StatusConflict || body["code"] != "RESULT_NOT_READY" {
		t.Fatalf("before any work: %d %s", code, raw)
	}

	p := w.place(task, "worker-a")
	claim, outcome := w.execute(p, producesOutput("hello"), sourceFunc(quiet))
	if outcome.Status != executor.StatusCompleted {
		t.Fatalf("outcome = %+v", outcome)
	}
	w.report("worker-a", claim)
	// The Worker has claimed completion. That is not a result.
	if code, body, raw := w.clientGetResult(task); code != http.StatusConflict || body["code"] != "RESULT_NOT_READY" {
		t.Fatalf("after the claim: %d %s", code, raw)
	}

	if result := w.verifyAndAccept(p, "hello"); result.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result = %+v", result)
	}
	code, body, raw := w.clientGetResult(task)
	if code != http.StatusOK || body["schema"] != "eki.task-result/v1" || body["task_id"] != task.String() {
		t.Fatalf("after acceptance: %d %s", code, raw)
	}
	stored, ok, err := w.state.LoadAcceptedResult(w.ctx, task)
	if err != nil || !ok {
		t.Fatalf("LoadAcceptedResult() = %v, %v", ok, err)
	}
	if got := body["assurance"].(map[string]any)["profile"]; got != string(stored.Level()) {
		t.Fatalf("profile = %v, want the stored level %s", got, stored.Level())
	}
	// The Client reads the digest the acceptance recorded, in the contract's
	// Digest shape. This is what a caller pins the accepted result by.
	digest, ok := body["output_digest"].(map[string]any)
	if !ok || len(digest) != 2 || digest["algorithm"] != stored.OutputDigest().Algorithm() || digest["value"] != stored.OutputDigest().Hex() {
		t.Fatalf("output_digest = %v, want the accepted output digest %s", body["output_digest"], stored.OutputDigest())
	}
	if digest["value"] == stored.ClaimDigest().Hex() {
		t.Fatal("the output digest must not be the claim digest")
	}
	lower := strings.ToLower(raw)
	for _, leak := range []string{
		"worker", "lease", "fence", "attempt", "verifier", "claim", "policy",
		stored.ClaimDigest().Hex(), stored.PolicyDigest().Hex(),
	} {
		if strings.Contains(lower, leak) {
			t.Fatalf("the result mentions %q: %s", leak, raw)
		}
	}
}

func TestClientDoesNotSeeAHeldClaimAsAResult(t *testing.T) {
	w := newWorld(t)
	task := w.submit("client-held")

	p := w.place(task, "worker-a")
	claim, _ := w.execute(p, producesOutput("hello"), sourceFunc(quiet))
	w.report("worker-a", claim)
	// The verifiers disagree, so nothing is accepted and the Task is held.
	differs := reproduced(p, "verifier-2", "something else")
	differs.Check = resultaccept.CheckDiffers
	result, err := w.acceptance().Accept(w.ctx, resultacceptance.Request{
		AttemptID:     p.attempt,
		Verifications: []resultaccept.Verification{reproduced(p, "verifier-1", "hello"), differs},
	})
	if err != nil || result.Outcome() == resultaccept.OutcomeAccepted {
		t.Fatalf("Accept() = %+v, %v, want no acceptance", result, err)
	}
	code, body, raw := w.clientGetResult(task)
	if code != http.StatusConflict || body["code"] == nil {
		t.Fatalf("without an acceptance: %d %s", code, raw)
	}
	for _, leak := range []string{"disagree", "differs", "verifier", "digest", "worker"} {
		if strings.Contains(strings.ToLower(raw), leak) {
			t.Fatalf("the problem mentions %q: %s", leak, raw)
		}
	}
}
