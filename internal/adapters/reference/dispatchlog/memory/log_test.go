package memory

import (
	"context"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/dispatchlogtest"
	"github.com/hat47x/eki-fabric/internal/spi/dispatchlog"
)

func TestLogContract(t *testing.T) {
	dispatchlogtest.RunContract(t, func(t testing.TB) dispatchlog.Log {
		t.Helper()
		return New()
	})
}

func TestZeroValueLogIsUsable(t *testing.T) {
	var log Log
	ctx := context.Background()
	effect := dispatchlog.EffectID("zero.value")

	if err := log.RecordAttempt(ctx, effect, 1); err != nil {
		t.Fatalf("RecordAttempt() on zero Log error = %v", err)
	}
	got, ok, err := log.Latest(ctx, effect)
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if got != 1 || !ok {
		t.Fatalf("Latest() = (%d, %t), want (1, true)", got, ok)
	}
}
