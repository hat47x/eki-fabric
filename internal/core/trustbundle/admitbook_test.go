package trustbundle

import (
	"errors"
	"testing"
	"time"
)

func TestAVersionIsAdmittedWithTheApprovalsInTheBook(t *testing.T) {
	pub, priv := testKey(t)
	store, err := NewStore(mustSign(t, validBundle(), priv))
	if err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 2
	next.NotAfter = next.NotAfter.Add(24 * time.Hour)
	next.Issuers = append(next.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	signed := mustSign(t, next, priv)

	book := NewApprovalBook()
	if err := store.AdmitFromBook(signed, pub, book, base.Add(time.Hour)); !errors.Is(err, ErrUnapproved) {
		t.Fatalf("error = %v, want %v", err, ErrUnapproved)
	}
	if err := book.Add(approvalForBundle(2, OperationAdd, 9)); err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitFromBook(signed, pub, book, base.Add(time.Hour)); err != nil {
		t.Fatalf("AdmitFromBook() error = %v", err)
	}
	if got := book.Len(2); got != 0 {
		t.Fatalf("the book kept %d approvals after the version was stored", got)
	}
	if current, err := store.Current(); err != nil || current.Bundle.Version != 2 {
		t.Fatalf("current = %+v, %v", current, err)
	}
}

func TestAFailedAdmissionKeepsTheApprovals(t *testing.T) {
	pub, priv := testKey(t)
	store, err := NewStore(mustSign(t, validBundle(), priv))
	if err != nil {
		t.Fatal(err)
	}
	next := validBundle()
	next.Version = 3 // skips a version on purpose
	next.NotAfter = next.NotAfter.Add(24 * time.Hour)
	next.Issuers = append(next.Issuers, issuer(9, base, base.Add(365*24*time.Hour)))
	book := NewApprovalBook()
	if err := book.Add(approvalForBundle(3, OperationAdd, 9)); err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitFromBook(mustSign(t, next, priv), pub, book, base.Add(time.Hour)); err == nil {
		t.Fatal("AdmitFromBook() accepted a version that does not follow")
	}
	if got := book.Len(3); got != 1 {
		t.Fatalf("the book dropped the approvals: %d", got)
	}
}

func TestAdmittingWithoutABookIsRefused(t *testing.T) {
	pub, priv := testKey(t)
	store, err := NewStore(mustSign(t, validBundle(), priv))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitFromBook(mustSign(t, validBundle(), priv), pub, nil, base); !errors.Is(err, ErrUnapproved) {
		t.Fatalf("error = %v, want %v", err, ErrUnapproved)
	}
}
