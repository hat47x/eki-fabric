package service

import (
	"context"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

type fakeAttempts struct{ record attemptstate.AttemptRecord }

func (f fakeAttempts) LoadAttempt(context.Context, ids.AttemptID) (attemptstate.AttemptRecord, error) {
	return f.record, nil
}

func TestTheOwnerLookupAlsoNamesTheTask(t *testing.T) {
	taskID := ids.TaskID("tsk_00000000000000000000000000000001")
	owner := attemptOwner{attempts: fakeAttempts{record: attemptstate.AttemptRecord{TaskID: taskID}}}
	got, err := owner.LoadAttemptTask(context.Background(), "att_00000000000000000000000000000001")
	if err != nil {
		t.Fatalf("LoadAttemptTask() error = %v", err)
	}
	if got != taskID.String() {
		t.Fatalf("task = %q, want %q", got, taskID.String())
	}
}
