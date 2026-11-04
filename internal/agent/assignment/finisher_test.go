package assignment

import (
	"context"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

type plainHandler struct{ started int }

func (h *plainHandler) Start(ctx context.Context, ready Ready) error { h.started++; return nil }

type finisherHandler struct {
	plainHandler
	done chan struct{}
}

func (h *finisherHandler) Finished(ids.AttemptID) <-chan struct{} { return h.done }

// A Handler that cannot say when it finished holds no reservation: one nobody
// releases would shrink the endpoint for good (ADR-0031 §6.2).
func TestAPlainHandlerHoldsNoReservation(t *testing.T) {
	if done := completionOf(&plainHandler{}, "atm_x"); done != nil {
		t.Fatalf("completionOf() = %v, want nil for a handler that cannot answer", done)
	}
}

// A Handler that can answer gives the service the channel to wait on.
func TestAFinishingHandlerReportsItsEnd(t *testing.T) {
	channel := make(chan struct{})
	got := completionOf(&finisherHandler{done: channel}, "atm_x")
	if got == nil {
		t.Fatal("completionOf() = nil for a handler that can answer")
	}
	close(channel)
	<-got
}
