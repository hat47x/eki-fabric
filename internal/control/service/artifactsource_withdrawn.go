package service

import (
	"context"

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
)

// taskWithdrawal answers whether the Task a body came from was withdrawn
// (ADR-0037 §9). It is an interface so the retrieval path can be tested without a
// state store, and so a deployment that keeps no tasks can pass nil.
type taskWithdrawal interface {
	TaskWithdrawn(ctx context.Context, taskID string) (bool, error)
}

// withdrawnFor asks the state whether this body may still be served. A body with
// no Task recorded is served as before: the record predates the field, and telling
// its requester "withdrawn" would be a guess.
//
// A read failure is not treated as "withdrawn": that would stop a legitimate
// retrieval because the state store hiccuped. It is not treated as "active" either
// - the caller logs it - so the operator can see that one answer was made without
// the fact.
func (a artifactSource) withdrawnFor(ctx context.Context, meta artifactfile.Meta) (bool, error) {
	if a.tasks == nil || meta.TaskID == "" {
		return false, nil
	}
	withdrawn, err := a.tasks.TaskWithdrawn(ctx, meta.TaskID)
	if err != nil {
		return false, err
	}
	return withdrawn, nil
}
