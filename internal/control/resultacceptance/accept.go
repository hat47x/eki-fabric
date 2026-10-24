// Package resultacceptance is the Result Acceptance Authority (RAC-1): it decides
// whether the result a Worker reported may be promoted to an AcceptedResult, and
// applies that decision to the Control Plane's state.
//
// The decision itself is resultaccept.Decide, a pure function. This package
// supplies what that function must not take from a Worker: the policy and the
// output class, which come from the trusted boundary, and the producer's
// independence dimensions, which come from identity records. It then performs the
// one state change the decision allows:
//
//	accepted -> record the AcceptedResult, close the Attempt as accepted and
//	            complete the Task, in one atomic commit
//	rejected -> record why, and close the Attempt as rejected, in one atomic
//	            commit; the Task is left for recovery
//	held     -> change nothing; the evidence is insufficient or disagrees
//
// A held decision is not a failure. The verifications may be incomplete, and a
// later call with more evidence can still accept the same Attempt.
package resultacceptance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/acceptedresult"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/rejectedresult"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/resultverify"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var (
	ErrInvalidConfig      = errors.New("invalid result acceptance configuration")
	ErrWrongPhase         = errors.New("the Attempt is not waiting for result verification")
	ErrNoCompletedClaim   = errors.New("the Attempt has no completed claim to accept")
	ErrStateChanged       = errors.New("the Attempt or Task changed while the result was being decided")
	ErrTaskNotCompletable = errors.New("the Task cannot be completed: it is terminal or has a cancellation request")
	ErrPolicyUnavailable  = errors.New("the result policy or the producer identity could not be read")
)

type AttemptStore interface {
	LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error)
	CompareAndSwapAttempt(ctx context.Context, id ids.AttemptID, expectedRevision uint64, next attemptstate.AttemptRecord) error
}

type TaskStore interface {
	LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error)
}

// ResultPolicy is what the trusted boundary requires for one Task's result.
type ResultPolicy struct {
	// Policy is the quorum requirement. It never comes from a claim or a Worker.
	Policy resultaccept.Policy
	// Output is the output class the admitted Task contract declares.
	Output resultaccept.OutputClass
}

// PolicySource returns the result policy for a Task. A failure must be returned
// as an error: a missing policy is never read as "no requirement".
type PolicySource interface {
	ResultPolicy(ctx context.Context, task ids.TaskID) (ResultPolicy, error)
}

// ProducerSource returns the independence dimension values of the Worker that
// produced a result, from the trusted identity records and not from the Worker's
// own words.
type ProducerSource interface {
	ProducerDimensions(ctx context.Context, workerIdentity string) (map[resultaccept.Dimension]string, error)
}

type Clock interface{ Now() time.Time }

type Config struct {
	Attempts AttemptStore
	Tasks    TaskStore
	Claims   statestore.ResultClaimStore
	Results  statestore.ResultAcceptanceStore
	// Rejections records why a result was rejected, with the Attempt's close.
	Rejections statestore.ResultRejectionStore
	Policies   PolicySource
	Producers  ProducerSource
	Clock      Clock
}

type Service struct {
	attempts   AttemptStore
	tasks      TaskStore
	claims     statestore.ResultClaimStore
	results    statestore.ResultAcceptanceStore
	rejections statestore.ResultRejectionStore
	policies   PolicySource
	producers  ProducerSource
	clock      Clock
}

func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Attempts == nil:
		return nil, fmt.Errorf("%w: Attempt store is required", ErrInvalidConfig)
	case cfg.Tasks == nil:
		return nil, fmt.Errorf("%w: Task store is required", ErrInvalidConfig)
	case cfg.Claims == nil:
		return nil, fmt.Errorf("%w: Claim store is required", ErrInvalidConfig)
	case cfg.Results == nil:
		return nil, fmt.Errorf("%w: result store is required", ErrInvalidConfig)
	case cfg.Rejections == nil:
		return nil, fmt.Errorf("%w: rejection store is required", ErrInvalidConfig)
	case cfg.Policies == nil:
		return nil, fmt.Errorf("%w: policy source is required", ErrInvalidConfig)
	case cfg.Producers == nil:
		return nil, fmt.Errorf("%w: producer source is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}
	return &Service{
		attempts: cfg.Attempts, tasks: cfg.Tasks, claims: cfg.Claims, results: cfg.Results, rejections: cfg.Rejections,
		policies: cfg.Policies, producers: cfg.Producers, clock: cfg.Clock,
	}, nil
}

// Request names the Attempt and the verification evidence for it. The evidence
// comes from Result Verification (RVR-1). It is input to a decision, not a
// decision.
type Request struct {
	AttemptID     ids.AttemptID
	Verifications []resultaccept.Verification
	// Observations are raw verifier reports. Accept compares each with the
	// stored completed claim (resultverify.Normalize) and adds the result to
	// Verifications, so the relation between an observation and the claim is
	// decided here and not by the verifier or the Worker.
	Observations []resultverify.Observation
}

// Result reports what Accept decided and did.
type Result struct {
	// Decision is the evaluation of this call. It is the zero value when the
	// Attempt had already been accepted by an earlier call.
	Decision resultaccept.Decision
	// Accepted is the stored AcceptedResult. It is set only when the outcome is
	// accepted, including when an earlier call already accepted this Attempt.
	Accepted acceptedresult.Record
	// Rejected is the stored rejection. It is set only when the outcome is
	// rejected, including when an earlier call already rejected this Attempt.
	Rejected rejectedresult.Record
	// Replayed is true when the Attempt was already decided and nothing changed.
	Replayed bool
}

// Outcome is the verdict of this call, accepted, held or rejected.
func (r Result) Outcome() resultaccept.Outcome {
	if r.Accepted.Valid() {
		return resultaccept.OutcomeAccepted
	}
	if r.Rejected.Valid() {
		return resultaccept.OutcomeRejected
	}
	return r.Decision.Outcome
}

// Accept decides one Attempt that is waiting for verification. It is idempotent:
// an Attempt that was already accepted returns the stored result.
func (s *Service) Accept(ctx context.Context, req Request) (Result, error) {
	if s == nil || s.attempts == nil || s.tasks == nil || s.claims == nil || s.results == nil ||
		s.rejections == nil || s.policies == nil || s.producers == nil || s.clock == nil {
		return Result{}, fmt.Errorf("%w: service is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is required", ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	now := s.clock.Now().UTC()
	if now.IsZero() {
		return Result{}, fmt.Errorf("%w: clock returned zero time", ErrInvalidConfig)
	}

	attempt, err := s.attempts.LoadAttempt(ctx, req.AttemptID)
	if err != nil {
		return Result{}, err
	}
	if attempt.Phase == attemptstate.AttemptPhaseClosed && attempt.CloseReason == attemptstate.CloseReasonResultAccepted {
		return s.replay(ctx, attempt)
	}
	if attempt.Phase == attemptstate.AttemptPhaseClosed && attempt.CloseReason == attemptstate.CloseReasonResultRejected {
		return s.replayRejection(ctx, attempt)
	}
	if attempt.Phase != attemptstate.AttemptPhaseVerifying {
		return Result{}, fmt.Errorf("%w: Attempt %s is %s", ErrWrongPhase, attempt.ID, attempt.Phase)
	}

	claim, found, err := s.claims.LoadResultClaim(ctx, attempt.ID)
	if err != nil {
		return Result{}, err
	}
	if !found || claim.Status() != resultclaim.StatusCompleted {
		return Result{}, fmt.Errorf("%w: Attempt %s", ErrNoCompletedClaim, attempt.ID)
	}
	task, err := s.tasks.LoadTask(ctx, attempt.TaskID)
	if err != nil {
		return Result{}, err
	}
	// A Task that can no longer complete will never accept this result. Say so
	// before evaluating evidence, so the caller does not read it as a race to retry.
	if task.CancellationRequested || !task.Phase.AcceptsResult() {
		return Result{}, fmt.Errorf("%w: Task %s is %s", ErrTaskNotCompletable, task.ID, task.Phase)
	}

	subject, err := resultverify.SubjectOf(claim)
	if err != nil {
		return Result{}, fmt.Errorf("%w: Attempt %s: %v", ErrNoCompletedClaim, attempt.ID, err)
	}
	if subject.TaskID != attempt.TaskID || subject.TaskIRDigest != attempt.TaskIRDigest {
		return Result{}, fmt.Errorf("%w: Attempt %s: stored claim is bound to another Task or TaskIR", ErrNoCompletedClaim, attempt.ID)
	}
	normalized, err := resultverify.NormalizeAll(subject, req.Observations)
	if err != nil {
		return Result{}, err
	}
	verifications := append(append([]resultaccept.Verification(nil), req.Verifications...), normalized...)

	policy, err := s.policies.ResultPolicy(ctx, attempt.TaskID)
	if err != nil {
		return Result{}, fmt.Errorf("%w: policy: %w", ErrPolicyUnavailable, err)
	}
	producer, err := s.producers.ProducerDimensions(ctx, attempt.WorkerIdentity)
	if err != nil {
		return Result{}, fmt.Errorf("%w: producer: %w", ErrPolicyUnavailable, err)
	}

	decision, err := resultaccept.Decide(resultaccept.ResultClaim{
		TaskID:       attempt.TaskID.String(),
		AttemptID:    attempt.ID.String(),
		WorkerID:     attempt.WorkerIdentity,
		OutputDigest: claim.OutputDigest().Hex(),
		Dimensions:   producer,
	}, verifications, policy.Output, &policy.Policy)
	if err != nil {
		return Result{}, err
	}

	switch decision.Outcome {
	case resultaccept.OutcomeAccepted:
		return s.accept(ctx, attempt, task, claim, decision, now)
	case resultaccept.OutcomeRejected:
		return s.reject(ctx, attempt, claim, decision, now)
	default:
		return Result{Decision: decision}, nil
	}
}

func (s *Service) accept(ctx context.Context, attempt attemptstate.AttemptRecord, task taskstate.TaskRecord,
	claim resultclaim.Claim, decision resultaccept.Decision, now time.Time) (Result, error) {
	record, err := acceptedresult.New(acceptedresult.Config{
		TaskID: attempt.TaskID, AttemptID: attempt.ID,
		ClaimDigest: claim.Digest(), OutputDigest: claim.OutputDigest(),
		Level: decision.Level, PolicyDigest: decision.PolicyDigest,
		Verifiers: decision.Verifiers, AcceptedAt: now,
	})
	if err != nil {
		return Result{}, err
	}

	closed := attempt
	closed.Phase = attemptstate.AttemptPhaseClosed
	closed.CloseReason = attemptstate.CloseReasonResultAccepted
	closed.Revision++
	closed.UpdatedAt = laterOf(now, attempt.UpdatedAt)

	completed := task
	completed.Phase = taskstate.TaskPhaseTerminal
	completed.TerminalOutcome = taskstate.TerminalOutcomeCompleted
	completed.Revision++
	completed.UpdatedAt = laterOf(now, task.UpdatedAt)

	err = s.results.CommitAcceptedResult(ctx, statestore.ResultAcceptanceCommit{
		Result:                  record,
		ExpectedAttemptRevision: attempt.Revision,
		NextAttempt:             closed,
		ExpectedTaskRevision:    task.Revision,
		NextTask:                completed,
	})
	switch {
	case err == nil:
		return Result{Decision: decision, Accepted: record}, nil
	case errors.Is(err, statestore.ErrAlreadyExists):
		// Another call committed the same decision first.
		stored, ok, loadErr := s.results.LoadAcceptedResult(ctx, attempt.TaskID)
		if loadErr != nil {
			return Result{}, loadErr
		}
		if ok && stored.Equal(record) {
			return Result{Decision: decision, Accepted: stored, Replayed: true}, nil
		}
		return Result{}, fmt.Errorf("%w: %v", ErrStateChanged, err)
	case errors.Is(err, statestore.ErrConflict):
		return Result{}, fmt.Errorf("%w: %v", ErrStateChanged, err)
	}
	return Result{}, err
}

// reject records why the result was rejected and closes the Attempt as
// rejected, in one commit, so the evidence chain of #57 keeps the dissent, the
// eligible and excluded verifiers, and the policy digest. The Task is not
// touched: whether the work is retried is a recovery decision.
func (s *Service) reject(ctx context.Context, attempt attemptstate.AttemptRecord, claim resultclaim.Claim,
	decision resultaccept.Decision, now time.Time) (Result, error) {
	record, err := rejectedresult.FromDecision(attempt.TaskID, attempt.ID, claim.Digest(), claim.OutputDigest(), decision, now)
	if err != nil {
		return Result{}, err
	}
	closed := attempt
	closed.Phase = attemptstate.AttemptPhaseClosed
	closed.CloseReason = attemptstate.CloseReasonResultRejected
	closed.Revision++
	closed.UpdatedAt = laterOf(now, attempt.UpdatedAt)
	err = s.rejections.CommitRejectedResult(ctx, statestore.ResultRejectionCommit{
		Result:                  record,
		ExpectedAttemptRevision: attempt.Revision,
		NextAttempt:             closed,
	})
	switch {
	case err == nil:
		return Result{Decision: decision, Rejected: record}, nil
	case errors.Is(err, statestore.ErrAlreadyExists):
		stored, ok, loadErr := s.rejections.LoadRejectedResult(ctx, attempt.ID)
		if loadErr != nil {
			return Result{}, loadErr
		}
		if ok && stored.Equal(record) {
			return Result{Decision: decision, Rejected: stored, Replayed: true}, nil
		}
		return Result{}, fmt.Errorf("%w: %v", ErrStateChanged, err)
	case errors.Is(err, statestore.ErrConflict):
		return Result{}, fmt.Errorf("%w: %v", ErrStateChanged, err)
	}
	return Result{}, err
}

func (s *Service) replayRejection(ctx context.Context, attempt attemptstate.AttemptRecord) (Result, error) {
	stored, ok, err := s.rejections.LoadRejectedResult(ctx, attempt.ID)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		// An Attempt closed as rejected without a record was closed before
		// rejections were recorded, or by another path. It is not decided again.
		return Result{}, fmt.Errorf("%w: Attempt %s is closed as rejected", ErrWrongPhase, attempt.ID)
	}
	return Result{Rejected: stored, Replayed: true}, nil
}

func (s *Service) replay(ctx context.Context, attempt attemptstate.AttemptRecord) (Result, error) {
	stored, ok, err := s.results.LoadAcceptedResult(ctx, attempt.TaskID)
	if err != nil {
		return Result{}, err
	}
	if !ok || stored.AttemptID() != attempt.ID {
		return Result{}, fmt.Errorf("%w: Attempt %s is closed as accepted but the Task has no matching accepted result",
			ErrStateChanged, attempt.ID)
	}
	return Result{Accepted: stored, Replayed: true}, nil
}

// laterOf keeps a stored UpdatedAt from moving backwards when the clock reads
// earlier than the record, which the state machines reject.
func laterOf(now, recorded time.Time) time.Time {
	if recorded.After(now) {
		return recorded
	}
	return now
}
