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
	// ListTasksByPhase returns at most limit tasks whose current phase is the
	// requested one, oldest creation first. It backs the Control Plane's
	// dispatch loop, which must find work without knowing Task IDs in
	// advance. limit must be positive; a store may return fewer records than
	// exist. A store need not accept a terminal-phase query: a terminal Task
	// is found through the Task it belongs to, not by scanning for it.
	ListTasksByPhase(ctx context.Context, phase taskstate.TaskPhase, limit int) ([]taskstate.TaskRecord, error)
}
