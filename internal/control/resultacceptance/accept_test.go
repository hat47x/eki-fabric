package resultacceptance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var baseTime = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func digestOf(s string) string { return contract.SHA256([]byte(s)).Hex() }

// outputDigest is the output the Worker claims in every test.
var outputDigest = contract.SHA256([]byte("hello"))

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type policySource struct {
	policy ResultPolicy
	err    error
}

func (p policySource) ResultPolicy(context.Context, ids.TaskID) (ResultPolicy, error) {
	return p.policy, p.err
}

type producerSource struct {
	dims map[resultaccept.Dimension]string
	err  error
}

func (p producerSource) ProducerDimensions(context.Context, string) (map[resultaccept.Dimension]string, error) {
	return p.dims, p.err
}

func producerDims() map[resultaccept.Dimension]string {
	return map[resultaccept.Dimension]string{
		resultaccept.DimensionPrincipal:      "principal-P0",
		resultaccept.DimensionOperator:       "operator-O0",
		resultaccept.DimensionExecutorDigest: digestOf("executor-E0"),
	}
}

func twoVoicePolicy() ResultPolicy {
	return ResultPolicy{
		Policy: resultaccept.Policy{
			Level: resultaccept.LevelRA2, Threshold: 2,
			DistinctDimensions: []resultaccept.Dimension{resultaccept.DimensionPrincipal, resultaccept.DimensionOperator},
			RejectAuthorities:  []string{"authority-1"},
		},
		Output: resultaccept.OutputMechanical,
	}
}

type fixture struct {
	store   *memory.Store
	service *Service
	task    taskstate.TaskRecord
	attempt attemptstate.AttemptRecord
	claim   resultclaim.Claim
	policy  *policySource
	clock   *fakeClock
}

type fixtureOptions struct {
	stopAt      attemptstate.AttemptPhase
	claimStatus resultclaim.Status
}

// newFixture takes a Task and an Attempt through the real store to the point
// where the Worker's claim is in and the Attempt waits for verification.
func newFixture(t *testing.T, opts fixtureOptions) *fixture {
	t.Helper()
	ctx := context.Background()
	if opts.stopAt == "" {
		opts.stopAt = attemptstate.AttemptPhaseVerifying
	}
	if opts.claimStatus == "" {
		opts.claimStatus = resultclaim.StatusCompleted
	}
	store := memory.New()

	taskID, err := ids.NewTaskID()
	if err != nil {
		t.Fatal(err)
	}
	task, err := taskstate.NewTask(taskID, contract.SHA256([]byte("taskir")), baseTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	ready := task
	ready.Phase = taskstate.TaskPhaseReady
	ready.Revision++
	ready.UpdatedAt = baseTime.Add(time.Second)
	if err := store.CompareAndSwapTask(ctx, task.ID, task.Revision, ready); err != nil {
		t.Fatal(err)
	}

	attemptID, err := ids.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptstate.New(attemptID, ready.ID, ready.TaskIRDigest, 1, baseTime.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	leaseID, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}

	step := func(phase attemptstate.AttemptPhase, mutate func(*attemptstate.AttemptRecord)) {
		next := attempt
		next.Phase = phase
		next.Revision++
		next.UpdatedAt = attempt.UpdatedAt.Add(time.Second)
		if mutate != nil {
			mutate(&next)
		}
		if err := store.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, next); err != nil {
			t.Fatalf("move Attempt to %s: %v", phase, err)
		}
		attempt = next
	}
	step(attemptstate.AttemptPhasePlacing, func(a *attemptstate.AttemptRecord) { a.WorkerIdentity = "worker-0" })
	step(attemptstate.AttemptPhaseAssigned, func(a *attemptstate.AttemptRecord) { a.LeaseID = leaseID })
	step(attemptstate.AttemptPhaseDispatched, nil)
	step(attemptstate.AttemptPhaseClaimed, nil)

	completed := opts.claimStatus == resultclaim.StatusCompleted
	cfg := resultclaim.ClaimConfig{
		TaskID: task.ID, TaskIRDigest: task.TaskIRDigest, AttemptID: attempt.ID,
		WorkerIdentity: "worker-0", LeaseID: leaseID, FenceEpoch: 1,
		WorkOrderDecision: "workorder-1", ExecutorDigest: contract.SHA256([]byte("executor")),
		Nonce: []byte("0123456789abcdef"), Status: opts.claimStatus, ExitCode: 1, OutputBytes: 5,
		StartedAt: baseTime, EndedAt: baseTime.Add(time.Second),
	}
	if completed {
		cfg.ExitCode = 0
		cfg.OutputDigest = outputDigest
	}
	claim, err := resultclaim.NewClaim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyResultClaim(ctx, statestore.ResultClaimAcceptance{Claim: claim, Record: true}); err != nil {
		t.Fatal(err)
	}
	if opts.stopAt == attemptstate.AttemptPhaseVerifying {
		step(attemptstate.AttemptPhaseVerifying, nil)
	}

	stored, err := store.LoadTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy := &policySource{policy: twoVoicePolicy()}
	clock := &fakeClock{now: baseTime.Add(time.Minute)}
	service, err := New(Config{
		Attempts: store, Tasks: store, Claims: store, Results: store,
		Policies: policy, Producers: producerSource{dims: producerDims()}, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{store: store, service: service, task: stored, attempt: attempt, claim: claim, policy: policy, clock: clock}
}

// verification builds one verifier's evidence about the claimed output.
func (f *fixture) verification(id, principal, operator string, check resultaccept.Check, observed string) resultaccept.Verification {
	return resultaccept.Verification{
		VerifierID: id, TaskID: f.task.ID.String(), AttemptID: f.attempt.ID.String(),
		Check: check, ObservedDigest: observed,
		Dimensions: map[resultaccept.Dimension]string{
			resultaccept.DimensionPrincipal:      principal,
			resultaccept.DimensionOperator:       operator,
			resultaccept.DimensionExecutorDigest: digestOf("executor-of-" + id),
		},
	}
}

func (f *fixture) reproduced(id, principal, operator string) resultaccept.Verification {
	return f.verification(id, principal, operator, resultaccept.CheckReproduced, outputDigest.Hex())
}

func (f *fixture) twoIndependentVerifiers() []resultaccept.Verification {
	return []resultaccept.Verification{
		f.reproduced("verifier-1", "principal-P1", "operator-O1"),
		f.reproduced("verifier-2", "principal-P2", "operator-O2"),
	}
}

func (f *fixture) request(verifications ...resultaccept.Verification) Request {
	return Request{AttemptID: f.attempt.ID, Verifications: verifications}
}

// assertUntouched checks that the Attempt and the Task are exactly as the
// fixture left them and that no accepted result exists.
func (f *fixture) assertUntouched(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	attempt, err := f.store.LoadAttempt(ctx, f.attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Phase != f.attempt.Phase || attempt.Revision != f.attempt.Revision || attempt.CloseReason != f.attempt.CloseReason {
		t.Fatalf("the Attempt changed: %+v, want %+v", attempt, f.attempt)
	}
	task, err := f.store.LoadTask(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Phase != f.task.Phase || task.Revision != f.task.Revision || task.TerminalOutcome != f.task.TerminalOutcome {
		t.Fatalf("the Task changed: %+v, want %+v", task, f.task)
	}
	if _, ok, err := f.store.LoadAcceptedResult(ctx, f.task.ID); err != nil || ok {
		t.Fatalf("an accepted result exists: %v, %v", ok, err)
	}
}

func TestAcceptPromotesAVerifiedResultAndCompletesTheTask(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	ctx := context.Background()

	result, err := f.service.Accept(ctx, f.request(f.twoIndependentVerifiers()...))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Outcome() != resultaccept.OutcomeAccepted || result.Replayed {
		t.Fatalf("result = %+v", result)
	}
	if got := result.Accepted; got.TaskID() != f.task.ID || got.AttemptID() != f.attempt.ID ||
		got.ClaimDigest() != f.claim.Digest() || got.OutputDigest() != outputDigest ||
		got.Level() != resultaccept.LevelRA2 || got.PolicyDigest() != result.Decision.PolicyDigest ||
		!got.AcceptedAt().Equal(f.clock.now) {
		t.Fatalf("accepted result = %+v", got)
	}
	if verifiers := result.Accepted.Verifiers(); len(verifiers) != 2 || verifiers[0] != "verifier-1" || verifiers[1] != "verifier-2" {
		t.Fatalf("verifiers = %v", verifiers)
	}

	stored, ok, err := f.store.LoadAcceptedResult(ctx, f.task.ID)
	if err != nil || !ok || !stored.Equal(result.Accepted) {
		t.Fatalf("stored accepted result = %+v, %v, %v", stored, ok, err)
	}
	attempt, _ := f.store.LoadAttempt(ctx, f.attempt.ID)
	if attempt.Phase != attemptstate.AttemptPhaseClosed || attempt.CloseReason != attemptstate.CloseReasonResultAccepted {
		t.Fatalf("Attempt = %+v", attempt)
	}
	task, _ := f.store.LoadTask(ctx, f.task.ID)
	if task.Phase != taskstate.TaskPhaseTerminal || task.TerminalOutcome != taskstate.TerminalOutcomeCompleted {
		t.Fatalf("Task = %+v", task)
	}
}

func TestAcceptIsIdempotentAndReturnsTheStoredResult(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	ctx := context.Background()
	first, err := f.service.Accept(ctx, f.request(f.twoIndependentVerifiers()...))
	if err != nil {
		t.Fatal(err)
	}
	// A repeat, even with less evidence, never decides again.
	again, err := f.service.Accept(ctx, f.request(f.reproduced("verifier-1", "principal-P1", "operator-O1")))
	if err != nil {
		t.Fatalf("repeat Accept() error = %v", err)
	}
	if !again.Replayed || again.Outcome() != resultaccept.OutcomeAccepted || !again.Accepted.Equal(first.Accepted) {
		t.Fatalf("repeat = %+v, want the stored result replayed", again)
	}
	task, _ := f.store.LoadTask(ctx, f.task.ID)
	if task.Revision != f.task.Revision+1 {
		t.Fatalf("the repeat changed the Task: revision %d", task.Revision)
	}
}

func TestAcceptHoldsUntilEnoughIndependentVoicesAgree(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	ctx := context.Background()

	held, err := f.service.Accept(ctx, f.request(f.reproduced("verifier-1", "principal-P1", "operator-O1")))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if held.Outcome() != resultaccept.OutcomeHeld || held.Decision.Reason != resultaccept.ReasonQuorumNotMet {
		t.Fatalf("held = %+v", held)
	}
	f.assertUntouched(t)

	// More evidence can still accept the same Attempt later.
	accepted, err := f.service.Accept(ctx, f.request(f.twoIndependentVerifiers()...))
	if err != nil || accepted.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("later Accept() = %+v, %v", accepted, err)
	}
}

func TestAcceptHoldsWithNoVerification(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	result, err := f.service.Accept(context.Background(), f.request())
	if err != nil || result.Outcome() != resultaccept.OutcomeHeld ||
		result.Decision.Reason != resultaccept.ReasonNoIndependentVerification {
		t.Fatalf("result = %+v, %v", result, err)
	}
	f.assertUntouched(t)
}

func TestAcceptDoesNotCountTheProducerAsAVerifier(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	// Both verifiers sit behind the principal that produced the result, so
	// neither can justify it, however many of them there are.
	result, err := f.service.Accept(context.Background(), f.request(
		f.reproduced("verifier-1", "principal-P0", "operator-O1"),
		f.reproduced("verifier-2", "principal-P0", "operator-O2"),
	))
	if err != nil || result.Outcome() != resultaccept.OutcomeHeld {
		t.Fatalf("result = %+v, %v", result, err)
	}
	f.assertUntouched(t)
}

func TestAcceptHoldsWhenVerificationsDisagree(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	result, err := f.service.Accept(context.Background(), f.request(
		f.reproduced("verifier-1", "principal-P1", "operator-O1"),
		f.verification("verifier-2", "principal-P2", "operator-O2", resultaccept.CheckDiffers, digestOf("a different output")),
	))
	if err != nil || result.Outcome() != resultaccept.OutcomeHeld ||
		result.Decision.Reason != resultaccept.ReasonVerificationsDisagree {
		t.Fatalf("result = %+v, %v", result, err)
	}
	f.assertUntouched(t)
}

func TestAcceptClosesTheAttemptWhenAnAuthorityRejectsTheOutput(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	ctx := context.Background()
	rejected := f.verification("authority-1", "principal-P3", "operator-O3", resultaccept.CheckValidatorRejected, outputDigest.Hex())

	result, err := f.service.Accept(ctx, f.request(f.reproduced("verifier-1", "principal-P1", "operator-O1"), rejected))
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if result.Outcome() != resultaccept.OutcomeRejected || result.Accepted.Valid() {
		t.Fatalf("result = %+v", result)
	}
	attempt, _ := f.store.LoadAttempt(ctx, f.attempt.ID)
	if attempt.Phase != attemptstate.AttemptPhaseClosed || attempt.CloseReason != attemptstate.CloseReasonResultRejected {
		t.Fatalf("Attempt = %+v", attempt)
	}
	// Retrying is a recovery decision. Rejection leaves the Task as it was.
	task, _ := f.store.LoadTask(ctx, f.task.ID)
	if task.Phase != f.task.Phase || task.Revision != f.task.Revision {
		t.Fatalf("a rejection changed the Task: %+v", task)
	}
	if _, ok, _ := f.store.LoadAcceptedResult(ctx, f.task.ID); ok {
		t.Fatal("a rejected result was recorded as accepted")
	}
}

func TestAcceptRefusesAnAttemptThatIsNotWaitingForVerification(t *testing.T) {
	f := newFixture(t, fixtureOptions{stopAt: attemptstate.AttemptPhaseClaimed})
	_, err := f.service.Accept(context.Background(), f.request(f.twoIndependentVerifiers()...))
	if !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("Accept() error = %v, want %v", err, ErrWrongPhase)
	}
	f.assertUntouched(t)
}

func TestAcceptRefusesAnAttemptWhoseClaimDidNotComplete(t *testing.T) {
	f := newFixture(t, fixtureOptions{stopAt: attemptstate.AttemptPhaseClaimed, claimStatus: resultclaim.StatusFailed})
	// The state machine only reaches verification after a completed claim. This
	// forces the Attempt there to check the service does not rely on that alone.
	next := f.attempt
	next.Phase = attemptstate.AttemptPhaseVerifying
	next.Revision++
	next.UpdatedAt = f.attempt.UpdatedAt.Add(time.Second)
	if err := f.store.CompareAndSwapAttempt(context.Background(), f.attempt.ID, f.attempt.Revision, next); err != nil {
		t.Fatal(err)
	}
	f.attempt = next

	_, err := f.service.Accept(context.Background(), f.request(f.twoIndependentVerifiers()...))
	if !errors.Is(err, ErrNoCompletedClaim) {
		t.Fatalf("Accept() error = %v, want %v", err, ErrNoCompletedClaim)
	}
	f.assertUntouched(t)
}

func TestAcceptFailsClosedWhenTheTrustedInputsCannotBeRead(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	f.policy.err = errors.New("policy store is down")
	if _, err := f.service.Accept(context.Background(), f.request(f.twoIndependentVerifiers()...)); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("policy failure error = %v, want %v", err, ErrPolicyUnavailable)
	}
	f.assertUntouched(t)

	f = newFixture(t, fixtureOptions{})
	broken, err := New(Config{
		Attempts: f.store, Tasks: f.store, Claims: f.store, Results: f.store,
		Policies: f.policy, Producers: producerSource{err: errors.New("identity records are down")}, Clock: f.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broken.Accept(context.Background(), f.request(f.twoIndependentVerifiers()...)); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("producer failure error = %v, want %v", err, ErrPolicyUnavailable)
	}
	f.assertUntouched(t)
}

func TestAcceptRefusesEvidenceForAnotherAttemptWithoutChangingState(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	foreign := f.reproduced("verifier-1", "principal-P1", "operator-O1")
	foreign.AttemptID = "atm_00000000000000000000000000000099"
	_, err := f.service.Accept(context.Background(), f.request(foreign, f.reproduced("verifier-2", "principal-P2", "operator-O2")))
	if !errors.Is(err, resultaccept.ErrRejectedInput) {
		t.Fatalf("Accept() error = %v, want %v", err, resultaccept.ErrRejectedInput)
	}
	f.assertUntouched(t)
}

func TestAcceptDoesNotCompleteATaskWithACancellationRequest(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	ctx := context.Background()
	requested := f.task
	requested.CancellationRequested = true
	requested.Revision++
	requested.UpdatedAt = requested.UpdatedAt.Add(time.Second)
	if err := f.store.CompareAndSwapTask(ctx, f.task.ID, f.task.Revision, requested); err != nil {
		t.Fatal(err)
	}
	f.task = requested

	_, err := f.service.Accept(ctx, f.request(f.twoIndependentVerifiers()...))
	if !errors.Is(err, ErrTaskNotCompletable) {
		t.Fatalf("Accept() error = %v, want %v", err, ErrTaskNotCompletable)
	}
	f.assertUntouched(t)
}

func TestAcceptNeverMovesTimeBackwards(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	f.clock.now = f.attempt.UpdatedAt.Add(-time.Hour)
	result, err := f.service.Accept(context.Background(), f.request(f.twoIndependentVerifiers()...))
	if err != nil || result.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result = %+v, %v", result, err)
	}
	attempt, _ := f.store.LoadAttempt(context.Background(), f.attempt.ID)
	if attempt.UpdatedAt.Before(f.attempt.UpdatedAt) {
		t.Fatalf("the Attempt time moved backwards: %s", attempt.UpdatedAt)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	valid := Config{
		Attempts: f.store, Tasks: f.store, Claims: f.store, Results: f.store,
		Policies: f.policy, Producers: producerSource{dims: producerDims()}, Clock: f.clock,
	}
	cases := map[string]func(*Config){
		"attempts":  func(c *Config) { c.Attempts = nil },
		"tasks":     func(c *Config) { c.Tasks = nil },
		"claims":    func(c *Config) { c.Claims = nil },
		"results":   func(c *Config) { c.Results = nil },
		"policies":  func(c *Config) { c.Policies = nil },
		"producers": func(c *Config) { c.Producers = nil },
		"clock":     func(c *Config) { c.Clock = nil },
	}
	for name, mutate := range cases {
		cfg := valid
		mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: New() error = %v, want %v", name, err, ErrInvalidConfig)
		}
	}
}

func TestAcceptDoesNotLetTheProducingWorkerVouchForItself(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	// The producing Worker reports under its own identity as a verifier too. Its
	// agreement with itself is not independent evidence, even when its other
	// dimensions look different from the real verifier's.
	self := f.reproduced("worker-0", "principal-P9", "operator-O9")
	result, err := f.service.Accept(context.Background(), f.request(
		self, f.reproduced("verifier-1", "principal-P1", "operator-O1")))
	if err != nil || result.Outcome() != resultaccept.OutcomeHeld {
		t.Fatalf("result = %+v, %v", result, err)
	}
	f.assertUntouched(t)
}

func TestAcceptRefusesATaskThatAlreadyReachedAnotherEnd(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	ctx := context.Background()
	cancelled := f.task
	cancelled.Phase = taskstate.TaskPhaseTerminal
	cancelled.TerminalOutcome = taskstate.TerminalOutcomeCancelled
	cancelled.Revision++
	cancelled.UpdatedAt = cancelled.UpdatedAt.Add(time.Second)
	if err := f.store.CompareAndSwapTask(ctx, f.task.ID, f.task.Revision, cancelled); err != nil {
		t.Fatal(err)
	}
	f.task = cancelled

	_, err := f.service.Accept(ctx, f.request(f.twoIndependentVerifiers()...))
	if !errors.Is(err, ErrTaskNotCompletable) || errors.Is(err, ErrStateChanged) {
		t.Fatalf("Accept() error = %v, want %v and not a retryable race", err, ErrTaskNotCompletable)
	}
	f.assertUntouched(t)
}

func TestAcceptKeepsTheCauseOfAPolicyFailure(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	cause := errors.New("policy store is down")
	f.policy.err = cause
	_, err := f.service.Accept(context.Background(), f.request(f.twoIndependentVerifiers()...))
	if !errors.Is(err, ErrPolicyUnavailable) || !errors.Is(err, cause) {
		t.Fatalf("Accept() error = %v, want it to wrap both %v and %v", err, ErrPolicyUnavailable, cause)
	}
}

func TestAcceptOnAnUnbuiltServiceFailsInsteadOfPanicking(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	partial := &Service{attempts: f.store, results: f.store, clock: f.clock}
	if _, err := partial.Accept(context.Background(), f.request()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Accept() error = %v, want %v", err, ErrInvalidConfig)
	}
}
