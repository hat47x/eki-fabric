package claimwire

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	queuesqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/sqlite"
	statesqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/agent/claimclient"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	server "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type tlsClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *tlsClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *tlsClock) Set(v time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.now = v }

func openCP(t *testing.T, path string) *statesqlite.Store {
	t.Helper()
	s, err := statesqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func openPending(t *testing.T, path string) *queuesqlite.Store {
	t.Helper()
	s, err := queuesqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func realAPI(t *testing.T, s *statesqlite.Store, clock *tlsClock) workerplane.ClaimClient {
	t.Helper()
	accept, err := claimaccept.New(claimaccept.Config{Attempts: s, Leases: s, Claims: s, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	plane, err := claimplane.New(accept)
	if err != nil {
		t.Fatal(err)
	}
	api, err := server.New(server.Config{Claims: plane})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

// Adapted from the root durable round-trip fixture. All state is seeded through
// public store APIs; policy and placement authority here are synthetic.
func seedTLSClaim(t *testing.T, s *statesqlite.Store, now time.Time, status resultclaim.Status, worker string, n int) (resultclaim.Claim, attemptstate.AttemptRecord) {
	t.Helper()
	ctx := context.Background()
	policy, err := policystate.NewActive(1, "policy-1", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n == 1 {
		if err := s.CompareAndSwapPolicy(ctx, 0, policy); err != nil {
			t.Fatal(err)
		}
	}
	task, err := taskstate.NewTask(ids.TaskID(fmt.Sprintf("tsk_%032d", n)), contract.SHA256([]byte("taskir")), now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	task.Phase = taskstate.TaskPhaseReady
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptstate.New(ids.AttemptID(fmt.Sprintf("atm_%032d", n)), task.ID, task.TaskIRDigest, 1, task.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	placing := attempt
	placing.Phase, placing.WorkerIdentity, placing.Revision = attemptstate.AttemptPhasePlacing, worker, attempt.Revision+1
	if err := s.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, placing); err != nil {
		t.Fatal(err)
	}
	reservation, err := s.ReserveLease(ctx, statestore.LeaseReservation{
		LeaseID: ids.LeaseID(fmt.Sprintf("les_%032d", n)), TaskID: task.ID, TaskIRDigest: task.TaskIRDigest,
		ExpectedTaskRevision: task.Revision, AttemptID: attempt.ID, ExpectedAttemptRevision: placing.Revision,
		WorkerIdentity: worker, PlacementTrustDomain: "example.org", PlacementAuthorityPrincipal: "placement-1",
		PlacementDecisionID: fmt.Sprintf("decision-%d", n), PlacementPayloadDigest: contract.SHA256([]byte("placement")),
		PlacementExpiresAt: now.Add(time.Hour), PolicyRevision: policy.Revision, ExpectedPolicyEpoch: policy.Epoch,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := s.LoadAttempt(ctx, attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := assigned
	dispatched.Phase, dispatched.UpdatedAt, dispatched.Revision = attemptstate.AttemptPhaseDispatched, now, assigned.Revision+1
	if err := s.CompareAndSwapAttempt(ctx, attempt.ID, assigned.Revision, dispatched); err != nil {
		t.Fatal(err)
	}
	var output contract.Digest
	if status == resultclaim.StatusCompleted {
		output = contract.SHA256([]byte("output"))
	}
	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: task.ID, TaskIRDigest: task.TaskIRDigest, AttemptID: attempt.ID, WorkerIdentity: worker,
		LeaseID: reservation.Lease.ID, FenceEpoch: reservation.Lease.FenceEpoch, WorkOrderDecision: "workorder-1",
		ExecutorDigest: contract.SHA256([]byte("executor")), Nonce: []byte("0123456789abcdef"),
		Status: status, OutputBytes: 42, OutputDigest: output, StartedAt: now, EndedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return claim, dispatched
}

func pendingQueue(t *testing.T, store claimqueue.Store, api workerplane.ClaimClient, clock *tlsClock) *claimqueue.Queue {
	t.Helper()
	client, err := claimclient.NewWithTimeout(api, workerplane.Principal{WorkerIdentity: "worker-a", TrustDomain: "example.org"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	q, err := claimqueue.New(claimqueue.Config{Store: store, Sender: client, Clock: clock, BaseDelay: time.Second, MaxDelay: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestTLSQueueRecovery(t *testing.T) {
	for _, status := range []resultclaim.Status{resultclaim.StatusCompleted, resultclaim.StatusReclaimed, resultclaim.StatusRunnerStuck} {
		for _, fault := range []string{"http-503", "drop-response", "wrong-receipt", "malformed-receipt", "timeout"} {
			t.Run(string(status)+"/"+fault, func(t *testing.T) {
				ctx := context.Background()
				c := testCodec(t)
				clock := &tlsClock{now: time.Date(2026, 10, 24, 0, 0, 2, 0, time.UTC)}
				cpPath, pendingPath := filepath.Join(t.TempDir(), "cp.db"), filepath.Join(t.TempDir(), "worker.db")
				state, pending := openCP(t, cpPath), openPending(t, pendingPath)
				claim, initial := seedTLSClaim(t, state, clock.Now().Add(-2*time.Second), status, "worker-a", 1)
				srv, handler := startTLSFixture(t, c, realAPI(t, state, clock), fault)
				api := tlsClient(c, srv, "fixture-a")
				response, err := api.http.Get(srv.URL + "/health") // Establish TLS before testing the deadline.
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				q := pendingQueue(t, pending, api, clock)
				if _, err := q.Enqueue(ctx, claim); err != nil {
					t.Fatal(err)
				}
				if report, err := q.DeliverDue(ctx, 1); err != nil || report != (claimqueue.Report{Retried: 1}) {
					t.Fatalf("failed delivery was not retained: %+v, %v", report, err)
				}
				if fault == "timeout" {
					select {
					case <-handler.cancelled:
					case <-time.After(5 * time.Second):
						t.Fatal("server request did not observe cancellation")
					}
				}
				srv.Close() // Drain handlers before closing both SQLite stores.
				queued, found, err := pending.Load(ctx, claim.AttemptID())
				if err != nil || !found || !queued.Claim.Equal(claim) || queued.Attempts != 1 || !queued.NextAt.After(clock.Now()) || queued.LastError == "" {
					t.Fatalf("claim or retry metadata lost: %+v, %v, %v", queued, found, err)
				}
				stored, committed, err := state.LoadResultClaim(ctx, claim.AttemptID())
				wantCommitted := fault != "http-503" && fault != "timeout"
				if err != nil || committed != wantCommitted || (committed && !stored.Equal(claim)) {
					t.Fatalf("unexpected CP claim: found=%v, %v", committed, err)
				}
				before, err := state.LoadAttempt(ctx, claim.AttemptID())
				if err != nil {
					t.Fatal(err)
				}
				if handler.calls.Load() != boolInt(wantCommitted) || handler.newClaims.Load() != boolInt(wantCommitted) {
					t.Fatal("fault repeated the backend call")
				}
				if err := pending.Close(); err != nil {
					t.Fatal(err)
				}
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
				state, pending = openCP(t, cpPath), openPending(t, pendingPath)
				restored, found, err := pending.Load(ctx, claim.AttemptID())
				if err != nil || !found || !restored.Claim.Equal(queued.Claim) || restored.Attempts != queued.Attempts || !restored.NextAt.Equal(queued.NextAt) || !restored.QueuedAt.Equal(queued.QueuedAt) || restored.LastError != queued.LastError {
					t.Fatalf("retry schedule changed on reopen: %+v, %v, %v", restored, found, err)
				}
				good, recovered := startTLSFixture(t, c, realAPI(t, state, clock), "")
				q = pendingQueue(t, pending, tlsClient(c, good, "fixture-a"), clock)
				if report, err := q.DeliverDue(ctx, 1); err != nil || report != (claimqueue.Report{}) || recovered.calls.Load() != 0 {
					t.Fatalf("retry sent before NextAt: %+v, %v", report, err)
				}
				clock.Set(restored.NextAt)
				if report, err := q.DeliverDue(ctx, 1); err != nil || report != (claimqueue.Report{Delivered: 1}) {
					t.Fatalf("recovery = %+v, %v", report, err)
				}
				good.Close()
				after, err := state.LoadAttempt(ctx, claim.AttemptID())
				if err != nil {
					t.Fatal(err)
				}
				wantPhase, wantRevision := initial.Phase, initial.Revision
				wantReason := attemptstate.CloseReasonNone
				switch status {
				case resultclaim.StatusCompleted:
					wantPhase, wantRevision = attemptstate.AttemptPhaseVerifying, initial.Revision+2
				case resultclaim.StatusReclaimed:
					wantPhase, wantRevision = attemptstate.AttemptPhaseClosed, initial.Revision+1
					wantReason = attemptstate.CloseReasonReclaimed
				}
				wantDecision := "new"
				if committed {
					wantDecision = "duplicate"
				}
				if after.Phase != wantPhase || after.Revision != wantRevision || after.CloseReason != wantReason || (committed && after != before) || recovered.calls.Load() != 1 || recovered.newClaims.Load() != boolInt(!committed) || recovered.decision.Load() != wantDecision {
					t.Fatalf("recovery changed CP state incorrectly: %+v", after)
				}
				if err := pending.Close(); err != nil {
					t.Fatal(err)
				}
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
				state, pending = openCP(t, cpPath), openPending(t, pendingPath)
				if _, found, err := pending.Load(ctx, claim.AttemptID()); err != nil || found {
					t.Fatalf("deletion not durable: %v, %v", found, err)
				}
				stored, found, err = state.LoadResultClaim(ctx, claim.AttemptID())
				if err != nil || !found || !stored.Equal(claim) {
					t.Fatalf("CP claim not durable: %v, %v", found, err)
				}
				final, err := state.LoadAttempt(ctx, claim.AttemptID())
				if err != nil || final != after {
					t.Fatalf("CP Attempt not durable: %+v, %v", final, err)
				}
				task, err := state.LoadTask(ctx, claim.TaskID())
				if err != nil || task.Phase != taskstate.TaskPhaseReady || task.TerminalOutcome != taskstate.TerminalOutcomeNone {
					t.Fatalf("claim became an AcceptedResult: %+v, %v", task, err)
				}
				t.Logf("committed before retry=%t; recovery=%s; phase=%s revision=%d; both SQLite stores reopened twice", committed, wantDecision, after.Phase, after.Revision)
			})
		}
	}
}

func boolInt(v bool) int32 {
	if v {
		return 1
	}
	return 0
}

func TestTLSWorkerBindingAndInputRejection(t *testing.T) {
	ctx := context.Background()
	c := testCodec(t)
	clock := &tlsClock{now: time.Date(2026, 10, 24, 0, 0, 2, 0, time.UTC)}
	state := openCP(t, filepath.Join(t.TempDir(), "cp.db"))
	a, beforeA := seedTLSClaim(t, state, clock.Now().Add(-2*time.Second), resultclaim.StatusCompleted, "worker-a", 1)
	b, beforeB := seedTLSClaim(t, state, clock.Now().Add(-2*time.Second), resultclaim.StatusRunnerStuck, "worker-b", 2)
	srv, handler := startTLSFixture(t, c, realAPI(t, state, clock), "")
	pa, pb := handler.sessions["fixture-a"], handler.sessions["fixture-b"]
	if _, err := tlsClient(c, srv, "missing").SubmitClaim(ctx, pa, a); err == nil {
		t.Fatal("unknown fixture session accepted")
	}
	if _, err := tlsClient(c, srv, "fixture-b").SubmitClaim(ctx, pa, a); err == nil {
		t.Fatal("unchanged A bytes accepted in B session")
	}
	if handler.calls.Load() != 0 {
		t.Fatal("invalid session binding reached CP")
	}
	cfg := copyClaimConfig(a)
	cfg.WorkerIdentity = pb.WorkerIdentity // Recompute the digest as a malicious fixture Worker B could.
	forged, err := resultclaim.NewClaim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tlsClient(c, srv, "fixture-b").SubmitClaim(ctx, pb, forged); err == nil {
		t.Fatal("CP accepted B's claim for A's Attempt")
	}
	for _, claim := range []resultclaim.Claim{a, b} {
		if _, found, err := state.LoadResultClaim(ctx, claim.AttemptID()); err != nil || found {
			t.Fatal("rejected input persisted a claim")
		}
	}
	afterA, err := state.LoadAttempt(ctx, a.AttemptID())
	if err != nil || afterA != beforeA {
		t.Fatal("rejection changed A's Attempt")
	}
	afterB, err := state.LoadAttempt(ctx, b.AttemptID())
	if err != nil || afterB != beforeB {
		t.Fatal("rejection changed B's Attempt")
	}
	api := tlsClient(c, srv, "fixture-a")
	wire, err := c.encodeClaim(pa, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, contentType string
		body                      []byte
		want                      int
	}{
		{"oversized", http.MethodPost, experimentalContentType, make([]byte, maxMessageBytes+1), http.StatusRequestEntityTooLarge},
		{"method", http.MethodGet, experimentalContentType, wire, http.StatusMethodNotAllowed},
		{"content-type", http.MethodPost, "application/json", wire, http.StatusUnsupportedMediaType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(ctx, tc.method, srv.URL+"/claim", bytes.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", tc.contentType)
			req.Header.Set("X-Experiment-Session", "fixture-a")
			res, err := api.http.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("HTTP %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
	untrusted := *api
	untrusted.http = &http.Client{Timeout: time.Second}
	if _, err := untrusted.SubmitClaim(ctx, pa, a); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	plain := *api
	plain.url = "http://127.0.0.1:1"
	if _, err := plain.SubmitClaim(ctx, pa, a); err == nil {
		t.Fatal("plaintext endpoint accepted")
	}
	if handler.calls.Load() != 1 {
		t.Fatal("invalid HTTP/TLS input reached CP")
	}
	acceptedA := beforeA
	for i, tc := range []struct {
		session string
		p       workerplane.Principal
		claim   resultclaim.Claim
	}{{"fixture-a", pa, a}, {"fixture-b", pb, b}} {
		ack, err := tlsClient(c, srv, tc.session).SubmitClaim(ctx, tc.p, tc.claim)
		if err != nil || ack.Decision != "new" {
			t.Fatalf("valid fixture Worker could not submit: %+v, %v", ack, err)
		}
		stored, found, err := state.LoadResultClaim(ctx, tc.claim.AttemptID())
		if err != nil || !found || !stored.Equal(tc.claim) {
			t.Fatalf("stored claim differs: %v", err)
		}
		currentA, err := state.LoadAttempt(ctx, a.AttemptID())
		if err != nil {
			t.Fatal(err)
		}
		currentB, err := state.LoadAttempt(ctx, b.AttemptID())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if currentA.Phase != attemptstate.AttemptPhaseVerifying || currentA.Revision != beforeA.Revision+2 || currentB != beforeB {
				t.Fatal("A submission changed the wrong Attempt")
			}
			acceptedA = currentA
		} else if currentA != acceptedA || currentB != beforeB {
			t.Fatal("B runner_stuck submission changed A or closed B")
		}
	}
}
