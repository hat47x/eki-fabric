package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

const (
	attemptX = ids.AttemptID("atm_0000000000000000000000000000000a")
	attemptY = ids.AttemptID("atm_0000000000000000000000000000000b")
)

type sentBody struct {
	attempt ids.AttemptID
	body    []byte
}

type recordingBodySender struct {
	sent []sentBody
	err  error
}

func (r *recordingBodySender) SendBody(_ context.Context, attempt ids.AttemptID, body []byte, _ string) error {
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, sentBody{attempt: attempt, body: append([]byte(nil), body...)})
	return nil
}

type okClaimSender struct{ err error }

func (o okClaimSender) Send(context.Context, resultclaim.Claim) error { return o.err }

func completedClaim(t *testing.T, attempt ids.AttemptID, body []byte) resultclaim.Claim {
	t.Helper()
	src := validClaimSource()
	src.attemptID = attempt
	now := time.Now().UTC()
	claim, err := claimFromOutcome(src, "worker-test", executor.Outcome{
		Status: executor.StatusCompleted, OutputBytes: uint64(len(body)),
		OutputDigest: contract.SHA256(body), StartedAt: now, EndedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

func TestBodyHoldKeepsBodiesPerAttempt(t *testing.T) {
	hold := newBodyHold()
	source := []byte("abc")
	hold.Put(attemptX, source)
	hold.Put(attemptY, []byte("xyz"))
	source[0] = 'Z' // the hold keeps its own copy

	if got, ok := hold.Take(attemptX); !ok || string(got) != "abc" {
		t.Fatalf("Attempt X got %q ok=%v, want abc", got, ok)
	}
	if _, ok := hold.Take(attemptX); ok {
		t.Fatal("a body was taken twice")
	}
	if got, ok := hold.Take(attemptY); !ok || string(got) != "xyz" {
		t.Fatalf("Attempt Y got %q ok=%v, want xyz", got, ok)
	}
}

func TestBodyHoldIgnoresEmptyBodiesAndNilHolds(t *testing.T) {
	hold := newBodyHold()
	hold.Put(attemptX, nil)
	if _, ok := hold.Take(attemptX); ok {
		t.Fatal("an empty body was kept")
	}
	var none *bodyHold
	none.Put(attemptX, []byte("abc"))
	if _, ok := none.Take(attemptX); ok {
		t.Fatal("a nil hold returned a body")
	}
}

// AB-3: Attempt X's claim is delivered after Attempt Y has produced its output.
// X must get X's bytes, not the most recent output.
func TestSendingBodiesSendsEachAttemptItsOwnBody(t *testing.T) {
	hold := newBodyHold()
	hold.Put(attemptX, []byte("abc"))
	hold.Put(attemptY, []byte("xyz"))
	bodies := &recordingBodySender{}
	s := sendingBodies{next: okClaimSender{}, sender: bodies, bodies: hold, events: &eventLog{}, log: slog.Default()}

	if err := s.Send(context.Background(), completedClaim(t, attemptX, []byte("abc"))); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), completedClaim(t, attemptY, []byte("xyz"))); err != nil {
		t.Fatal(err)
	}
	if len(bodies.sent) != 2 ||
		bodies.sent[0].attempt != attemptX || string(bodies.sent[0].body) != "abc" ||
		bodies.sent[1].attempt != attemptY || string(bodies.sent[1].body) != "xyz" {
		t.Fatalf("unexpected bodies sent: %+v", bodies.sent)
	}
}

func TestSendingBodiesSendsNothingWhenTheClaimWasNotAcknowledged(t *testing.T) {
	hold := newBodyHold()
	hold.Put(attemptX, []byte("abc"))
	bodies := &recordingBodySender{}
	wantErr := errors.New("control plane down")
	s := sendingBodies{next: okClaimSender{err: wantErr}, sender: bodies, bodies: hold, events: &eventLog{}, log: slog.Default()}

	if err := s.Send(context.Background(), completedClaim(t, attemptX, []byte("abc"))); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if len(bodies.sent) != 0 {
		t.Fatalf("a body was sent before its claim was acknowledged: %+v", bodies.sent)
	}
	// The claim is redelivered later; the body must still be there.
	if _, ok := hold.Take(attemptX); !ok {
		t.Fatal("the body was dropped although its claim was not acknowledged")
	}
}

// AB-4: the claim is acknowledged but the body does not arrive. The claim is
// not failed (the queue must not resend it), and the failure is recorded
// under its own name, not as a queued claim.
func TestSendingBodiesRecordsABodyThatDidNotArrive(t *testing.T) {
	hold := newBodyHold()
	hold.Put(attemptX, []byte("abc"))
	eventsPath := filepath.Join(t.TempDir(), "events.jsonl")
	bodies := &recordingBodySender{err: errors.New("receiver refused")}
	s := sendingBodies{next: okClaimSender{}, sender: bodies, bodies: hold, events: &eventLog{path: eventsPath}, log: slog.Default()}

	if err := s.Send(context.Background(), completedClaim(t, attemptX, []byte("abc"))); err != nil {
		t.Fatalf("a body failure failed the acknowledged claim: %v", err)
	}
	raw, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(eventBodySendFailed)) || strings.Contains(string(raw), eventClaimQueued) {
		t.Fatalf("events = %s, want %s and no %s", raw, eventBodySendFailed, eventClaimQueued)
	}
}
