package trustbundle

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func approvalFor(version uint64, op Operation, issuer byte) Approval {
	return Approval{
		Version: version, Operation: op, Issuer: contractSHA(issuer),
		ApprovedBy: "operator@example.org", ApprovedAt: base,
	}
}

func contractSHA(n byte) contract.Digest { return contract.SHA256([]byte{n}) }

func nextVersion(add ...Issuer) Bundle {
	b := validBundle()
	b.Version = 2
	b.NotAfter = b.NotAfter.Add(24 * time.Hour)
	b.Issuers = append(b.Issuers, add...)
	return b
}

func TestAnAdditionWithItsApprovalIsAccepted(t *testing.T) {
	previous := validBundle()
	next := nextVersion(issuer(9, base, base.Add(365*24*time.Hour)))
	err := CheckTransition(previous, next, []Approval{approvalFor(2, OperationAdd, 9)})
	if err != nil {
		t.Fatalf("CheckTransition() error = %v", err)
	}
}

func TestAnAdditionWithoutItsApprovalIsRefused(t *testing.T) {
	previous := validBundle()
	next := nextVersion(issuer(9, base, base.Add(365*24*time.Hour)))
	err := CheckTransition(previous, next, nil)
	if !errors.Is(err, ErrUnapproved) {
		t.Fatalf("error = %v, want %v", err, ErrUnapproved)
	}
}

func TestARemovalNeedsTheRemoveOperation(t *testing.T) {
	previous := validBundle()
	previous.Issuers = append(previous.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	next := validBundle()
	next.Version = 2
	next.NotAfter = previous.NotAfter.Add(24 * time.Hour)
	if err := CheckTransition(previous, next, []Approval{approvalFor(2, OperationAdd, 9)}); !errors.Is(err, ErrUnapproved) {
		t.Fatalf("add-approval-for-removal error = %v, want %v", err, ErrUnapproved)
	}
	if err := CheckTransition(previous, next, []Approval{approvalFor(2, OperationRemove, 9)}); err != nil {
		t.Fatalf("CheckTransition() error = %v", err)
	}
}

func TestAnApprovalForAnUnchangedIssuerIsRefused(t *testing.T) {
	previous := validBundle()
	next := nextVersion(issuer(9, base, base.Add(365*24*time.Hour)))
	err := CheckTransition(previous, next, []Approval{
		approvalFor(2, OperationAdd, 9),
		approvalFor(2, OperationAdd, 1),
	})
	if !errors.Is(err, ErrUnapproved) {
		t.Fatalf("error = %v, want %v", err, ErrUnapproved)
	}
}

func TestAnApprovalForAnotherVersionIsRefused(t *testing.T) {
	previous := validBundle()
	next := nextVersion(issuer(9, base, base.Add(365*24*time.Hour)))
	err := CheckTransition(previous, next, []Approval{approvalFor(3, OperationAdd, 9)})
	if !errors.Is(err, ErrUnapproved) {
		t.Fatalf("error = %v, want %v", err, ErrUnapproved)
	}
}

func TestAnIncompleteApprovalIsRefused(t *testing.T) {
	previous := validBundle()
	next := nextVersion(issuer(9, base, base.Add(365*24*time.Hour)))
	missingApprover := approvalFor(2, OperationAdd, 9)
	missingApprover.ApprovedBy = ""
	if err := CheckTransition(previous, next, []Approval{missingApprover}); !errors.Is(err, ErrUnapproved) {
		t.Fatalf("error = %v, want %v", err, ErrUnapproved)
	}
}

func approvalForBundle(version uint64, op Operation, issuerByte byte) Approval {
	return Approval{
		Version: version, Operation: op, Issuer: contractSHA(issuerByte),
		ApprovedBy: "operator@example.org", ApprovedAt: base,
	}
}
