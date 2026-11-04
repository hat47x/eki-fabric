package service

import (
	"context"
	"errors"
	"testing"

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
)

type fakeWithdrawal struct {
	withdrawn bool
	err       error
	calls     int
}

func (f *fakeWithdrawal) TaskWithdrawn(context.Context, string) (bool, error) {
	f.calls++
	return f.withdrawn, f.err
}

func TestAWithdrawnTaskIsReportedAsWithdrawn(t *testing.T) {
	source := artifactSource{tasks: &fakeWithdrawal{withdrawn: true}}
	got, err := source.withdrawnFor(context.Background(), artifactfile.Meta{TaskID: "tsk_1"})
	if err != nil || !got {
		t.Fatalf("withdrawnFor() = %v, %v; want true, nil", got, err)
	}
}

func TestABodyWithoutATaskIsNotWithdrawn(t *testing.T) {
	fake := &fakeWithdrawal{withdrawn: true}
	source := artifactSource{tasks: fake}
	got, err := source.withdrawnFor(context.Background(), artifactfile.Meta{})
	if err != nil || got {
		t.Fatalf("withdrawnFor() = %v, %v; want false, nil", got, err)
	}
	if fake.calls != 0 {
		t.Fatalf("the state was asked %d times for a body with no Task", fake.calls)
	}
}

func TestAStateFailureIsReportedRatherThanGuessed(t *testing.T) {
	source := artifactSource{tasks: &fakeWithdrawal{err: errors.New("boom")}}
	got, err := source.withdrawnFor(context.Background(), artifactfile.Meta{TaskID: "tsk_1"})
	if err == nil {
		t.Fatal("withdrawnFor() hid a state failure")
	}
	if got {
		t.Fatal("withdrawnFor() reported withdrawn on a failure: that would stop a legitimate retrieval")
	}
}

func TestWithoutAReaderNothingIsWithdrawn(t *testing.T) {
	source := artifactSource{}
	got, err := source.withdrawnFor(context.Background(), artifactfile.Meta{TaskID: "tsk_1"})
	if err != nil || got {
		t.Fatalf("withdrawnFor() = %v, %v; want false, nil", got, err)
	}
}
