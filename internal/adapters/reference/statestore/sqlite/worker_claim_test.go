package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

// newWorker creates one registered Worker through the enrollment path and returns
// its identity, so the tests below start from a row the adapter wrote itself.
func newWorker(t *testing.T, store *Store) string {
	t.Helper()
	svc, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: fixedClock{registryTestNow}})
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
	return string(worker.Identity)
}

// A row written before the claim existed reads as "claimed nothing": NULL is the
// same as no claim, so an old database needs no backfill (ADR-0029 §5.1).
func TestAWorkerWithoutAClaimReadsAsNoClaim(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	t.Cleanup(func() { _ = store.Close() })
	identity := newWorker(t, store)

	worker, err := store.LoadWorker(context.Background(), ids.WorkerIdentity(identity))
	if err != nil {
		t.Fatal(err)
	}
	if len(worker.HeldArtifacts) != 0 {
		t.Fatalf("HeldArtifacts = %v, want none", worker.HeldArtifacts)
	}
}

// The stored form round-trips, so a claim written by a future update path reads
// back as the same digests.
func TestAClaimRoundTripsThroughItsStoredForm(t *testing.T) {
	held := contract.SHA256([]byte("artifact-a"))
	encoded, ok := encodeHeldArtifacts(workerregistry.HeldArtifacts{held}).(string)
	if !ok {
		t.Fatalf("encodeHeldArtifacts() = %T, want a string", encodeHeldArtifacts(workerregistry.HeldArtifacts{held}))
	}
	decoded, err := decodeHeldArtifacts(sql.NullString{String: encoded, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Holds(held) {
		t.Fatalf("decoded = %v, want the stored digest", decoded)
	}
}

// NULL, an empty value and a zero claim all mean "claimed nothing".
func TestAnEmptyStoredClaimReadsAsNoClaim(t *testing.T) {
	for _, value := range []sql.NullString{{}, {String: "", Valid: true}} {
		held, err := decodeHeldArtifacts(value)
		if err != nil || len(held) != 0 {
			t.Fatalf("decodeHeldArtifacts(%+v) = %v, %v", value, held, err)
		}
	}
	if encodeHeldArtifacts(workerregistry.HeldArtifacts{}) != nil {
		t.Fatalf("encodeHeldArtifacts(empty) = %v, want NULL", encodeHeldArtifacts(workerregistry.HeldArtifacts{}))
	}
}

// A stored value that is not a digest is corruption, not a hint to be ignored: a
// half-read claim would steer placement with parts nobody checked.
func TestACorruptStoredClaimIsRefused(t *testing.T) {
	if _, err := decodeHeldArtifacts(sql.NullString{String: "not-a-digest", Valid: true}); err == nil {
		t.Fatal("decodeHeldArtifacts() accepted a value that is not a digest")
	}
	if _, err := decodeHeldArtifacts(sql.NullString{String: "aa,bb", Valid: true}); err == nil {
		t.Fatal("decodeHeldArtifacts() accepted short digests")
	}
}
