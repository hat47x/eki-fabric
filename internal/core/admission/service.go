// Package admission turns a hostile TaskRequest into an admitted Task, and
// exposes the Client-visible view of what was admitted.
//
// It owns the Task Admission rules (ADM-2): strict validation, principal
// binding, idempotency, policy and budget checks. It knows nothing about HTTP;
// the transport layer calls it and maps its results.
package admission

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// Principal is an authenticated submitter. Authentication is the caller's job;
// admission only binds work to the identity it is given.
type Principal struct {
	ID string
}

// Config wires the Service. Store and Policy are required.
type Config struct {
	Store  statestore.Store
	Policy Policy
	// Results reads the durable AcceptedResult of a Task. It is optional: when
	// nil, Result is unavailable and the Client API answers 501 for it.
	Results statestore.ResultAcceptanceStore
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// NewTaskID creates a Task ID. Nil means ids.NewTaskID.
	NewTaskID func() (ids.TaskID, error)
	// OnInternalError receives failures that are our fault. The Client only
	// ever sees a generic error. Nil discards them.
	OnInternalError func(operation string, err error)
	// QuotaDrainsAutomatically says whether admitted Tasks can finish without
	// the Client doing anything. A quota rejection is retryable only when they
	// can: otherwise waiting changes nothing and the Client must cancel a Task
	// to free capacity (#282).
	QuotaDrainsAutomatically bool
}

type Service struct {
	store       statestore.Store
	results     statestore.ResultAcceptanceStore
	policy      Policy
	now         func() time.Time
	newTaskID   func() (ids.TaskID, error)
	onError     func(string, error)
	quotaDrains bool
}

func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: a store is required", ErrInvalidPolicy)
	}
	if err := cfg.Policy.validate(); err != nil {
		return nil, err
	}
	s := &Service{
		store: cfg.Store, results: cfg.Results, policy: cfg.Policy,
		now: cfg.Now, newTaskID: cfg.NewTaskID, onError: cfg.OnInternalError,
		quotaDrains: cfg.QuotaDrainsAutomatically,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.newTaskID == nil {
		s.newTaskID = ids.NewTaskID
	}
	if s.onError == nil {
		s.onError = func(string, error) {}
	}
	return s, nil
}

// Accepted is the result of a successful Submit.
type Accepted struct {
	TaskID      ids.TaskID
	SubmittedAt time.Time
	// Replayed is true when the idempotency key had already produced this Task.
	Replayed bool
}

// Submit admits a Task for principal. idempotencyKey scopes to the principal.
func (s *Service) Submit(ctx context.Context, p Principal, idempotencyKey string, body []byte) (Accepted, error) {
	if err := validatePrincipal(p); err != nil {
		return Accepted{}, err
	}
	if !ValidIdempotencyKey(idempotencyKey) {
		return Accepted{}, InvalidIdempotencyKey()
	}
	if len(body) > statestore.MaxTaskContentBytes {
		return Accepted{}, badRequest("TASK_REQUEST_TOO_LARGE", fmt.Sprintf("task request exceeds %d bytes", statestore.MaxTaskContentBytes))
	}

	req, err := ParseTaskRequest(body)
	if err != nil {
		return Accepted{}, err
	}
	now := s.now().UTC()
	if perr := s.policy.check(req, now); perr != nil {
		return Accepted{}, perr
	}

	fingerprint, err := req.Fingerprint()
	if err != nil {
		return Accepted{}, s.internal("fingerprint", err)
	}
	irDigest, err := req.TaskIRDigest()
	if err != nil {
		return Accepted{}, s.internal("task ir digest", err)
	}
	id, err := s.newTaskID()
	if err != nil {
		return Accepted{}, s.internal("new task id", err)
	}
	task, err := taskstate.NewTask(id, irDigest, now)
	if err != nil {
		return Accepted{}, s.internal("new task", err)
	}

	admission := statestore.Admission{
		Task:               task,
		Principal:          p.ID,
		IdempotencyKey:     idempotencyKey,
		RequestFingerprint: fingerprint,
		MaxAttempts:        req.Reliability.MaxAttempts,
		// The validated request body, kept verbatim: WorkOrder issuance and
		// deadline decisions must be able to read the parameters and deadline
		// the Task was admitted with, not only the digest of them.
		Content: append([]byte(nil), body...),
	}
	result, err := s.store.AdmitTask(ctx, admission, statestore.AdmitLimit{MaxOutstandingTasks: s.policy.MaxOutstandingTasks})
	switch {
	case err == nil:
		return Accepted{
			TaskID:      result.Admission.Task.ID,
			SubmittedAt: result.Admission.Task.CreatedAt,
			Replayed:    result.Replayed,
		}, nil
	case errors.Is(err, statestore.ErrIdempotencyConflict):
		return Accepted{}, &Error{
			Kind:   KindConflict,
			Code:   "IDEMPOTENCY_KEY_CONFLICT",
			Title:  "The idempotency key was already used for a different request",
			Detail: "Use a new Idempotency-Key for a different request.",
		}
	case errors.Is(err, statestore.ErrQuotaExceeded):
		// Waiting frees capacity only when admitted Tasks can finish on their
		// own. Without that, the Client has to cancel a Task, and announcing a
		// retry would send it into a loop that never clears (#282).
		quotaError := &Error{
			Kind:                 KindTooManyRequests,
			Code:                 "TASK_QUOTA_EXCEEDED",
			Title:                "The outstanding task limit has been reached",
			Retryable:            s.quotaDrains,
			PolicyReasonCategory: "QUOTA",
			Detail:               "Cancel a task you no longer need to free capacity.",
		}
		if s.quotaDrains {
			quotaError.Detail = "Wait for a running task to finish, or cancel one to free capacity."
		}
		return Accepted{}, quotaError
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return Accepted{}, err
	default:
		return Accepted{}, s.internal("admit task", err)
	}
}

// PublicStatus is the Client-visible projection of a Task. It never carries
// Worker identity, Attempts, Leases, or policy revisions.
type PublicStatus struct {
	TaskID        ids.TaskID
	Status        string
	SubmittedAt   time.Time
	UpdatedAt     time.Time
	TerminalCode  string
	PublicMessage string
}

// Status returns the public status of a Task the principal submitted. A Task
// that does not exist and a Task that belongs to someone else look the same.
func (s *Service) Status(ctx context.Context, p Principal, taskID string) (PublicStatus, error) {
	admission, err := s.owned(ctx, p, taskID)
	if err != nil {
		return PublicStatus{}, err
	}
	return project(admission.Task), nil
}

// CancelAccepted is the result of a successful Cancel.
type CancelAccepted struct {
	TaskID ids.TaskID
}

const cancelRetries = 5

// Cancel records the principal's cancellation intent. A Task that has never
// been given an Attempt cannot have effects, so it becomes cancelled at once;
// otherwise the intent is recorded and the components that own running work
// act on it. Cancelling a finished Task is a conflict; repeating a
// cancellation is not.
func (s *Service) Cancel(ctx context.Context, p Principal, taskID string) (CancelAccepted, error) {
	admission, err := s.owned(ctx, p, taskID)
	if err != nil {
		return CancelAccepted{}, err
	}
	id := admission.Task.ID

	for attempt := 0; attempt < cancelRetries; attempt++ {
		current := admission.Task
		switch {
		case current.Phase == taskstate.TaskPhaseTerminal && current.TerminalOutcome == taskstate.TerminalOutcomeCancelled:
			return CancelAccepted{TaskID: id}, nil
		case current.Phase == taskstate.TaskPhaseTerminal:
			return CancelAccepted{}, &Error{
				Kind:   KindConflict,
				Code:   "TASK_ALREADY_FINISHED",
				Title:  "The task has already finished and cannot be cancelled",
				TaskID: id.String(),
			}
		case current.CancellationRequested:
			return CancelAccepted{TaskID: id}, nil
		}

		next := current
		next.CancellationRequested = true
		next.Revision = current.Revision + 1
		// The wall clock can step backwards (NTP correction, VM clock sync,
		// a restart on another host), so the new time is never earlier than
		// what the record already carries. Otherwise the record would fail
		// Validate and a valid cancellation would surface as a 500.
		next.UpdatedAt = s.now().UTC()
		if next.UpdatedAt.Before(current.UpdatedAt) {
			next.UpdatedAt = current.UpdatedAt
		}
		if current.Phase == taskstate.TaskPhaseAdmitted || current.Phase == taskstate.TaskPhaseReady {
			next.Phase = taskstate.TaskPhaseTerminal
			next.TerminalOutcome = taskstate.TerminalOutcomeCancelled
		}

		err := s.store.CompareAndSwapTask(ctx, id, current.Revision, next)
		switch {
		case err == nil:
			return CancelAccepted{TaskID: id}, nil
		case errors.Is(err, statestore.ErrConflict):
			// Someone else moved the Task. Look again and re-decide.
			admission, err = s.owned(ctx, p, taskID)
			if err != nil {
				return CancelAccepted{}, err
			}
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return CancelAccepted{}, err
		default:
			return CancelAccepted{}, s.internal("cancel task", err)
		}
	}
	return CancelAccepted{}, &Error{
		Kind:      KindConflict,
		Code:      "TASK_BUSY",
		Title:     "The task is changing too quickly to cancel; try again",
		Retryable: true,
		TaskID:    id.String(),
	}
}

func (s *Service) owned(ctx context.Context, p Principal, taskID string) (statestore.Admission, error) {
	if err := validatePrincipal(p); err != nil {
		return statestore.Admission{}, err
	}
	id, err := ids.ParseTaskID(taskID)
	if err != nil {
		// A malformed ID cannot exist. Answer like any absent Task.
		return statestore.Admission{}, notFound()
	}
	admission, err := s.store.LoadAdmission(ctx, id)
	switch {
	case err == nil:
	case errors.Is(err, statestore.ErrNotFound):
		return statestore.Admission{}, notFound()
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return statestore.Admission{}, err
	default:
		return statestore.Admission{}, s.internal("load admission", err)
	}
	if admission.Principal != p.ID {
		return statestore.Admission{}, notFound()
	}
	return admission, nil
}

func validatePrincipal(p Principal) *Error {
	if p.ID == "" || len(p.ID) > statestore.MaxPrincipalBytes {
		return &Error{Kind: KindForbidden, Code: "PRINCIPAL_INVALID", Title: "The principal is not authorized for this operation"}
	}
	return nil
}

func (s *Service) internal(operation string, err error) *Error {
	s.onError(operation, err)
	return internalError()
}

func project(t taskstate.TaskRecord) PublicStatus {
	status := PublicStatus{
		TaskID:      t.ID,
		SubmittedAt: t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
	switch t.Phase {
	case taskstate.TaskPhaseAdmitted, taskstate.TaskPhaseReady:
		status.Status = "queued"
	case taskstate.TaskPhaseActive:
		status.Status = "running"
	case taskstate.TaskPhaseVerifying:
		status.Status = "verifying"
	case taskstate.TaskPhaseReconciling:
		status.Status = "reconciling"
	case taskstate.TaskPhaseHeld:
		status.Status = "attention-required"
	case taskstate.TaskPhaseTerminal:
		switch t.TerminalOutcome {
		case taskstate.TerminalOutcomeCompleted:
			status.Status = "completed"
		case taskstate.TerminalOutcomeCancelled:
			status.Status = "cancelled"
		default:
			status.Status = "failed"
		}
		status.TerminalCode = strings.ToUpper(string(t.TerminalOutcome))
	}
	if t.CancellationRequested && t.Phase != taskstate.TaskPhaseTerminal {
		status.PublicMessage = "Cancellation has been requested."
	}
	return status
}
