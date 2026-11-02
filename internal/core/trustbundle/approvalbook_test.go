package trustbundle

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func anApproval(version uint64, by string, at time.Time) Approval {
	return Approval{
		Version: version, Operation: OperationAdd, Issuer: contract.SHA256([]byte("ca")),
		ApprovedBy: by, ApprovedAt: at, Justification: "new authority",
	}
}

func TestApprovalsAreKeptPerVersion(t *testing.T) {
	book := NewApprovalBook()
	base := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	if err := book.Add(anApproval(2, "alice", base)); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := book.Add(anApproval(2, "bob", base.Add(time.Minute))); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if got := book.Len(2); got != 2 {
		t.Fatalf("Len(2) = %d, want 2", got)
	}
	if got := book.Len(1); got != 0 {
		t.Fatalf("Len(1) = %d, want 0", got)
	}
}

func TestApprovalsComeBackOldestFirst(t *testing.T) {
	book := NewApprovalBook()
	base := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	if err := book.Add(anApproval(2, "bob", base.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if err := book.Add(anApproval(2, "alice", base)); err != nil {
		t.Fatal(err)
	}
	got := book.For(2)
	if len(got) != 2 || got[0].ApprovedBy != "alice" {
		t.Fatalf("approvals = %+v", got)
	}
}

func TestTheSameOperatorCannotApproveTheSameThingTwice(t *testing.T) {
	book := NewApprovalBook()
	base := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	if err := book.Add(anApproval(2, "alice", base)); err != nil {
		t.Fatal(err)
	}
	if err := book.Add(anApproval(2, "alice", base.Add(time.Hour))); !errors.Is(err, ErrDuplicateApproval) {
		t.Fatalf("error = %v, want %v", err, ErrDuplicateApproval)
	}
	if got := book.Len(2); got != 1 {
		t.Fatalf("Len(2) = %d, want 1", got)
	}
}

func TestTheSameOperatorMayApproveADifferentOperation(t *testing.T) {
	book := NewApprovalBook()
	base := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	if err := book.Add(anApproval(2, "alice", base)); err != nil {
		t.Fatal(err)
	}
	removal := anApproval(2, "alice", base)
	removal.Operation = OperationRemove
	if err := book.Add(removal); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if got := book.Len(2); got != 2 {
		t.Fatalf("Len(2) = %d, want 2", got)
	}
}

func TestTheBookRefusesAnIncompleteApproval(t *testing.T) {
	book := NewApprovalBook()
	bad := anApproval(0, "", time.Now())
	bad.Justification = ""
	if err := book.Add(bad); err == nil {
		t.Fatal("Add() accepted an incomplete approval")
	}
}

func TestReleaseDropsTheApprovalsOfAVersion(t *testing.T) {
	book := NewApprovalBook()
	base := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	if err := book.Add(anApproval(2, "alice", base)); err != nil {
		t.Fatal(err)
	}
	if err := book.Add(anApproval(3, "alice", base)); err != nil {
		t.Fatal(err)
	}
	book.Release(2)
	if got := book.Len(2); got != 0 {
		t.Fatalf("Len(2) = %d, want 0", got)
	}
	if got := book.Len(3); got != 1 {
		t.Fatalf("Len(3) = %d, want 1", got)
	}
}

func TestTheReturnedSliceIsACopy(t *testing.T) {
	book := NewApprovalBook()
	base := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	if err := book.Add(anApproval(2, "alice", base)); err != nil {
		t.Fatal(err)
	}
	got := book.For(2)
	got[0].ApprovedBy = "mallory"
	if again := book.For(2); again[0].ApprovedBy != "alice" {
		t.Fatalf("the book changed: %+v", again)
	}
}
