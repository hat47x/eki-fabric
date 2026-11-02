package service

import (
	"context"
	"fmt"

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
	if err != nil {
		return "", fmt.Errorf("service: load admission: %w", err)
	}
	if admission.Principal == "" {
		return "", fmt.Errorf("service: the task has no principal")
	}
	return admission.Principal, nil
}
