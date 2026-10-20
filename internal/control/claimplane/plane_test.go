package claimplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

var baseTime = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)

type fakeAcceptor struct {
	calls int
	claim resultclaim.Claim
	err   error
}

func (f *fakeAcceptor) Accept(_ context.Context, claim resultclaim.Claim) (claimaccept.Result, error) {
	f.calls++
	f.claim = claim
	return claimaccept.Result{}, f.err
}

func claim(t *testing.T, worker string) resultclaim.Claim {
	t.Helper()
	record, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID:         ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest:   contract.SHA256([]byte("taskir")),
		AttemptID:      ids.AttemptID("atm_00000000000000000000000000000001"),
		WorkerIdentity: worker, LeaseID: ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch: 7, WorkOrderDecision: "wor-001",
		ExecutorDigest: contract.SHA256([]byte("executable")),
		Nonce:          []byte("0123456789abcdef"), Status: resultclaim.StatusReclaimed,
		ExitCode: 0, OutputBytes: 10,
		StartedAt: baseTime, EndedAt: baseTime.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestSubmitAcceptsTheClaimOfTheAuthenticatedWorker(t *testing.T) {
	acceptor := &fakeAcceptor{}
	service, err := New(acceptor)
	if err != nil {
		t.Fatal(err)
	}
	c := claim(t, "worker-a")
	if _, err := service.Submit(context.Background(), Principal{WorkerIdentity: "worker-a", TrustDomain: "example.org"}, c); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if acceptor.calls != 1 || !acceptor.claim.Equal(c) {
		t.Fatalf("acceptor = %+v, want the submitted claim", acceptor)
	}
}

func TestSubmitRejectsAClaimForAnotherWorker(t *testing.T) {
	acceptor := &fakeAcceptor{}
	service, err := New(acceptor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), Principal{WorkerIdentity: "worker-a"}, claim(t, "worker-b")); !errors.Is(err, ErrNotClaimOwner) {
		t.Fatalf("Submit() error = %v, want %v", err, ErrNotClaimOwner)
	}
	if acceptor.calls != 0 {
		t.Fatal("the acceptor was called for a claim from another Worker")
	}
}

func TestSubmitRejectsAnInvalidPrincipalOrClaim(t *testing.T) {
	acceptor := &fakeAcceptor{}
	service, err := New(acceptor)
	if err != nil {
		t.Fatal(err)
	}
	c := claim(t, "worker-a")
	if _, err := service.Submit(context.Background(), Principal{}, c); !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("empty principal error = %v, want %v", err, ErrInvalidPrincipal)
	}
	if _, err := service.Submit(context.Background(), Principal{WorkerIdentity: "worker-a"}, resultclaim.Claim{}); !errors.Is(err, resultclaim.ErrInvalidClaim) {
		t.Fatalf("zero claim error = %v, want %v", err, resultclaim.ErrInvalidClaim)
	}
	if _, err := service.Submit(nil, Principal{WorkerIdentity: "worker-a"}, c); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil context error = %v, want %v", err, ErrInvalidConfig)
	}
	if _, err := New(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(nil) error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestSubmitPropagatesTheAcceptorResult(t *testing.T) {
	acceptor := &fakeAcceptor{err: claimaccept.ErrBindingMismatch}
	service, err := New(acceptor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), Principal{WorkerIdentity: "worker-a"}, claim(t, "worker-a")); !errors.Is(err, claimaccept.ErrBindingMismatch) {
		t.Fatalf("Submit() error = %v, want the acceptor error", err)
	}
}
