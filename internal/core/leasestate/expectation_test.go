package leasestate

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/placementverify"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

// fakeApproval records the Current snapshot it was asked to revalidate and
// returns a configured error. Real approvals are covered by placementverify.
type fakeApproval struct {
	calls int
	got   placementverify.Current
	err   error
}

func (f *fakeApproval) Revalidate(cur placementverify.Current) error {
	f.calls++
	f.got = cur
	return f.err
}

type reservationFixture struct {
	task    taskstate.TaskRecord
	attempt attemptstate.AttemptRecord
	now     time.Time
}

func newReservationFixture(t *testing.T) reservationFixture {
	t.Helper()
	taskID, err := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	digest := contract.SHA256([]byte("reservation-task-ir"))
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

	task := taskstate.TaskRecord{
		ID:           taskID,
		TaskIRDigest: digest,
		Phase:        taskstate.TaskPhaseReady,
		Revision:     2,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	attempt, err := attemptstate.New(attemptID, taskID, digest, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Phase = attemptstate.AttemptPhasePlacing
	attempt.WorkerIdentity = "worker-a"
	attempt.Revision = 2
	attempt.UpdatedAt = now.Add(time.Second)
	return reservationFixture{task: task, attempt: attempt, now: now}
}

func TestExpectReservationDerivesCurrentRevisions(t *testing.T) {
	fx := newReservationFixture(t)
	approval := &fakeApproval{}

	got, err := ExpectReservation(approval, fx.task, fx.attempt, "policy-42", fx.now)
	if err != nil {
		t.Fatalf("ExpectReservation() error = %v", err)
	}
	want := ReservationExpectation{
		TaskID:                  fx.task.ID,
		TaskIRDigest:            fx.task.TaskIRDigest,
		ExpectedTaskRevision:    fx.task.Revision,
		AttemptID:               fx.attempt.ID,
		ExpectedAttemptRevision: fx.attempt.Revision,
		WorkerIdentity:          "worker-a",
	}
	if got != want {
		t.Fatalf("ExpectReservation() = %#v, want %#v", got, want)
	}

	if approval.calls != 1 {
		t.Fatalf("Revalidate() calls = %d, want 1", approval.calls)
	}
	if approval.got.Task.ID != fx.task.ID ||
		approval.got.AttemptID != fx.attempt.ID ||
		approval.got.WorkerIdentity != fx.attempt.WorkerIdentity ||
		approval.got.PolicyRevision != "policy-42" ||
		!approval.got.Now.Equal(fx.now) {
		t.Fatalf("Revalidate() received %#v, want the current Task, Attempt, Worker, policy and time", approval.got)
	}
}

func TestExpectReservationRejectsAttemptThatIsNotPlacing(t *testing.T) {
	leaseID, err := ids.ParseLeaseID("les_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*attemptstate.AttemptRecord){
		"created": func(a *attemptstate.AttemptRecord) {
			a.Phase = attemptstate.AttemptPhaseCreated
			a.WorkerIdentity = ""
		},
		"assigned": func(a *attemptstate.AttemptRecord) {
			a.Phase = attemptstate.AttemptPhaseAssigned
			a.LeaseID = leaseID
		},
		"closed": func(a *attemptstate.AttemptRecord) {
			a.Phase = attemptstate.AttemptPhaseClosed
			a.LeaseID = ""
			a.CloseReason = attemptstate.CloseReasonSuperseded
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newReservationFixture(t)
			mutate(&fx.attempt)
			approval := &fakeApproval{}

			_, err := ExpectReservation(approval, fx.task, fx.attempt, "policy-42", fx.now)
			if !errors.Is(err, ErrReservationNotCurrent) {
				t.Fatalf("ExpectReservation() error = %v, want ErrReservationNotCurrent", err)
			}
			if approval.calls != 0 {
				t.Fatalf("Revalidate() ran for a non-PLACING Attempt; calls = %d", approval.calls)
			}
		})
	}
}

func TestExpectReservationRejectsAttemptBoundElsewhere(t *testing.T) {
	t.Run("other-task", func(t *testing.T) {
		fx := newReservationFixture(t)
		otherTask, err := ids.ParseTaskID("tsk_00000000000000000000000000000002")
		if err != nil {
			t.Fatal(err)
		}
		fx.attempt.TaskID = otherTask
		if _, err := ExpectReservation(&fakeApproval{}, fx.task, fx.attempt, "policy-42", fx.now); !errors.Is(err, ErrReservationNotCurrent) {
			t.Fatalf("ExpectReservation() error = %v, want ErrReservationNotCurrent", err)
		}
	})

	t.Run("other-taskir", func(t *testing.T) {
		fx := newReservationFixture(t)
		fx.attempt.TaskIRDigest = contract.SHA256([]byte("another-task-ir"))
		if _, err := ExpectReservation(&fakeApproval{}, fx.task, fx.attempt, "policy-42", fx.now); !errors.Is(err, ErrReservationNotCurrent) {
			t.Fatalf("ExpectReservation() error = %v, want ErrReservationNotCurrent", err)
		}
	})
}

func TestExpectReservationPropagatesApprovalFailure(t *testing.T) {
	fx := newReservationFixture(t)
	stale := errors.New("approval no longer matches the current Attempt")
	approval := &fakeApproval{err: stale}

	_, err := ExpectReservation(approval, fx.task, fx.attempt, "policy-42", fx.now)
	if !errors.Is(err, ErrReservationNotCurrent) {
		t.Fatalf("ExpectReservation() error = %v, want ErrReservationNotCurrent", err)
	}
	if !errors.Is(err, stale) {
		t.Fatalf("ExpectReservation() error = %v, want the approval failure preserved", err)
	}
}

func TestExpectReservationRequiresApproval(t *testing.T) {
	fx := newReservationFixture(t)
	if _, err := ExpectReservation(nil, fx.task, fx.attempt, "policy-42", fx.now); !errors.Is(err, ErrReservationNotCurrent) {
		t.Fatalf("ExpectReservation(nil) error = %v, want ErrReservationNotCurrent", err)
	}
}

func TestExpectReservationRejectsInvalidAttemptSnapshot(t *testing.T) {
	fx := newReservationFixture(t)
	fx.attempt.Revision = 0
	if _, err := ExpectReservation(&fakeApproval{}, fx.task, fx.attempt, "policy-42", fx.now); !errors.Is(err, ErrReservationNotCurrent) {
		t.Fatalf("ExpectReservation() error = %v, want ErrReservationNotCurrent", err)
	}
}
