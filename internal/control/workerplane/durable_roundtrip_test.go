package workerplane_test

import (
	"context"
	"errors"
	"path/filepath"
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

type roundtripClock struct{ now time.Time }

func (c *roundtripClock) Now() time.Time { return c.now }

var interrupted = errors.New("synthetic interruption")

// These faults are message/persistence boundaries, not a network transport or
// authentication implementation. The test supplies the trusted principal.
type interruptedAPI struct {
	workerplane.ClaimClient
	before, after           bool
	emptyBefore, emptyAfter bool
	acks                    []workerplane.ClaimAck
}

func (a *interruptedAPI) SubmitClaim(ctx context.Context, p workerplane.Principal, c resultclaim.Claim) (workerplane.ClaimAck, error) {
	if a.emptyBefore {
		return workerplane.ClaimAck{}, nil
	}
	if a.before {
		return workerplane.ClaimAck{}, interrupted
	}
	ack, err := a.ClaimClient.SubmitClaim(ctx, p, c)
	if err == nil {
		a.acks = append(a.acks, ack)
		if a.after {
			return workerplane.ClaimAck{}, interrupted
		}
		if a.emptyAfter {
			return workerplane.ClaimAck{}, nil
		}
	}
	return ack, err
}

type interruptedClaims struct{ statestore.ResultClaimStore }

func (s interruptedClaims) ApplyResultClaim(context.Context, statestore.ResultClaimAcceptance) error {
	return interrupted
}

type interruptedDelete struct{ claimqueue.Store }

func (s interruptedDelete) Delete(context.Context, ids.AttemptID, string) error { return interrupted }

func openState(t *testing.T, path string) *statesqlite.Store {
	t.Helper()
	s, err := statesqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func openQueue(t *testing.T, path string) *queuesqlite.Store {
	t.Helper()
	s, err := queuesqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func durableServer(t *testing.T, s *statesqlite.Store, clock *roundtripClock, failCommit bool) *server.Server {
	t.Helper()
	var claims statestore.ResultClaimStore = s
	if failCommit {
		claims = interruptedClaims{s}
	}
	accept, err := claimaccept.New(claimaccept.Config{Attempts: s, Leases: s, Claims: claims, Clock: clock})
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

func durableQueue(t *testing.T, s claimqueue.Store, api workerplane.ClaimClient, clock *roundtripClock) *claimqueue.Queue {
	t.Helper()
	client, err := claimclient.New(api, workerplane.Principal{WorkerIdentity: "worker-a", TrustDomain: "example.org"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := claimqueue.New(claimqueue.Config{Store: s, Sender: client, Clock: clock, BaseDelay: time.Second, MaxDelay: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// Seed through public store APIs: no SQL shortcuts or fabricated stored phases.
// The policy/placement inputs are synthetic, not verified signing authority.
func seedClaim(t *testing.T, s *statesqlite.Store, now time.Time, status resultclaim.Status) (resultclaim.Claim, attemptstate.AttemptRecord) {
	t.Helper()
	ctx := context.Background()
	policy, err := policystate.NewActive(1, "policy-1", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompareAndSwapPolicy(ctx, 0, policy); err != nil {
		t.Fatal(err)
	}
	task, err := taskstate.NewTask(ids.TaskID("tsk_00000000000000000000000000000001"), contract.SHA256([]byte("taskir")), now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	task.Phase = taskstate.TaskPhaseReady
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptstate.New(ids.AttemptID("atm_00000000000000000000000000000001"), task.ID, task.TaskIRDigest, 1, task.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	placing := attempt
	placing.Phase, placing.WorkerIdentity = attemptstate.AttemptPhasePlacing, "worker-a"
	placing.Revision++
	if err := s.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, placing); err != nil {
		t.Fatal(err)
	}
	reservation, err := s.ReserveLease(ctx, statestore.LeaseReservation{
		LeaseID: ids.LeaseID("les_00000000000000000000000000000001"), TaskID: task.ID, TaskIRDigest: task.TaskIRDigest,
		ExpectedTaskRevision: task.Revision, AttemptID: attempt.ID, ExpectedAttemptRevision: placing.Revision,
		WorkerIdentity: "worker-a", PlacementTrustDomain: "example.org", PlacementAuthorityPrincipal: "placement-1",
		PlacementDecisionID: "decision-1", PlacementPayloadDigest: contract.SHA256([]byte("placement")),
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
	dispatched.Phase, dispatched.UpdatedAt = attemptstate.AttemptPhaseDispatched, now
	dispatched.Revision++
	if err := s.CompareAndSwapAttempt(ctx, attempt.ID, assigned.Revision, dispatched); err != nil {
		t.Fatal(err)
	}
	c, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: task.ID, TaskIRDigest: task.TaskIRDigest, AttemptID: attempt.ID, WorkerIdentity: "worker-a",
		LeaseID: reservation.Lease.ID, FenceEpoch: reservation.Lease.FenceEpoch, WorkOrderDecision: "workorder-1",
		ExecutorDigest: contract.SHA256([]byte("executor")), Nonce: []byte("0123456789abcdef"),
		Status: status, OutputBytes: 42, StartedAt: now, EndedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, dispatched
}

func TestDurableClaimRoundtripAcrossInterruptedBoundaries(t *testing.T) {
	for _, status := range []resultclaim.Status{resultclaim.StatusCompleted, resultclaim.StatusReclaimed, resultclaim.StatusRunnerStuck} {
		for _, boundary := range []string{"before_receive", "before_claim_commit", "after_commit_before_ack", "after_ack_before_delete", "before_receive_empty_ack", "after_commit_empty_ack"} {
			t.Run(string(status)+"/"+boundary, func(t *testing.T) {
				ctx := context.Background()
				clock := &roundtripClock{time.Date(2026, 10, 21, 0, 0, 2, 0, time.UTC)}
				dir := t.TempDir()
				statePath, queuePath := filepath.Join(dir, "control.db"), filepath.Join(dir, "worker.db")
				state, pending := openState(t, statePath), openQueue(t, queuePath)
				claim, initial := seedClaim(t, state, clock.now.Add(-2*time.Second), status)
				api := &interruptedAPI{ClaimClient: durableServer(t, state, clock, boundary == "before_claim_commit"), before: boundary == "before_receive", after: boundary == "after_commit_before_ack", emptyBefore: boundary == "before_receive_empty_ack", emptyAfter: boundary == "after_commit_empty_ack"}
				var queueStore claimqueue.Store = pending
				if boundary == "after_ack_before_delete" {
					queueStore = interruptedDelete{pending}
				}
				q := durableQueue(t, queueStore, api, clock)
				if duplicate, err := q.Enqueue(ctx, claim); err != nil || duplicate {
					t.Fatalf("enqueue = %v, %v", duplicate, err)
				}
				report, err := q.DeliverDue(ctx, 1)
				if boundary == "after_ack_before_delete" {
					if !errors.Is(err, interrupted) || report.Delivered != 0 {
						t.Fatalf("delete failure = %+v, %v", report, err)
					}
				} else if err != nil || report.Retried != 1 {
					t.Fatalf("interruption = %+v, %v", report, err)
				}
				queued, found, err := pending.Load(ctx, claim.AttemptID())
				if err != nil || !found || !queued.Claim.Equal(claim) {
					t.Fatalf("pending lost after interruption: %+v, %v, %v", queued, found, err)
				}
				if boundary == "after_ack_before_delete" {
					if queued.Attempts != 0 || !queued.NextAt.Equal(clock.now) || queued.LastError != "" {
						t.Fatalf("failed deletion changed retry metadata: %+v", queued)
					}
				} else if queued.Attempts != 1 || !queued.NextAt.After(clock.now) || queued.LastError == "" {
					t.Fatalf("failed delivery did not persist backoff: %+v", queued)
				}
				committed := boundary == "after_commit_before_ack" || boundary == "after_ack_before_delete" || boundary == "after_commit_empty_ack"
				stored, found, err := state.LoadResultClaim(ctx, claim.AttemptID())
				if err != nil || found != committed || (found && !stored.Equal(claim)) {
					t.Fatalf("claim before reopen: %v, %v", found, err)
				}
				before, err := state.LoadAttempt(ctx, claim.AttemptID())
				if err != nil {
					t.Fatal(err)
				}
				if !committed {
					want := initial
					if boundary == "before_claim_commit" && status == resultclaim.StatusCompleted {
						want.Phase = attemptstate.AttemptPhaseClaimed
						want.Revision++
					}
					if before.Phase != want.Phase || before.Revision != want.Revision {
						t.Fatalf("partial transition = %+v, want %+v", before, want)
					}
				}
				if err := pending.Close(); err != nil {
					t.Fatal(err)
				}
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
				state, pending = openState(t, statePath), openQueue(t, queuePath)
				api = &interruptedAPI{ClaimClient: durableServer(t, state, clock, false)}
				q = durableQueue(t, pending, api, clock)
				if duplicate, err := q.Enqueue(ctx, claim); err != nil || !duplicate {
					t.Fatalf("re-enqueue = %v, %v", duplicate, err)
				}
				restored, found, err := pending.Load(ctx, claim.AttemptID())
				if err != nil || !found || restored.Attempts != queued.Attempts || !restored.NextAt.Equal(queued.NextAt) || restored.LastError != queued.LastError {
					t.Fatalf("retry schedule changed: %+v, %v", restored, err)
				}
				if queued.NextAt.After(clock.now) {
					report, err := q.DeliverDue(ctx, 1)
					if err != nil || report != (claimqueue.Report{}) || len(api.acks) != 0 {
						t.Fatalf("early retry: %+v, %v", report, err)
					}
				}
				clock.now = queued.NextAt
				report, err = q.DeliverDue(ctx, 1)
				if err != nil || report.Delivered != 1 || len(api.acks) != 1 {
					t.Fatalf("retry = %+v, %v, acks=%+v", report, err, api.acks)
				}
				wantDecision := string(resultclaim.KindNew)
				if committed {
					wantDecision = string(resultclaim.KindDuplicate)
				}
				if api.acks[0].Decision != wantDecision {
					t.Fatalf("decision = %+v, want %s", api.acks[0], wantDecision)
				}
				after, err := state.LoadAttempt(ctx, claim.AttemptID())
				if err != nil {
					t.Fatal(err)
				}
				wantPhase, wantRevision := attemptstate.AttemptPhaseDispatched, initial.Revision
				wantReason := attemptstate.CloseReasonNone
				switch status {
				case resultclaim.StatusCompleted:
					wantPhase = attemptstate.AttemptPhaseVerifying
					wantRevision += 2
				case resultclaim.StatusReclaimed:
					wantPhase = attemptstate.AttemptPhaseClosed
					wantReason = attemptstate.CloseReasonReclaimed
					wantRevision++
				}
				if after.Phase != wantPhase || after.Revision != wantRevision || after.CloseReason != wantReason {
					t.Fatalf("attempt = %+v, want %s revision %d reason %s", after, wantPhase, wantRevision, wantReason)
				}
				if committed && after != before {
					t.Fatal("duplicate delivery changed the committed Attempt")
				}
				if api.acks[0].VerificationRequired != (status == resultclaim.StatusCompleted) {
					t.Fatalf("verification ack = %+v", api.acks[0])
				}
				// Reopen again to prove deletion and CP state, not cached observations.
				if err := pending.Close(); err != nil {
					t.Fatal(err)
				}
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
				state, pending = openState(t, statePath), openQueue(t, queuePath)
				if _, found, err := pending.Load(ctx, claim.AttemptID()); err != nil || found {
					t.Fatalf("queue deletion not durable: %v, %v", found, err)
				}
				stored, found, err = state.LoadResultClaim(ctx, claim.AttemptID())
				if err != nil || !found || !stored.Equal(claim) {
					t.Fatalf("CP claim not durable: %v, %v", found, err)
				}
				restoredAttempt, err := state.LoadAttempt(ctx, claim.AttemptID())
				if err != nil || restoredAttempt != after {
					t.Fatalf("CP Attempt not durable: %+v, %v", restoredAttempt, err)
				}
				task, err := state.LoadTask(ctx, claim.TaskID())
				if err != nil || task.Phase != taskstate.TaskPhaseReady || task.TerminalOutcome != taskstate.TerminalOutcomeNone {
					t.Fatalf("claim became an accepted Task result: %+v, %v", task, err)
				}
			})
		}
	}
}
