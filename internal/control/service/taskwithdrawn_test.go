package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type fakeTasks struct {
	record taskstate.TaskRecord
	err    error
}

func (f fakeTasks) LoadTask(context.Context, ids.TaskID) (taskstate.TaskRecord, error) {
	return f.record, f.err
}

func TestARunningTaskIsNotWithdrawn(t *testing.T) {
	reader := taskWithdrawalFromState{tasks: fakeTasks{record: taskstate.TaskRecord{Phase: taskstate.TaskPhaseActive}}}
	got, err := reader.TaskWithdrawn(context.Background(), "tsk_00000000000000000000000000000001")
	if err != nil || got {
		t.Fatalf("TaskWithdrawn() = %v, %v; want false, nil", got, err)
	}
}

func TestACancelledTaskIsWithdrawn(t *testing.T) {
	reader := taskWithdrawalFromState{tasks: fakeTasks{record: taskstate.TaskRecord{TerminalOutcome: taskstate.TerminalOutcomeCancelled}}}
	got, err := reader.TaskWithdrawn(context.Background(), "tsk_00000000000000000000000000000001")
	if err != nil || !got {
		t.Fatalf("TaskWithdrawn() = %v, %v; want true, nil", got, err)
	}
}

func TestAMissingTaskIsNotWithdrawn(t *testing.T) {
	reader := taskWithdrawalFromState{tasks: fakeTasks{err: statestore.ErrNotFound}}
	got, err := reader.TaskWithdrawn(context.Background(), "tsk_00000000000000000000000000000001")
	if err != nil || got {
		t.Fatalf("TaskWithdrawn() = %v, %v; want false, nil: a missing row is not a decision", got, err)
	}
}

func TestAStoreFailureIsReported(t *testing.T) {
	reader := taskWithdrawalFromState{tasks: fakeTasks{err: errors.New("boom")}}
	if _, err := reader.TaskWithdrawn(context.Background(), "tsk_00000000000000000000000000000001"); err == nil {
		t.Fatal("TaskWithdrawn() hid a store failure")
	}
}

func TestABadTaskIDInTheRecordIsReported(t *testing.T) {
	reader := taskWithdrawalFromState{tasks: fakeTasks{}}
	if _, err := reader.TaskWithdrawn(context.Background(), "not-a-task"); err == nil {
		t.Fatal("TaskWithdrawn() accepted a malformed task id")
	}
}

func TestWithoutATaskReaderNothingIsWithdrawn(t *testing.T) {
	var reader taskWithdrawalFromState
	got, err := reader.TaskWithdrawn(context.Background(), "tsk_00000000000000000000000000000001")
	if err != nil || got {
		t.Fatalf("TaskWithdrawn() = %v, %v; want false, nil", got, err)
	}
}
