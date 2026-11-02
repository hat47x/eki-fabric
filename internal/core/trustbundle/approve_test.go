package trustbundle

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestAnApprovalBookRecordsThroughApprove(t *testing.T) {
	book := NewApprovalBook()
	approval := Approval{
		Version: 2, Operation: OperationAdd, Issuer: contract.SHA256([]byte("ca")),
		ApprovedBy: "alice", ApprovedAt: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
		Justification: "new authority",
	}
	if err := book.Approve(approval); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if got := book.Len(2); got != 1 {
		t.Fatalf("Len(2) = %d, want 1", got)
	}
	if err := book.Approve(approval); !errors.Is(err, ErrDuplicateApproval) {
		t.Fatalf("error = %v, want %v", err, ErrDuplicateApproval)
	}
}

func TestTheBookSatisfiesTheApproverShape(t *testing.T) {
	var approver interface{ Approve(Approval) error } = NewApprovalBook()
	if approver == nil {
		t.Fatal("the book does not satisfy the approver shape")
	}
}
