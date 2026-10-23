package recovery

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func released(t *testing.T, tasks ...int) (Policy, Decision) {
	t.Helper()
	policy := testPolicy()
	var pending []Pending
	for _, task := range tasks {
		pending = append(pending, pendingFor(t, task, PriorityNormal, time.Time{}))
	}
	d, err := Release(baseTime, policy, pending, Budget{}, 1)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if len(d.Permits) != len(tasks) {
		t.Fatalf("released %d, want %d", len(d.Permits), len(tasks))
	}
	return policy, d
}

func TestPendingValidateRejectsWhatReleaseRejects(t *testing.T) {
	good := pendingFor(t, 1, PriorityNormal, time.Time{})
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	cases := map[string]func(*Pending){
		"no task":         func(p *Pending) { p.TaskID = "" },
		"no digest":       func(p *Pending) { p.TaskIRDigest = contract.Digest{} },
		"no previous":     func(p *Pending) { p.PreviousAttemptID = "" },
		"wrong next":      func(p *Pending) { p.NextAttemptNumber = 5 },
		"over the budget": func(p *Pending) { p.MaxAttempts = 1 },
		"unknown rank":    func(p *Pending) { p.Priority = Priority(9) },
		"no start time":   func(p *Pending) { p.NotBefore = time.Time{} },
	}
	for name, mutate := range cases {
		p := good
		mutate(&p)
		if err := p.Validate(); !errors.Is(err, ErrInvalidPending) {
			t.Fatalf("%s: Validate() error = %v, want %v", name, err, ErrInvalidPending)
		}
	}
}

func TestPermitSurvivesBeingStoredAndRestored(t *testing.T) {
	_, d := released(t, 1)
	original := d.Permits[0]
	restored, err := RestorePermit(original.Record())
	if err != nil {
		t.Fatalf("RestorePermit() error = %v", err)
	}
	if restored.Digest() != original.Digest() || restored != original {
		t.Fatalf("restored = %+v, want %+v", restored, original)
	}
	subject := Subject{TaskID: original.TaskID(), TaskIRDigest: original.TaskIRDigest(), PreviousAttemptID: original.PreviousAttemptID(), PolicyRevision: original.PolicyRevision()}
	if err := restored.Validate(baseTime.Add(2*time.Second), subject); err != nil {
		t.Fatalf("a restored Permit is not usable: %v", err)
	}
}

func TestRestorePermitRefusesAnEditedRowOrAnInventedOne(t *testing.T) {
	_, d := released(t, 1)
	good := d.Permits[0].Record()
	cases := map[string]func(*PermitRecord){
		"a later expiry":     func(r *PermitRecord) { r.ExpiresAt = r.ExpiresAt.Add(time.Hour) },
		"another policy":     func(r *PermitRecord) { r.PolicyRevision++ },
		"another attempt":    func(r *PermitRecord) { r.NextAttemptNumber++ },
		"a larger estimate":  func(r *PermitRecord) { r.EstimatedComputeMillis++ },
		"a different task":   func(r *PermitRecord) { r.TaskID = mustTaskID(t, 99) },
		"a missing digest":   func(r *PermitRecord) { r.Digest = contract.Digest{} },
		"a missing sequence": func(r *PermitRecord) { r.Sequence = 0 },
		"an unusable window": func(r *PermitRecord) { r.ExpiresAt = r.NotBefore.Add(-time.Second) },
	}
	for name, mutate := range cases {
		r := good
		mutate(&r)
		if _, err := RestorePermit(r); !errors.Is(err, ErrInvalidPermit) {
			t.Fatalf("%s: RestorePermit() error = %v, want %v", name, err, ErrInvalidPermit)
		}
	}
}

func TestCheckReleaseAcceptsWhatReleaseProduced(t *testing.T) {
	policy, d := released(t, 1, 2)
	if err := CheckRelease(baseTime, policy, Budget{}, d); err != nil {
		t.Fatalf("CheckRelease() error = %v", err)
	}
}

func TestCheckReleaseRefusesADecisionThatUnderReportsConsumption(t *testing.T) {
	policy, d := released(t, 1, 2)
	cases := map[string]func(*Decision){
		"fewer releases":    func(d *Decision) { d.Budget.Released-- },
		"less transfer":     func(d *Decision) { d.Budget.TransferBytes = 0 },
		"less compute":      func(d *Decision) { d.Budget.ComputeMillis-- },
		"another window":    func(d *Decision) { d.Budget.WindowStart = d.Budget.WindowStart.Add(time.Millisecond) },
		"another sequence":  func(d *Decision) { d.Sequence++ },
		"another policy":    func(d *Decision) { d.PolicyRevision++ },
		"a Permit dropped":  func(d *Decision) { d.Permits = d.Permits[:1] },
		"a Permit repeated": func(d *Decision) { d.Permits = append(d.Permits, d.Permits[0]) },
	}
	for name, mutate := range cases {
		copyOf := d
		copyOf.Permits = append([]Permit(nil), d.Permits...)
		mutate(&copyOf)
		if err := CheckRelease(baseTime, policy, Budget{}, copyOf); err == nil {
			t.Fatalf("%s: CheckRelease() accepted the decision", name)
		}
	}
}

func TestCheckReleaseCarriesAWindowAndOnlyAWindow(t *testing.T) {
	policy := testPolicy()
	policy.MaxReleasesPerWindow = 1
	first, err := Release(baseTime, policy, []Pending{pendingFor(t, 1, PriorityNormal, time.Time{})}, Budget{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Inside the window the consumed budget carries over.
	same := baseTime.Add(500 * time.Millisecond)
	blocked, err := Release(same, policy, []Pending{pendingFor(t, 2, PriorityNormal, time.Time{})}, first.Budget, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRelease(same, policy, first.Budget, blocked); err != nil {
		t.Fatalf("a window that carried over was refused: %v", err)
	}
	// A pass that claims a fresh budget inside the same window is refused.
	forged := blocked
	forged.Budget = Budget{WindowStart: same}
	if err := CheckRelease(same, policy, first.Budget, forged); err == nil {
		t.Fatal("a budget reset inside the window was accepted")
	}

	// After the window ends the budget starts again.
	later := baseTime.Add(2 * time.Second)
	next, err := Release(later, policy, []Pending{pendingFor(t, 2, PriorityNormal, time.Time{})}, first.Budget, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRelease(later, policy, first.Budget, next); err != nil {
		t.Fatalf("a new window was refused: %v", err)
	}
	if err := CheckRelease(baseTime.Add(-time.Second), policy, first.Budget, next); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("a clock that moved back error = %v, want %v", err, ErrClockRegression)
	}
}
