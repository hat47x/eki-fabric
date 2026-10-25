package testprofile

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

func testClaim(t *testing.T, status resultclaim.Status) resultclaim.Claim {
	t.Helper()
	cfg := resultclaim.ClaimConfig{
		TaskID: "tsk_1", TaskIRDigest: contract.SHA256([]byte("ir")), AttemptID: "atm_1",
		WorkerIdentity: "worker-a", LeaseID: "les_1", FenceEpoch: 1<<63 + 5, WorkOrderDecision: "wo-1",
		ExecutorDigest: contract.SHA256([]byte("ex")), Nonce: []byte("0123456789abcdef"),
		Status: status, OutputBytes: 1<<63 + 7,
		StartedAt: time.Date(2026, 10, 25, 1, 2, 3, 123456789, time.UTC),
		EndedAt:   time.Date(2026, 10, 25, 1, 2, 4, 987654321, time.UTC),
	}
	if status == resultclaim.StatusCompleted {
		cfg.OutputDigest = contract.SHA256([]byte("out"))
	}
	c, err := resultclaim.NewClaim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClaimRoundtripKeepsEveryField(t *testing.T) {
	for _, status := range []resultclaim.Status{resultclaim.StatusCompleted, resultclaim.StatusReclaimed} {
		claim := testClaim(t, status)
		raw, err := EncodeClaim(claim)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeClaim(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(claim) || got.Digest() != claim.Digest() {
			t.Fatalf("%s: claim changed in the roundtrip", status)
		}
	}
}

func TestDecodeClaimRefusesMalformedBodies(t *testing.T) {
	raw, err := EncodeClaim(testClaim(t, resultclaim.StatusCompleted))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for name, bad := range map[string]string{
		"unknown field":   strings.Replace(body, `{`, `{"extra":1,`, 1),
		"trailing data":   body + `{}`,
		"bad digest":      strings.Replace(body, `"task_ir_digest":"`, `"task_ir_digest":"zz`, 1),
		"not json":        `nope`,
		"empty":           ``,
		"zero fence":      strings.Replace(body, `"fence_epoch":9223372036854775813`, `"fence_epoch":0`, 1),
		"negative uint64": strings.Replace(body, `"fence_epoch":9223372036854775813`, `"fence_epoch":-1`, 1),
	} {
		if bad == body {
			t.Fatalf("%s: mutation did not change the body", name)
		}
		if _, err := DecodeClaim([]byte(bad)); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("%s: error = %v, want ErrInvalidMessage", name, err)
		}
	}
}

func TestAckRoundtrip(t *testing.T) {
	want := workerplane.ClaimAck{Decision: "new", AttemptPhase: "verifying", VerificationRequired: true}
	raw, err := EncodeAck(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeAck(raw)
	if err != nil || got != want {
		t.Fatalf("ack = %+v, %v", got, err)
	}
	if _, err := DecodeAck([]byte(`{"decision":"new","surprise":true}`)); err == nil {
		t.Fatal("unknown ack field accepted")
	}
}
