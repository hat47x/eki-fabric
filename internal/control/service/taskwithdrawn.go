package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// taskRecordReader is the part of the state store this adapter needs.
type taskRecordReader interface {
	LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error)
}

// taskWithdrawalFromState answers whether a Task was withdrawn by reading its
// record (ADR-0037 §9, #592).
//
// A Task that is not in the store answers "not withdrawn": the body's record
// outlived the task record, and guessing "withdrawn" would take a result away from
// its requester on the strength of a missing row. Any other read error is returned,
// so the caller can log that one answer was given without the fact.
type taskWithdrawalFromState struct {
	tasks taskRecordReader
}

func (t taskWithdrawalFromState) TaskWithdrawn(ctx context.Context, taskID string) (bool, error) {
	if t.tasks == nil {
		return false, nil
	}
	id, err := ids.ParseTaskID(taskID)
	if err != nil {
		return false, fmt.Errorf("service: task id in the artifact record: %w", err)
	}
	record, err := t.tasks.LoadTask(ctx, id)
	if errors.Is(err, statestore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("service: load task: %w", err)
	}
	return record.Withdrawn(), nil
}
