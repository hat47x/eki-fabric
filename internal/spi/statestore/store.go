package statestore

import (
	"context"
	"errors"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

var (
	ErrNotFound      = errors.New("state record not found")
	ErrAlreadyExists = errors.New("state record already exists")
	ErrConflict      = errors.New("state revision conflict")
)

type TaskStore interface {
	CreateTask(ctx context.Context, record taskstate.TaskRecord) error
	LoadTask(ctx context.Context, id ids.TaskID) (taskstate.TaskRecord, error)
	CompareAndSwapTask(ctx context.Context, id ids.TaskID, expectedRevision uint64, next taskstate.TaskRecord) error
}
