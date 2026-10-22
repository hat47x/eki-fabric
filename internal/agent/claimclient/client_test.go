package claimclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type fakeAPI struct {
	calls     int
	principal workerplane.Principal
	claim     resultclaim.Claim
	err       error
	ack       workerplane.ClaimAck
}

func (f *fakeAPI) SubmitClaim(_ context.Context, principal workerplane.Principal, claim resultclaim.Claim) (workerplane.ClaimAck, error) {
	f.calls++
	f.principal = principal
	f.claim = claim
	return f.ack, f.err
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
		StartedAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 10, 21, 0, 0, 1, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestSendUsesTheAgentPrincipal(t *testing.T) {
	api := &fakeAPI{ack: workerplane.ClaimAck{Decision: "new", AttemptPhase: "closed"}}
	client, err := New(api, workerplane.Principal{WorkerIdentity: "worker-a", TrustDomain: "example.org"})
	if err != nil {
		t.Fatal(err)
	}
	c := claim(t, "worker-a")
	if err := client.Send(context.Background(), c); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if api.calls != 1 || api.principal.WorkerIdentity != "worker-a" || !api.claim.Equal(c) {
		t.Fatalf("api = %+v", api)
	}
}

func TestSendRefusesAClaimForAnotherWorker(t *testing.T) {
	api := &fakeAPI{}
	client, err := New(api, workerplane.Principal{WorkerIdentity: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Send(context.Background(), claim(t, "worker-b")); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Send() error = %v, want %v", err, ErrInvalidConfig)
	}
	if api.calls != 0 {
		t.Fatal("the API was called for another Worker's claim")
	}
}

func TestSendPropagatesTransportErrors(t *testing.T) {
	api := &fakeAPI{err: errors.New("control plane unavailable")}
	client, err := New(api, workerplane.Principal{WorkerIdentity: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Send(context.Background(), claim(t, "worker-a")); err != api.err {
		t.Fatalf("Send() replaced the transport error: %v", err)
	}
}

func TestSendRefusesMalformedAcknowledgements(t *testing.T) {
	for name, ack := range map[string]workerplane.ClaimAck{
		"empty":                      {},
		"unknown decision":           {Decision: "accepted", AttemptPhase: "closed"},
		"unknown phase":              {Decision: "new", AttemptPhase: "accepted"},
		"missing phase":              {Decision: "duplicate"},
		"missing verification flag":  {Decision: "new", AttemptPhase: "verifying"},
		"spurious verification flag": {Decision: "duplicate", AttemptPhase: "closed", VerificationRequired: true},
	} {
		t.Run(name, func(t *testing.T) {
			api := &fakeAPI{ack: ack}
			client, err := New(api, workerplane.Principal{WorkerIdentity: "worker-a"})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Send(context.Background(), claim(t, "worker-a")); !errors.Is(err, ErrInvalidAcknowledgement) {
				t.Fatalf("Send() error = %v, want invalid acknowledgement", err)
			}
			if api.calls != 1 {
				t.Fatalf("calls = %d", api.calls)
			}
		})
	}
}

func TestSendAllowsEveryRecordedDecision(t *testing.T) {
	for _, decision := range []string{"new", "duplicate", "stale", "late"} {
		t.Run(decision, func(t *testing.T) {
			api := &fakeAPI{ack: workerplane.ClaimAck{Decision: decision, AttemptPhase: "closed"}}
			client, err := New(api, workerplane.Principal{WorkerIdentity: "worker-a"})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Send(context.Background(), claim(t, "worker-a")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	if _, err := New(nil, workerplane.Principal{WorkerIdentity: "worker-a"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil api error = %v, want %v", err, ErrInvalidConfig)
	}
	if _, err := New(&fakeAPI{}, workerplane.Principal{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty principal error = %v, want %v", err, ErrInvalidConfig)
	}
}
