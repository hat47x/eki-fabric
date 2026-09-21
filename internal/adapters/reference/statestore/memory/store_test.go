package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func newTaskForTest(t *testing.T) taskstate.TaskRecord {
	t.Helper()
	id, err := ids.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() error = %v", err)
	}
	record, err := taskstate.NewTask(
		id,
		contract.SHA256([]byte("task-ir")),
		time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	return record
}

func TestCreateLoadAndCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	store := New()
	record := newTaskForTest(t)

	if err := store.CreateTask(ctx, record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	loaded, err := store.LoadTask(ctx, record.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if loaded != record {
		t.Fatalf("loaded = %#v, want %#v", loaded, record)
	}

	next := loaded
	next.Phase = taskstate.TaskPhaseReady
	next.Revision = 2
	next.UpdatedAt = next.UpdatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(ctx, record.ID, 1, next); err != nil {
		t.Fatalf("CompareAndSwapTask() error = %v", err)
	}

	got, err := store.LoadTask(ctx, record.ID)
	if err != nil {
		t.Fatalf("LoadTask() after CAS error = %v", err)
	}
	if got.Revision != 2 || got.Phase != taskstate.TaskPhaseReady {
		t.Fatalf("got revision/phase = %d/%q", got.Revision, got.Phase)
	}
}

func TestCompareAndSwapRejectsStaleRevision(t *testing.T) {
	ctx := context.Background()
	store := New()
	record := newTaskForTest(t)
	if err := store.CreateTask(ctx, record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	next := record
	next.Phase = taskstate.TaskPhaseReady
	next.Revision = 2
	next.UpdatedAt = next.UpdatedAt.Add(time.Second)
	if err := store.CompareAndSwapTask(ctx, record.ID, 1, next); err != nil {
		t.Fatalf("first CompareAndSwapTask() error = %v", err)
	}

	stale := next
	stale.Phase = taskstate.TaskPhaseActive
	stale.Revision = 2
	stale.UpdatedAt = stale.UpdatedAt.Add(time.Second)
	err := store.CompareAndSwapTask(ctx, record.ID, 1, stale)
	if !errors.Is(err, statestore.ErrConflict) {
		t.Fatalf("stale CAS error = %v, want ErrConflict", err)
	}
}

func TestCreateRejectsDuplicateTask(t *testing.T) {
	ctx := context.Background()
	store := New()
	record := newTaskForTest(t)
	if err := store.CreateTask(ctx, record); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	err := store.CreateTask(ctx, record)
	if !errors.Is(err, statestore.ErrAlreadyExists) {
		t.Fatalf("duplicate CreateTask() error = %v, want ErrAlreadyExists", err)
	}
}

func TestLoadHonorsCancelledContext(t *testing.T) {
	store := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.LoadTask(ctx, ids.TaskID("tsk_0123456789abcdef0123456789abcdef"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadTask() error = %v, want context.Canceled", err)
	}
}
