package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"

	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type scriptedClaims struct {
	ack workerplane.ClaimAck
	err error
}

func (s scriptedClaims) SubmitClaim(context.Context, workerplane.Principal, resultclaim.Claim) (workerplane.ClaimAck, error) {
	return s.ack, s.err
}

func readEventLines(t *testing.T, path string) []event {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []event
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var ev event
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func testClaim(t *testing.T) resultclaim.Claim {
	t.Helper()
	claim, err := claimFromOutcome(validClaimSource(), "worker-test", completedOutcomeForTest())
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

// A failed send and the later success must both appear in events_file, so a
// reader can tell a claim still on its way from one the Control Plane holds.
func TestRecordingClaimsDistinguishesFailureFromDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	events := &eventLog{path: path}
	claim := testClaim(t)
	failing := recordingClaims{next: scriptedClaims{err: errors.New("control down")}, events: events, log: slog.Default()}
	if _, err := failing.SubmitClaim(context.Background(), testPrincipal, claim); err == nil {
		t.Fatal("transport error was swallowed")
	}
	ok := recordingClaims{next: scriptedClaims{ack: workerplane.ClaimAck{Decision: "new"}}, events: events, log: slog.Default()}
	if _, err := ok.SubmitClaim(context.Background(), testPrincipal, claim); err != nil {
		t.Fatal(err)
	}
	got := readEventLines(t, path)
	if len(got) != 2 {
		t.Fatalf("events = %d, want 2", len(got))
	}
	if got[0].Outcome != eventClaimDeliveryFailed || got[0].Failure != "control down" || got[0].ClaimDecision != "" {
		t.Fatalf("failure event = %+v", got[0])
	}
	if got[1].Outcome != eventClaimSubmitted || got[1].ClaimDecision != "new" || got[1].Failure != "" {
		t.Fatalf("delivery event = %+v", got[1])
	}
	if got[0].AttemptID != string(claim.AttemptID()) {
		t.Fatalf("attempt id = %q", got[0].AttemptID)
	}
}

func completedOutcomeForTest() executor.Outcome {
	now := time.Now().UTC()
	return executor.Outcome{
		Status: executor.StatusCompleted, OutputBytes: 5, OutputDigest: contract.SHA256([]byte("hello")),
		StartedAt: now, EndedAt: now.Add(time.Second),
	}
}
