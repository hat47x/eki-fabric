package workerplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type fakeAcceptor struct {
	calls  int
	claim  resultclaim.Claim
	result claimaccept.Result
	err    error
}

func (f *fakeAcceptor) Accept(_ context.Context, claim resultclaim.Claim) (claimaccept.Result, error) {
	f.calls++
	f.claim = claim
	return f.result, f.err
}

func validClaim(t *testing.T) resultclaim.Claim {
	t.Helper()
	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID:         ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest:   contract.SHA256([]byte("taskir")),
		AttemptID:      ids.AttemptID("atm_00000000000000000000000000000001"),
		WorkerIdentity: "worker-a", LeaseID: ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch: 7, WorkOrderDecision: "wor-001",
		ExecutorDigest: contract.SHA256([]byte("executable")),
		Nonce:          []byte("0123456789abcdef"), Status: resultclaim.StatusCompleted,
		ExitCode: 0, OutputBytes: 10,
		StartedAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 10, 21, 0, 0, 1, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

func newServer(t *testing.T, acceptor claimplane.Acceptor) *Server {
	t.Helper()
	plane, err := claimplane.New(acceptor)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{Claims: plane})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestSubmitClaimMapsTheResultToAnAck(t *testing.T) {
	acceptor := &fakeAcceptor{result: claimaccept.Result{
		Decision:             resultclaim.Decision{Kind: resultclaim.KindNew},
		Attempt:              attemptstate.AttemptRecord{Phase: attemptstate.AttemptPhaseVerifying},
		VerificationRequired: true,
	}}
	server := newServer(t, acceptor)
	claim := validClaim(t)
	ack, err := server.SubmitClaim(context.Background(), workerplane.Principal{WorkerIdentity: "worker-a"}, claim)
	if err != nil {
		t.Fatalf("SubmitClaim() error = %v", err)
	}
	if ack.Decision != string(resultclaim.KindNew) || ack.AttemptPhase != string(attemptstate.AttemptPhaseVerifying) || !ack.VerificationRequired {
		t.Fatalf("ack = %+v", ack)
	}
	if acceptor.calls != 1 || !acceptor.claim.Equal(claim) {
		t.Fatalf("acceptor = %+v", acceptor)
	}
}

func TestSubmitClaimRejectsAnotherWorkerBeforeTheAcceptor(t *testing.T) {
	acceptor := &fakeAcceptor{}
	server := newServer(t, acceptor)
	if _, err := server.SubmitClaim(context.Background(), workerplane.Principal{WorkerIdentity: "worker-b"}, validClaim(t)); !errors.Is(err, claimplane.ErrNotClaimOwner) {
		t.Fatalf("SubmitClaim() error = %v, want %v", err, claimplane.ErrNotClaimOwner)
	}
	if acceptor.calls != 0 {
		t.Fatal("the acceptor was called for another Worker's claim")
	}
}

func TestSubmitClaimPropagatesAcceptanceErrors(t *testing.T) {
	acceptor := &fakeAcceptor{err: claimaccept.ErrBindingMismatch}
	server := newServer(t, acceptor)
	if _, err := server.SubmitClaim(context.Background(), workerplane.Principal{WorkerIdentity: "worker-a"}, validClaim(t)); !errors.Is(err, claimaccept.ErrBindingMismatch) {
		t.Fatalf("SubmitClaim() error = %v, want the acceptance error", err)
	}
}

func TestNewRejectsMissingClaimPlane(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want %v", err, ErrInvalidConfig)
	}
}
