package claimplane

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

func TestAMalformedClaimIsCountedAsInvalid(t *testing.T) {
	reasons := []string{}
	s := &Service{acceptor: nilAcceptor{}, onRefusal: func(reason string) { reasons = append(reasons, reason) }}
	if _, err := s.Submit(t.Context(), Principal{WorkerIdentity: "wk_alice"}, resultclaim.Claim{}); err == nil {
		t.Fatal("Submit() accepted an empty claim")
	}
	if len(reasons) != 1 || reasons[0] != "invalid" {
		t.Fatalf("reasons = %v, want [invalid]", reasons)
	}
}

func TestAMissingIdentityIsCounted(t *testing.T) {
	reasons := []string{}
	s := &Service{acceptor: nilAcceptor{}, onRefusal: func(reason string) { reasons = append(reasons, reason) }}
	if _, err := s.Submit(t.Context(), Principal{}, resultclaim.Claim{}); err == nil {
		t.Fatal("Submit() accepted a claim with no identity")
	}
	if len(reasons) != 1 || reasons[0] != "identity" {
		t.Fatalf("reasons = %v, want [identity]", reasons)
	}
}

func TestWithoutAHookARefusalStillHappens(t *testing.T) {
	s := &Service{acceptor: nilAcceptor{}}
	if _, err := s.Submit(t.Context(), Principal{WorkerIdentity: "wk_alice"}, resultclaim.Claim{}); !errors.Is(err, resultclaim.ErrInvalidClaim) {
		t.Fatalf("error = %v, want %v", err, resultclaim.ErrInvalidClaim)
	}
}

// nilAcceptor stands in for claimaccept when the test never reaches it.
type nilAcceptor struct{}

func (nilAcceptor) Accept(_ context.Context, _ resultclaim.Claim) (claimaccept.Result, error) {
	return claimaccept.Result{}, errors.New("not reached")
}
