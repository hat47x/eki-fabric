package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

type claimClock struct{ at time.Time }

func (c claimClock) Now() time.Time { return c.at }

var claimNow = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)

func enrolledWorker(t *testing.T, store *Store) workerregistry.Worker {
	t.Helper()
	svc, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: claimClock{claimNow}})
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := workerenrollment.NewAdmin("root")
	cred, err := svc.IssueEnrollmentCredential(context.Background(), admin, workerenrollment.IssueRequest{TrustDomain: "team-a.example"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := svc.ConsumeEnrollmentCredential(context.Background(), cred.Secret.Reveal(), "team-a.example")
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

// The claim is recorded with a new revision, and the rest of the record is left
// alone (ADR-0029 §5.1).
func TestSetHeldArtifactsRecordsTheClaim(t *testing.T) {
	store := New()
	worker := enrolledWorker(t, store)
	held := contract.SHA256([]byte("artifact-a"))

	if err := store.SetHeldArtifacts(context.Background(), worker.Identity, workerregistry.HeldArtifacts{held}, claimNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadWorker(context.Background(), worker.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.HeldArtifacts.Holds(held) {
		t.Fatalf("HeldArtifacts = %v, want the recorded digest", loaded.HeldArtifacts)
	}
	if loaded.Revision != worker.Revision+1 {
		t.Fatalf("Revision = %d, want %d", loaded.Revision, worker.Revision+1)
	}
	if loaded.State != worker.State || loaded.TrustDomain != worker.TrustDomain {
		t.Fatalf("the claim changed more than the claim: %+v", loaded)
	}
}

// A claim that does not validate is refused before anything is written.
func TestSetHeldArtifactsRefusesABrokenClaim(t *testing.T) {
	store := New()
	worker := enrolledWorker(t, store)
	if err := store.SetHeldArtifacts(context.Background(), worker.Identity, workerregistry.HeldArtifacts{{}}, claimNow); !errors.Is(err, workerregistry.ErrHeldArtifacts) {
		t.Fatalf("SetHeldArtifacts() error = %v, want %v", err, workerregistry.ErrHeldArtifacts)
	}
}
