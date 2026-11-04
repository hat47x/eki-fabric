package clientapi

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/measurement"
)

func TestARefusedAuditIsCountedRatherThanIgnored(t *testing.T) {
	counters := measurement.New()
	h := &Handler{
		log:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		counters:         counters,
		artifactAuditErr: func(ArtifactAccess) error { return errors.New("sink down") },
	}
	h.audit("alice", "sha256:abc", ArtifactServed)
	if got := counters.Total(measurement.AuditUnrecorded); got != 1 {
		t.Fatalf("audit.unrecorded = %d, want 1: a gap in the record must not be silent", got)
	}
}

func TestAnAcceptedAuditIsNotCounted(t *testing.T) {
	counters := measurement.New()
	h := &Handler{
		log:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		counters:         counters,
		artifactAuditErr: func(ArtifactAccess) error { return nil },
	}
	h.audit("alice", "sha256:abc", ArtifactServed)
	if got := counters.Total(measurement.AuditUnrecorded); got != 0 {
		t.Fatalf("audit.unrecorded = %d, want 0", got)
	}
}
