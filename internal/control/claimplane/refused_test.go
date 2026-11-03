package claimplane

import (
	"context"
	"errors"
	"testing"
)

// A claim the acceptor refuses is a refusal this path made visible, and it is
// counted once (#548, ADR-0039 §2.1).
func TestARefusedClaimIsCounted(t *testing.T) {
	acceptor := &fakeAcceptor{err: errors.New("stale claim")}
	service, err := New(acceptor)
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	service.SetRefusalHook(func(reason string) { reasons = append(reasons, reason) })

	refused := claim(t, "worker-a")
	if _, err := service.Submit(context.Background(), Principal{WorkerIdentity: "worker-a", TrustDomain: "example.org"}, refused); err == nil {
		t.Fatal("Submit() accepted a claim the acceptor refused")
	}
	if len(reasons) != 1 || reasons[0] != "rejected" {
		t.Fatalf("reasons = %v, want one \"rejected\"", reasons)
	}
}

// A claim that succeeds is not counted: the number is refusals, not traffic.
func TestAnAcceptedClaimIsNotCountedAsARefusal(t *testing.T) {
	acceptor := &fakeAcceptor{}
	service, err := New(acceptor)
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	service.SetRefusalHook(func(reason string) { reasons = append(reasons, reason) })

	if _, err := service.Submit(context.Background(), Principal{WorkerIdentity: "worker-a", TrustDomain: "example.org"}, claim(t, "worker-a")); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("reasons = %v, want none", reasons)
	}
}
