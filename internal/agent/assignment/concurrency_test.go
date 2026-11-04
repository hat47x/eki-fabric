package assignment

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

type guardFinisher struct{ done chan struct{} }

func (h guardFinisher) Start(context.Context, Ready) error { return nil }

func (h guardFinisher) Finished(ids.AttemptID) <-chan struct{} { return h.done }

// Running more than one Attempt at a time needs reservations, and reservations need
// a Handler that says when an Attempt ended. The configuration is refused for that
// reason rather than admitted on capacity that is already in use (#354).
func TestMoreThanOneAttemptNeedsAFinishingHandler(t *testing.T) {
	_, err := New(Config{MaxConcurrent: 2, Handler: &plainHandler{}})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want %v", err, ErrInvalidConfig)
	}
	if !strings.Contains(err.Error(), "at once") {
		t.Fatalf("error %q does not name the concurrency limit", err)
	}
}

// One at a time stays allowed with a Handler that cannot report its end: that is
// today's serial behaviour, and it needs no reservations.
func TestOneAtATimeDoesNotNeedAFinisher(t *testing.T) {
	_, err := New(Config{MaxConcurrent: 1, Handler: &plainHandler{}})
	if err == nil {
		t.Fatal("New() accepted an empty config")
	}
	if strings.Contains(err.Error(), "at once") {
		t.Fatalf("error %q refuses the serial configuration", err)
	}
}
