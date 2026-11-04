package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/control/claimbody"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// attemptOwner resolves the Principal that may read a body: the submitter of the
// Task the Attempt ran for (#514).
//
// It reads rather than infers. The Worker identity that sent the body says who
// ran the work, not who asked for it, and only the requester may read the result.
type attemptOwner struct {
	attempts   attemptLoader
	admissions admissionLoader
}

type attemptLoader interface {
	LoadAttempt(ctx context.Context, id ids.AttemptID) (attemptstate.AttemptRecord, error)
}

type admissionLoader interface {
	LoadAdmission(ctx context.Context, id ids.TaskID) (statestore.Admission, error)
}

func (o attemptOwner) LoadAttemptOwner(ctx context.Context, attemptID string) (string, error) {
	attempt, err := o.attempts.LoadAttempt(ctx, ids.AttemptID(attemptID))
	if err != nil {
		return "", fmt.Errorf("service: load attempt: %w", err)
	}
	admission, err := o.admissions.LoadAdmission(ctx, attempt.TaskID)
	if errors.Is(err, statestore.ErrNotFound) {
		// No admission record means this Control Plane cannot say who asked for
		// the work, so it cannot say who may read the body either (#520).
		return "", claimbody.ErrNoOwner
	}
	if err != nil {
		return "", fmt.Errorf("service: load admission: %w", err)
	}
	if admission.Principal == "" {
		return "", fmt.Errorf("service: the task has no principal")
	}
	return admission.Principal, nil
}

// LoadAttemptTask names the Task an Attempt ran for, so a body can later be
// checked against the Task's cancellation state (ADR-0037 §9, #592).
func (o attemptOwner) LoadAttemptTask(ctx context.Context, attemptID string) (string, error) {
	attempt, err := o.attempts.LoadAttempt(ctx, ids.AttemptID(attemptID))
	if err != nil {
		return "", fmt.Errorf("service: load attempt: %w", err)
	}
	return attempt.TaskID.String(), nil
}
