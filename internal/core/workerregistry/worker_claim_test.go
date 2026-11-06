package workerregistry

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func workerWithClaim(t *testing.T, claim HeldArtifacts) Worker {
	t.Helper()
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	worker, err := NewWorker(ids.WorkerIdentity("wrk_00000000000000000000000000000001"), Credential{ID: "enr_00000000000000000000000000000001", TrustDomain: "example.org"}, now)
	if err != nil {
		t.Fatal(err)
	}
	worker.TrustDomain = "example.org"
	worker.HeldArtifacts = claim
	return worker
}

// The claim is part of the record, and a claim that does not validate is refused
// where the record is checked: half-trusted lists would steer placement with
// unknown parts (ADR-0029 §5.1).
func TestAValidClaimIsPartOfTheRecord(t *testing.T) {
	worker := workerWithClaim(t, HeldArtifacts{contract.SHA256([]byte("a"))})
	if err := worker.Validate(); err != nil {
		t.Fatalf("Validate() = %v; worker=%+v", err, worker)
	}
}

func TestARecordWithABrokenClaimIsRefused(t *testing.T) {
	worker := workerWithClaim(t, HeldArtifacts{{}})
	if !errors.Is(worker.Validate(), ErrInvalidWorker) {
		t.Fatalf("Validate() = %v, want %v", worker.Validate(), ErrInvalidWorker)
	}
}

func TestARecordWithoutAClaimIsValid(t *testing.T) {
	if err := workerWithClaim(t, nil).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want a record without a claim accepted", err)
	}
}
