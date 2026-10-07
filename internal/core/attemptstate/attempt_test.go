package attemptstate

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func TestAttemptLifecycleBindingsValidate(t *testing.T) {
	base := validAttempt(t)

	placing := base
	placing.Phase = AttemptPhasePlacing
	placing.WorkerIdentity = "worker-a"
	placing.Revision++
	placing.UpdatedAt = placing.UpdatedAt.Add(time.Second)
	if err := placing.Validate(); err != nil {
		t.Fatalf("placing Validate() error = %v", err)
	}

	assigned := placing
	assigned.Phase = AttemptPhaseAssigned
	assigned.LeaseID = mustLeaseID(t)
	assigned.Revision++
	assigned.UpdatedAt = assigned.UpdatedAt.Add(time.Second)
	if err := assigned.Validate(); err != nil {
		t.Fatalf("assigned Validate() error = %v", err)
	}

	closed := assigned
	closed.Phase = AttemptPhaseClosed
	closed.CloseReason = CloseReasonReclaimed
	closed.Revision++
	closed.UpdatedAt = closed.UpdatedAt.Add(time.Second)
	if err := closed.Validate(); err != nil {
		t.Fatalf("closed Validate() error = %v", err)
	}
}

func TestAttemptRejectsImpossibleBindings(t *testing.T) {
	base := validAttempt(t)
	lease := mustLeaseID(t)

	cases := map[string]func(*AttemptRecord){
		"zero number": func(r *AttemptRecord) { r.Number = 0 },
		"unknown phase": func(r *AttemptRecord) { r.Phase = "unknown" },
		"created worker": func(r *AttemptRecord) { r.WorkerIdentity = "worker-a" },
		"placing without worker": func(r *AttemptRecord) { r.Phase = AttemptPhasePlacing },
		"placing with lease": func(r *AttemptRecord) {
			r.Phase = AttemptPhasePlacing
			r.WorkerIdentity = "worker-a"
			r.LeaseID = lease
		},
		"assigned without lease": func(r *AttemptRecord) {
			r.Phase = AttemptPhaseAssigned
			r.WorkerIdentity = "worker-a"
		},
		"lease without worker": func(r *AttemptRecord) { r.LeaseID = lease },
		"close reason while open": func(r *AttemptRecord) { r.CloseReason = CloseReasonCancelled },
		"closed without reason": func(r *AttemptRecord) { r.Phase = AttemptPhaseClosed },
		"unknown close reason": func(r *AttemptRecord) {
			r.Phase = AttemptPhaseClosed
			r.CloseReason = "unknown"
		},
		"zero revision": func(r *AttemptRecord) { r.Revision = 0 },
		"updated before created": func(r *AttemptRecord) { r.UpdatedAt = r.CreatedAt.Add(-time.Second) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			record := base
			mutate(&record)
			if err := record.Validate(); !errors.Is(err, ErrInvalidAttemptRecord) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAttemptRecord", err)
			}
		})
	}
}

func validAttempt(t testing.TB) AttemptRecord {
	t.Helper()
	attemptID, err := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	record, err := New(
		attemptID,
		taskID,
		contract.SHA256([]byte("task-ir")),
		1,
		time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func mustLeaseID(t testing.TB) ids.LeaseID {
	t.Helper()
	id, err := ids.ParseLeaseID("les_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
