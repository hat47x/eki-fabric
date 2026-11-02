package claimbody

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

// --- fixtures -------------------------------------------------------------

type fakeAttempts struct {
	record attemptstate.AttemptRecord
	err    error
}

func (f fakeAttempts) LoadAttempt(context.Context, ids.AttemptID) (attemptstate.AttemptRecord, error) {
	return f.record, f.err
}

type fakeLeases struct{ worker string }

func (f fakeLeases) LoadLeaseWorker(context.Context, ids.LeaseID) (string, error) {
	return f.worker, nil
}

type fakeClaims struct {
	claim resultclaim.Claim
	found bool
}

func (f fakeClaims) LoadResultClaim(context.Context, ids.AttemptID) (resultclaim.Claim, bool, error) {
	return f.claim, f.found, nil
}

type fakeSink struct {
	gotBody    []byte
	gotClaimed string
	gotMax     int64
	digest     string
	size       int64
	err        error
}

func (f *fakeSink) Put(body []byte, claimed string, max int64) (string, int64, error) {
	f.gotBody, f.gotClaimed, f.gotMax = body, claimed, max
	if f.err != nil {
		return "", 0, f.err
	}
	return f.digest, f.size, nil
}

func attemptFor(worker string) attemptstate.AttemptRecord {
	taskID, _ := ids.NewTaskID()
	attemptID, _ := ids.NewAttemptID()
	rec, err := attemptstate.New(attemptID, taskID, contract.SHA256(nil), 1, time.Now())
	if err != nil {
		panic(err)
	}
	return rec
}

func completedClaim(body []byte) resultclaim.Claim {
	attempt := attemptFor("worker-a")
	leaseID, _ := ids.NewLeaseID()
	cfg := resultclaim.ClaimConfig{
		TaskID:            attempt.TaskID,
		TaskIRDigest:      attempt.TaskIRDigest,
		AttemptID:         attempt.ID,
		WorkerIdentity:    "worker-a",
		LeaseID:           leaseID,
		FenceEpoch:        1,
		WorkOrderDecision: "WOR-1",
		ExecutorDigest:    contract.SHA256(nil),
		Nonce:             make([]byte, 16),
		Status:            resultclaim.StatusCompleted,
		OutputBytes:       uint64(len(body)),
		OutputDigest:      contract.SHA256(body),
		StartedAt:         time.Now().Add(-time.Second),
		EndedAt:           time.Now(),
	}
	c, err := resultclaim.NewClaim(cfg)
	if err != nil {
		panic(err)
	}
	return c
}

func receiver(t *testing.T, attempts AttemptStore, claims ClaimStore, leases LeaseStore, sink BodySink) *Receiver {
	t.Helper()
	r, err := New(attempts, claims, leases, sink)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// --- tests ----------------------------------------------------------------

func TestAReceivedBodyIsMeasuredAgainstTheClaimItBindsTo(t *testing.T) {
	body := []byte("the output that was claimed")
	claim := completedClaim(body)
	attempt := attemptFor("worker-a")
	sink := &fakeSink{digest: claim.OutputDigest().Hex(), size: int64(len(body))}
	r := receiver(t, fakeAttempts{record: attempt}, fakeClaims{claim: claim, found: true}, fakeLeases{worker: "worker-a"}, sink)

	kept, err := r.Receive(context.Background(), attempt.ID, "worker-a", body, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Digest != claim.OutputDigest().Hex() || kept.Size != int64(len(body)) {
		t.Fatalf("kept = %s, %d; want %s, %d", kept.Digest, kept.Size, claim.OutputDigest().Hex(), len(body))
	}
	if sink.gotClaimed != claim.OutputDigest().Hex() {
		t.Fatalf("the sink was not told the claimed digest; got %q", sink.gotClaimed)
	}
	if kept.MediaType != "text/plain" {
		t.Fatalf("declared media type %q was not recorded", kept.MediaType)
	}
}

func TestABodyFromAnotherWorkerIsRefused(t *testing.T) {
	attempt := attemptFor("worker-a")
	claim := completedClaim([]byte("out"))
	r := receiver(t, fakeAttempts{record: attempt}, fakeClaims{claim: claim, found: true}, fakeLeases{worker: "worker-a"}, &fakeSink{})

	if _, err := r.Receive(context.Background(), attempt.ID, "worker-b", []byte("out"), ""); !errors.Is(err, ErrWorkerMismatch) {
		t.Fatalf("Receive error = %v; want ErrWorkerMismatch", err)
	}
}

func TestABodyWithoutACompletedClaimHasNothingToBindTo(t *testing.T) {
	attempt := attemptFor("worker-a")
	r := receiver(t, fakeAttempts{record: attempt}, fakeClaims{found: false}, fakeLeases{worker: "worker-a"}, &fakeSink{})

	if _, err := r.Receive(context.Background(), attempt.ID, "worker-a", []byte("out"), ""); !errors.Is(err, ErrNoCompletedClaim) {
		t.Fatalf("Receive error = %v; want ErrNoCompletedClaim", err)
	}
}

func TestADisagreeingBodyIsNotKept(t *testing.T) {
	body := []byte("what arrived")
	claim := completedClaim([]byte("what was claimed"))
	attempt := attemptFor("worker-a")
	// The sink measures what arrived and refuses the mismatch, which is the
	// behavior the file store has and the contract requires.
	sink := &fakeSink{err: ErrDigestMismatch}
	r := receiver(t, fakeAttempts{record: attempt}, fakeClaims{claim: claim, found: true}, fakeLeases{worker: "worker-a"}, sink)

	if _, err := r.Receive(context.Background(), attempt.ID, "worker-a", body, ""); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Receive error = %v; want ErrDigestMismatch", err)
	}
}

func TestTheMaximumComesFromTheClaimNotTheRequest(t *testing.T) {
	claim := completedClaim([]byte("0123456789"))
	attempt := attemptFor("worker-a")
	sink := &fakeSink{digest: claim.OutputDigest().Hex(), size: 10}
	r := receiver(t, fakeAttempts{record: attempt}, fakeClaims{claim: claim, found: true}, fakeLeases{worker: "worker-a"}, sink)

	if _, err := r.Receive(context.Background(), attempt.ID, "worker-a", []byte("0123456789"), ""); err != nil {
		t.Fatal(err)
	}
	if sink.gotMax != int64(claim.OutputBytes()) {
		t.Fatalf("the sink's maximum was %d; want the claim's %d", sink.gotMax, claim.OutputBytes())
	}
}

func TestAnUnknownAttemptIsRefusedBeforeAnyIO(t *testing.T) {
	r := receiver(t, fakeAttempts{err: ErrUnknownAttempt}, fakeClaims{}, fakeLeases{worker: "worker-a"}, &fakeSink{})
	if _, err := r.Receive(context.Background(), "attempt-x", "worker-a", []byte("out"), ""); !errors.Is(err, ErrUnknownAttempt) {
		t.Fatalf("Receive error = %v; want ErrUnknownAttempt", err)
	}
}

func TestAMissingDependencyIsAConfigurationError(t *testing.T) {
	for name, cfg := range map[string]struct {
		a AttemptStore
		c ClaimStore
		l LeaseStore
		b BodySink
	}{
		"no attempts": {c: fakeClaims{}, l: fakeLeases{}, b: &fakeSink{}},
		"no claims":   {a: fakeAttempts{}, l: fakeLeases{}, b: &fakeSink{}},
		"no leases":   {a: fakeAttempts{}, c: fakeClaims{}, b: &fakeSink{}},
		"no sink":     {a: fakeAttempts{}, c: fakeClaims{}, l: fakeLeases{}},
	} {
		if _, err := New(cfg.a, cfg.c, cfg.l, cfg.b); !strings.Contains(err.Error(), ErrInvalidConfig.Error()) {
			t.Fatalf("%s: New error = %v; want ErrInvalidConfig", name, err)
		}
	}
}
