package clientapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/acceptedresult"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// fakeResults serves AcceptedResults without the acceptance commit, which needs
// Attempts, Leases and claims that these handler tests have no use for. The
// commit itself is covered by the store conformance and the vertical slice.
type fakeResults struct {
	statestore.ResultAcceptanceStore
	records map[ids.TaskID]acceptedresult.Record
	err     error
}

func newFakeResults() *fakeResults {
	return &fakeResults{records: map[ids.TaskID]acceptedresult.Record{}}
}

func (f *fakeResults) LoadAcceptedResult(_ context.Context, id ids.TaskID) (acceptedresult.Record, bool, error) {
	if f.err != nil {
		return acceptedresult.Record{}, false, f.err
	}
	r, ok := f.records[id]
	return r, ok, nil
}

type errString string

func (e errString) Error() string { return string(e) }

func (e *env) result(token, id string) response {
	e.t.Helper()
	return e.do(http.MethodGet, "/v1/tasks/"+id+"/result", token, nil, "")
}

// finish moves the stored Task to a terminal outcome the way the owning
// component would. Completion goes through the accepted-result commit — the
// generic CAS refuses a completed write since #365, because only the commit
// carries the evidence. The other outcomes have no evidence behind them and
// still move by CAS.
func (e *env) finish(id string, outcome taskstate.TerminalOutcome) {
	e.t.Helper()
	tid, err := ids.ParseTaskID(id)
	if err != nil {
		e.t.Fatal(err)
	}
	if outcome != taskstate.TerminalOutcomeCompleted {
		a, err := e.store.LoadAdmission(context.Background(), tid)
		if err != nil {
			e.t.Fatal(err)
		}
		next := a.Task
		next.Phase, next.TerminalOutcome = taskstate.TaskPhaseTerminal, outcome
		next.Revision++
		if err := e.store.CompareAndSwapTask(context.Background(), tid, a.Task.Revision, next); err != nil {
			e.t.Fatal(err)
		}
		return
	}
	// The completion chain: reserve a lease, walk its Attempt to verifying,
	// record the Worker's claim, then commit the accepted result. The same
	// chain the acceptance fixture builds, against the env's own store.
	ctx := context.Background()
	// Load the Task directly: the admission snapshot would be behind the
	// store if a test moved the Task before finishing it.
	fresh, err := e.store.LoadTask(ctx, tid)
	if err != nil {
		e.t.Fatal(err)
	}
	// A submitted Task sits in admitted; the completion chain starts from a
	// placement, so walk it to ready first (the way dispatch preparation
	// would).
	ready := fresh
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = fresh.UpdatedAt.Add(time.Second)
	if err := e.store.CompareAndSwapTask(ctx, tid, fresh.Revision, ready); err != nil {
		e.t.Fatal(err)
	}
	task := ready
	ensureResultPolicy(e.t, e.store)
	// The Attempt is created first and walked to placing, the way placement
	// does; the reservation then binds the lease to it.
	attemptID, err := ids.NewAttemptID()
	if err != nil {
		e.t.Fatal(err)
	}
	attempt, err := attemptstate.New(attemptID, task.ID, task.TaskIRDigest, 1, task.UpdatedAt.Add(time.Second))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.CreateAttempt(ctx, attempt); err != nil {
		e.t.Fatal(err)
	}
	placing := attempt
	placing.Phase = attemptstate.AttemptPhasePlacing
	placing.WorkerIdentity = "worker-a"
	placing.Revision++
	placing.UpdatedAt = attempt.UpdatedAt.Add(time.Nanosecond)
	if err := e.store.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, placing); err != nil {
		e.t.Fatal(err)
	}
	attempt = placing
	reserved, err := e.store.ReserveLease(ctx, resultLeaseReservation(e.t, task, "placement-"+id, attemptID, attempt.Revision))
	if err != nil {
		e.t.Fatal(err)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	// The reservation moves the Attempt to assigned and stamps the lease on
	// it; reload to carry on from the store's own record.
	attempt, err = e.store.LoadAttempt(ctx, attemptID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, phase := range []attemptstate.AttemptPhase{
		attemptstate.AttemptPhaseDispatched, attemptstate.AttemptPhaseClaimed, attemptstate.AttemptPhaseVerifying,
	} {
		next := attempt
		next.Phase = phase
		next.Revision++
		next.UpdatedAt = attempt.UpdatedAt.Add(time.Second)
		if err := e.store.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, next); err != nil {
			e.t.Fatalf("move Attempt to %s: %v", phase, err)
		}
		attempt = next
	}
	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: task.ID, TaskIRDigest: task.TaskIRDigest, AttemptID: attempt.ID,
		WorkerIdentity: attempt.WorkerIdentity, LeaseID: reserved.Lease.ID, FenceEpoch: reserved.Lease.FenceEpoch,
		WorkOrderDecision: "workorder-" + id, ExecutorDigest: contract.SHA256([]byte("executor")),
		Nonce: []byte("0123456789abcdef"), Status: resultclaim.StatusCompleted, ExitCode: 0,
		OutputBytes: 5, OutputDigest: contract.SHA256([]byte("hello")),
		StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC().Add(time.Second),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.ApplyResultClaim(ctx, statestore.ResultClaimAcceptance{Claim: claim, Record: true}); err != nil {
		e.t.Fatal(err)
	}
	closed := attempt
	closed.Phase = attemptstate.AttemptPhaseClosed
	closed.CloseReason = attemptstate.CloseReasonResultAccepted
	closed.Revision++
	closed.UpdatedAt = attempt.UpdatedAt.Add(time.Second)
	completed := task
	completed.Phase = taskstate.TaskPhaseTerminal
	completed.TerminalOutcome = taskstate.TerminalOutcomeCompleted
	completed.Revision++
	completed.UpdatedAt = task.UpdatedAt.Add(time.Second)
	result, err := acceptedresult.New(acceptedresult.Config{
		TaskID: task.ID, AttemptID: attempt.ID,
		ClaimDigest: claim.Digest(), OutputDigest: claim.OutputDigest(),
		Level: resultaccept.LevelRA1, PolicyDigest: contract.SHA256([]byte("result policy")),
		Verifiers: []string{"verifier-b", "verifier-a"},
		AcceptedAt: attempt.UpdatedAt.Add(time.Second),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.CommitAcceptedResult(ctx, statestore.ResultAcceptanceCommit{
		Result: result, ExpectedAttemptRevision: attempt.Revision, NextAttempt: closed,
		ExpectedTaskRevision: task.Revision, NextTask: completed,
	}); err != nil {
		e.t.Fatal(err)
	}
}

func acceptedRecord(t *testing.T, id string) acceptedresult.Record {
	t.Helper()
	r, err := acceptedresult.New(acceptedresult.Config{
		TaskID: ids.TaskID(id), AttemptID: "atm_00000000000000000000000000000001",
		ClaimDigest: contract.SHA256([]byte("claim")), OutputDigest: contract.SHA256([]byte("output")),
		Level: resultaccept.LevelRA2, PolicyDigest: contract.SHA256([]byte("policy")),
		Verifiers:  []string{"verifier-secret-a", "verifier-secret-b"},
		AcceptedAt: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func submitFor(t *testing.T, e *env, token, key string) string {
	t.Helper()
	res := e.submit(token, key, validRequest)
	if res.status != http.StatusAccepted {
		t.Fatalf("submit status = %d\n%s", res.status, res.body)
	}
	return decode(t, res)["task_id"].(string)
}

func TestResultIsNotReadyUntilTheTaskIsTerminal(t *testing.T) {
	results := newFakeResults()
	e := newEnvWith(t, results)
	id := submitFor(t, e, aliceToken, "key-00000001")

	p := wantProblem(t, e.result(aliceToken, id), http.StatusConflict, "RESULT_NOT_READY")
	if p["retryable"] != true || p["task_id"] != id {
		t.Fatalf("problem = %v", p)
	}
	// A record without a terminal Task must never be shown: the result is only
	// visible once the Task itself says it is finished.
	results.records[ids.TaskID(id)] = acceptedRecord(t, id)
	wantProblem(t, e.result(aliceToken, id), http.StatusConflict, "RESULT_NOT_READY")
}

func TestAcceptedResultIsProjectedWithoutInternals(t *testing.T) {
	results := newFakeResults()
	e := newEnvWith(t, results)
	id := submitFor(t, e, aliceToken, "key-00000001")
	results.records[ids.TaskID(id)] = acceptedRecord(t, id)
	e.finish(id, taskstate.TerminalOutcomeCompleted)

	res := e.result(aliceToken, id)
	if res.status != http.StatusOK {
		t.Fatalf("status = %d\n%s", res.status, res.body)
	}
	if ct := res.header.Get("Content-Type"); ct != jsonContentType {
		t.Fatalf("Content-Type = %q", ct)
	}
	body := decode(t, res)
	checkAgainstSpec(t, "TaskResult", body)
	if body["schema"] != "eki.task-result/v1" || body["task_id"] != id {
		t.Fatalf("body = %s", res.body)
	}
	if arts, ok := body["artifacts"].([]any); !ok || len(arts) != 0 {
		t.Fatalf("artifacts = %v, want an empty array", body["artifacts"])
	}
	assurance, ok := body["assurance"].(map[string]any)
	if !ok || assurance["status"] != "satisfied" || assurance["profile"] != "RA-2" || len(assurance) != 2 {
		t.Fatalf("assurance = %v", body["assurance"])
	}
	// The digest is the one in the durable record, in the spec Digest shape, and
	// nothing else about the output is exposed.
	want := results.records[ids.TaskID(id)].OutputDigest()
	digest, ok := body["output_digest"].(map[string]any)
	if !ok || len(digest) != 2 || digest["algorithm"] != "sha256" || digest["value"] != want.Hex() {
		t.Fatalf("output_digest = %v, want sha256 %s", body["output_digest"], want.Hex())
	}
	if len(body) != 5 {
		t.Fatalf("unexpected keys in %s", res.body)
	}

	// Neither identities nor digests nor evidence may appear anywhere in it.
	text := strings.ToLower(string(res.body))
	for _, forbidden := range []string{
		"worker", "lease", "fence", "attempt", "atm_", "verifier", "policy", "claim",
		"alice", "principal", "reason",
		contract.SHA256([]byte("claim")).Hex(),
		contract.SHA256([]byte("policy")).Hex(),
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the result mentions %q: %s", forbidden, res.body)
		}
	}
	for _, key := range []string{"worker_identity", "lease_id", "fence_epoch", "attempt_id", "verifiers", "policy_digest", "claim_digest", "verification_digests"} {
		if _, present := body[key]; present {
			t.Errorf("forbidden key %q is present", key)
		}
	}
}

func TestCompletedStatusAloneIsNotAnAcceptedResult(t *testing.T) {
	e := newEnvWith(t, newFakeResults())
	id := submitFor(t, e, aliceToken, "key-00000001")
	e.finish(id, taskstate.TerminalOutcomeCompleted)

	if st := decode(t, e.get(aliceToken, id)); st["status"] != "completed" {
		t.Fatalf("status = %v", st["status"])
	}
	wantProblem(t, e.result(aliceToken, id), http.StatusConflict, "RESULT_NOT_AVAILABLE")
}

func TestUnsuccessfulTerminalTasksHaveNoResult(t *testing.T) {
	for _, outcome := range []taskstate.TerminalOutcome{
		taskstate.TerminalOutcomeCancelled, taskstate.TerminalOutcomeFailed,
		taskstate.TerminalOutcomeExhausted, taskstate.TerminalOutcomeAbandoned,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			results := newFakeResults()
			e := newEnvWith(t, results)
			id := submitFor(t, e, aliceToken, "key-00000001")
			// Even a stray record must not turn an unsuccessful Task into a result.
			results.records[ids.TaskID(id)] = acceptedRecord(t, id)
			e.finish(id, outcome)

			res := e.result(aliceToken, id)
			p := wantProblem(t, res, http.StatusConflict, "RESULT_NOT_AVAILABLE")
			if p["retryable"] != nil {
				t.Errorf("retryable = %v: this Task will never have a result", p["retryable"])
			}
			for _, leak := range []string{"reject", "ra-", "verif", "policy"} {
				if strings.Contains(strings.ToLower(string(res.body)), leak) {
					t.Errorf("the problem mentions %q: %s", leak, res.body)
				}
			}
		})
	}
}

func TestResultOwnershipLeavesNoOracle(t *testing.T) {
	results := newFakeResults()
	e := newEnvWith(t, results)
	id := submitFor(t, e, aliceToken, "key-00000001")
	results.records[ids.TaskID(id)] = acceptedRecord(t, id)
	e.finish(id, taskstate.TerminalOutcomeCompleted)

	other := e.result(bobToken, id)
	absent := e.result(bobToken, "tsk_00000000000000000000000000000099")
	malformed := e.result(bobToken, "not-a-task")
	for name, r := range map[string]response{"someone else's": other, "absent": absent, "malformed": malformed} {
		p := wantProblem(t, r, http.StatusNotFound, "TASK_NOT_FOUND")
		if p["task_id"] != nil {
			t.Errorf("%s: the 404 names a task: %v", name, p)
		}
	}
	if string(other.body) != string(absent.body) {
		t.Fatalf("someone else's Task is distinguishable from an absent one:\n%s\n%s", other.body, absent.body)
	}
	// The same holds before the Task is finished.
	id2 := submitFor(t, e, aliceToken, "key-00000002")
	wantProblem(t, e.result(bobToken, id2), http.StatusNotFound, "TASK_NOT_FOUND")
}

func TestResultAuthentication(t *testing.T) {
	e := newEnvWith(t, newFakeResults())
	id := submitFor(t, e, aliceToken, "key-00000001")

	wantProblem(t, e.result("", id), http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
	wantProblem(t, e.result("wrong-token-0123456789abcdef", id), http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
	// Admin credentials are not Client credentials, as for every operation.
	wantProblem(t, e.result(adminToken, id), http.StatusForbidden, "CLIENT_ROLE_REQUIRED")
}

func TestResultMethodNotAllowed(t *testing.T) {
	e := newEnvWith(t, newFakeResults())
	id := submitFor(t, e, aliceToken, "key-00000001")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		res := e.do(method, "/v1/tasks/"+id+"/result", aliceToken, map[string]string{"Idempotency-Key": "key-00000009"}, "")
		wantProblem(t, res, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		if res.header.Get("Allow") != http.MethodGet {
			t.Errorf("%s: Allow = %q", method, res.header.Get("Allow"))
		}
	}
}

func TestResultStoreFailureIsOpaque(t *testing.T) {
	results := newFakeResults()
	e := newEnvWith(t, results)
	id := submitFor(t, e, aliceToken, "key-00000001")
	e.finish(id, taskstate.TerminalOutcomeCompleted)
	results.err = errString("sqlite: disk I/O error at /var/secret/path")

	res := e.result(aliceToken, id)
	wantProblem(t, res, http.StatusInternalServerError, "INTERNAL_ERROR")
	if strings.Contains(string(res.body), "secret") || strings.Contains(string(res.body), "sqlite") {
		t.Fatalf("the internal error leaks detail: %s", res.body)
	}
}

// The digest is an additive, optional field of eki.task-result/v1. It must stay
// optional so that existing readers of the v1 contract keep working.
func TestSpecDeclaresTheOutputDigestAsOptional(t *testing.T) {
	schema := specSchema(t, "TaskResult")
	props := schema["properties"].(map[string]any)
	def, ok := props["output_digest"].(map[string]any)
	if !ok || def["$ref"] != "#/components/schemas/Digest" {
		t.Fatalf("output_digest = %v, want a reference to Digest", props["output_digest"])
	}
	for _, r := range schema["required"].([]any) {
		if r == "output_digest" {
			t.Fatal("output_digest must stay optional within eki.task-result/v1")
		}
	}
}

// Every response that is not a result must stay free of the digest, even when
// a durable record exists.
func TestOutputDigestIsOnlyInTheSuccessfulResult(t *testing.T) {
	hex := contract.SHA256([]byte("output")).Hex()
	results := newFakeResults()
	e := newEnvWith(t, results)
	id := submitFor(t, e, aliceToken, "key-00000001")
	results.records[ids.TaskID(id)] = acceptedRecord(t, id)

	notReady := e.result(aliceToken, id)
	wantProblem(t, notReady, http.StatusConflict, "RESULT_NOT_READY")
	e.finish(id, taskstate.TerminalOutcomeCancelled)
	notAvailable := e.result(aliceToken, id)
	wantProblem(t, notAvailable, http.StatusConflict, "RESULT_NOT_AVAILABLE")
	notFound := e.result(bobToken, id)
	results.err = errString("boom")
	for name, r := range map[string]response{"not ready": notReady, "not available": notAvailable, "not found": notFound, "status": e.get(aliceToken, id)} {
		if strings.Contains(string(r.body), hex) || strings.Contains(string(r.body), "output_digest") {
			t.Errorf("%s: the response carries the digest: %s", name, r.body)
		}
	}
}

// ensureResultPolicy records the policy the acceptance chain checks.
func ensureResultPolicy(t testing.TB, store statestore.PolicyStore) {
	t.Helper()
	if _, err := store.LoadCurrentPolicy(context.Background()); err == nil {
		return
	}
	initial, err := policystate.NewActive(1, "policy-42", time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapPolicy(context.Background(), 0, initial); err != nil {
		t.Fatal(err)
	}
}

// resultLeaseReservation is the lease reservation the completion chain needs,
// built here so these handler tests do not depend on the conformance package.
func resultLeaseReservation(t testing.TB, task taskstate.TaskRecord, decisionID string, attemptID ids.AttemptID, attemptRevision uint64) statestore.LeaseReservation {
	t.Helper()
	now := time.Now().UTC()
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	return statestore.LeaseReservation{
		LeaseID:                     leaseID,
		TaskID:                      task.ID,
		TaskIRDigest:                task.TaskIRDigest,
		ExpectedTaskRevision:        task.Revision,
		AttemptID:                   attemptID,
		ExpectedAttemptRevision:     attemptRevision,
		WorkerIdentity:              "worker-a",
		PlacementTrustDomain:        "local.example",
		PlacementAuthorityPrincipal: "control-plane/plc-1",
		PlacementDecisionID:         decisionID,
		PlacementPayloadDigest:      contract.SHA256([]byte("payload:" + decisionID)),
		PlacementExpiresAt:          now.Add(2 * time.Minute),
		PolicyRevision:              "policy-42",
		ExpectedPolicyEpoch:         1,
		IssuedAt:                    now,
		ExpiresAt:                   now.Add(time.Minute),
	}
}
