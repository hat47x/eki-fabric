package admission

import (
	"context"
	"errors"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// PublicResult is the Client-visible projection of an AcceptedResult.
//
// It carries only what P0's TaskResult can express: the Task and a public
// assurance summary. It never carries the Attempt, the verifier identities, the
// policy digest or the claim and output digests. P0 declares no field for the
// digests, and an ArtifactDescriptor needs a size, a media type and a data
// class that the AcceptedResult does not record, so none is invented.
type PublicResult struct {
	TaskID ids.TaskID
	// AssuranceProfile is the assurance level RAC-1 established, as recorded
	// in the AcceptedResult.
	AssuranceProfile string
}

// ResultsEnabled reports whether this Service can answer Result. A Service
// built without a result reader leaves the operation unavailable.
func (s *Service) ResultsEnabled() bool { return s.results != nil }

// Result returns the public projection of the AcceptedResult of a Task the
// principal submitted. Ownership is decided exactly as for Status, so an absent
// Task and someone else's Task look the same.
//
// A Task that has not finished is RESULT_NOT_READY (retryable). A Task that
// finished without an AcceptedResult (cancelled, failed) is
// RESULT_NOT_AVAILABLE and will never have one. A completed status alone is not
// enough: the durable AcceptedResult must exist and belong to the Task.
func (s *Service) Result(ctx context.Context, p Principal, taskID string) (PublicResult, error) {
	if s.results == nil {
		return PublicResult{}, internalError()
	}
	admission, err := s.owned(ctx, p, taskID)
	if err != nil {
		return PublicResult{}, err
	}
	task := admission.Task
	id := task.ID

	if task.Phase != taskstate.TaskPhaseTerminal {
		return PublicResult{}, &Error{
			Kind: KindConflict, Code: "RESULT_NOT_READY",
			Title:     "The task has not produced an accepted result yet",
			Retryable: true, TaskID: id.String(),
		}
	}
	notAvailable := &Error{
		Kind: KindConflict, Code: "RESULT_NOT_AVAILABLE",
		Title: "The task finished without an accepted result", TaskID: id.String(),
	}
	if task.TerminalOutcome != taskstate.TerminalOutcomeCompleted {
		return PublicResult{}, notAvailable
	}

	record, ok, err := s.results.LoadAcceptedResult(ctx, id)
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return PublicResult{}, err
	default:
		return PublicResult{}, s.internal("load accepted result", err)
	}
	if !ok {
		// Completed without a record cannot be reached through the atomic
		// acceptance commit. Never answer it as a result.
		s.onError("load accepted result", errors.New("task "+id.String()+" is completed but has no accepted result"))
		return PublicResult{}, notAvailable
	}
	if record.TaskID() != id {
		return PublicResult{}, s.internal("load accepted result", errors.New("accepted result belongs to another task"))
	}
	return PublicResult{TaskID: id, AssuranceProfile: string(record.Level())}, nil
}
