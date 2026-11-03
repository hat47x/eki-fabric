package operatorapi

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
)

// A refusal from the sink must reach the caller: an approval or an admission that
// nobody can look up afterwards is what ADR-0040 §5 refuses.
func TestRecordReportsARefusedSink(t *testing.T) {
	h := &Handler{
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:      func() time.Time { return time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC) },
		auditErr: func(Access) error { return errors.New("sink down") },
	}
	identity := operatorcert.Identity{Name: "alice", Role: operatorcert.RoleOperator}
	if err := h.record(identity, "add", "sha256:00", OutcomeApproved); err == nil {
		t.Fatal("record() hid a refused sink")
	}
}

func TestRecordAcceptsASuccessfulSink(t *testing.T) {
	h := &Handler{
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:      func() time.Time { return time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC) },
		auditErr: func(Access) error { return nil },
	}
	identity := operatorcert.Identity{Name: "alice", Role: operatorcert.RoleOperator}
	if err := h.record(identity, "add", "sha256:00", OutcomeApproved); err != nil {
		t.Fatalf("record() error = %v", err)
	}
}
