package claimwire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

var principal = workerplane.Principal{WorkerIdentity: "worker-観測-a", TrustDomain: "fixture-domain"}

func testCodec(t *testing.T) *codec {
	t.Helper()
	c, err := newCodec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func config(status resultclaim.Status, edge int) resultclaim.ClaimConfig {
	start := time.Date(2026, 10, 23, 1, 2, 3, 123456789, time.FixedZone("fixture", 9*3600))
	cfg := resultclaim.ClaimConfig{
		TaskID: ids.TaskID("tsk_00000000000000000000000000000001"), TaskIRDigest: contract.SHA256([]byte("TaskIR")),
		AttemptID: ids.AttemptID("atm_00000000000000000000000000000002"), WorkerIdentity: principal.WorkerIdentity,
		LeaseID: ids.LeaseID("les_00000000000000000000000000000003"), FenceEpoch: 1,
		WorkOrderDecision: "decision-distinct-from-work-order-id", ExecutorDigest: contract.SHA256([]byte("executor")),
		Nonce: bytes.Repeat([]byte{byte(edge)}, 16+8*edge), Status: status, OutputBytes: uint64(edge),
		StartedAt: start, EndedAt: start.Add(time.Nanosecond),
	}
	if edge != 0 {
		cfg.FenceEpoch, cfg.OutputBytes = math.MaxUint64, math.MaxUint64
		if status != resultclaim.StatusCompleted {
			if edge == 1 {
				cfg.ExitCode = -int(^uint(0)>>1) - 1
			} else {
				cfg.ExitCode = int(^uint(0) >> 1)
			}
		}
	}
	if status == resultclaim.StatusCompleted {
		cfg.OutputDigest = contract.SHA256([]byte("finalized-output-list-fixture"))
	}
	return cfg
}

func makeClaim(t *testing.T, cfg resultclaim.ClaimConfig) resultclaim.Claim {
	t.Helper()
	c, err := resultclaim.NewClaim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClaimProtobufRoundTrip(t *testing.T) {
	c := testCodec(t)
	for _, status := range statuses[1:] {
		for edge := range 3 {
			t.Run(fmt.Sprintf("%s/edge-%d/int-%d", status, edge, strconv.IntSize), func(t *testing.T) {
				original := makeClaim(t, config(status, edge))
				wire, err := c.encodeClaim(principal, original)
				if err != nil {
					t.Fatal(err)
				}
				recovered, err := c.decodeClaim(principal, wire)
				if err != nil || !recovered.Equal(original) || recovered.Digest() != original.Digest() {
					t.Fatalf("claim lost content or digest: %v", err)
				}
				second, err := c.encodeClaim(principal, recovered)
				if err != nil || !bytes.Equal(wire, second) {
					t.Fatalf("re-encoding changed bytes: %v", err)
				}
			})
		}
	}
}

func TestTimestampNanosecondBoundaries(t *testing.T) {
	c := testCodec(t)
	for _, ns := range []int64{math.MinInt64, 0, math.MaxInt64 - 1} {
		t.Run(fmt.Sprint(ns), func(t *testing.T) {
			cfg := config(resultclaim.StatusReclaimed, 0)
			cfg.StartedAt, cfg.EndedAt = time.Unix(0, ns), time.Unix(0, ns+1)
			original := makeClaim(t, cfg)
			wire, err := c.encodeClaim(principal, original)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.decodeClaim(principal, wire)
			if err != nil || !got.Equal(original) || got.Digest() != original.Digest() {
				t.Fatalf("timestamp boundary lost: %v", err)
			}
		})
	}
	cfg := config(resultclaim.StatusReclaimed, 0)
	cfg.StartedAt, cfg.EndedAt = time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2500, 1, 1, 0, 0, 1, 0, time.UTC)
	if _, err := resultclaim.NewClaim(cfg); !errors.Is(err, resultclaim.ErrInvalidClaim) {
		t.Fatalf("Core accepted a timestamp outside its canonical digest range: %v", err)
	}
}

func TestExitOutsideNativeIntRejected(t *testing.T) {
	if strconv.IntSize != 32 {
		t.Skip("the narrowing boundary is exercised by GOARCH=386")
	}
	c := testCodec(t)
	for _, exit := range []int64{math.MinInt64, math.MaxInt64} {
		// Give the attacker the digest of the truncated value. Checking the
		// digest alone would then accept narrowing the supplied sint64.
		cfg := config(resultclaim.StatusFailed, 0)
		cfg.ExitCode = int(exit)
		wire, err := c.encodeClaim(principal, makeClaim(t, cfg))
		if err != nil {
			t.Fatal(err)
		}
		bad := mutated(t, c, "Claim", wire, func(m *dynamicpb.Message) {
			put(m, "exit_code", protoreflect.ValueOfInt64(exit))
		})
		if _, err := c.decodeClaim(principal, bad); err == nil {
			t.Fatalf("out-of-range sint64 %d was narrowed to int32", exit)
		}
	}
}

func mutated(t *testing.T, c *codec, name protoreflect.Name, wire []byte, change func(*dynamicpb.Message)) []byte {
	t.Helper()
	m := c.message(name)
	if err := proto.Unmarshal(wire, m); err != nil {
		t.Fatal(err)
	}
	change(m)
	changed, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

func TestClaimRejectsMissingInvalidAndUnsupportedValues(t *testing.T) {
	c := testCodec(t)
	original := makeClaim(t, config(resultclaim.StatusCompleted, 0))
	wire, err := c.encodeClaim(principal, original)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []protoreflect.Name{"experimental_profile", "task_id", "task_ir_sha256", "attempt_id", "lease_id",
		"fence_epoch", "work_order_decision_id", "executor_sha256", "nonce", "status", "exit_code", "output_bytes",
		"output_sha256", "started_at", "ended_at", "claim_sha256"} {
		t.Run("missing/"+string(name), func(t *testing.T) {
			bad := mutated(t, c, "Claim", wire, func(m *dynamicpb.Message) { m.Clear(field(m, name)) })
			if claim, err := c.decodeClaim(principal, bad); err == nil || claim.Valid() {
				t.Fatal("missing field became a valid claim")
			}
		})
	}
	for name, change := range map[string]func(*dynamicpb.Message){
		"unknown-profile":         func(m *dynamicpb.Message) { put(m, "experimental_profile", protoreflect.ValueOfUint32(2)) },
		"unknown-status":          func(m *dynamicpb.Message) { put(m, "status", protoreflect.ValueOfEnum(99)) },
		"negative-status":         func(m *dynamicpb.Message) { put(m, "status", protoreflect.ValueOfEnum(-1)) },
		"malformed-id":            func(m *dynamicpb.Message) { put(m, "task_id", protoreflect.ValueOfString("not-an-id")) },
		"wrong-digest-size":       func(m *dynamicpb.Message) { put(m, "task_ir_sha256", protoreflect.ValueOfBytes([]byte{1})) },
		"short-nonce":             func(m *dynamicpb.Message) { put(m, "nonce", protoreflect.ValueOfBytes(make([]byte, 15))) },
		"long-nonce":              func(m *dynamicpb.Message) { put(m, "nonce", protoreflect.ValueOfBytes(make([]byte, 33))) },
		"content-digest-mismatch": func(m *dynamicpb.Message) { put(m, "output_bytes", protoreflect.ValueOfUint64(7)) },
		"failed-with-output":      func(m *dynamicpb.Message) { put(m, "status", protoreflect.ValueOfEnum(2)) },
		"timestamp-nanos": func(m *dynamicpb.Message) {
			put(get(m, "started_at").Message(), "nanos", protoreflect.ValueOfInt32(1000000000))
		},
		"timestamp-range": func(m *dynamicpb.Message) {
			put(get(m, "started_at").Message(), "seconds", protoreflect.ValueOfInt64(math.MaxInt64))
		},
		"nanosecond-storage-range": func(m *dynamicpb.Message) {
			put(get(m, "started_at").Message(), "seconds", protoreflect.ValueOfInt64(-62135596800))
		},
		"nested-unknown": func(m *dynamicpb.Message) {
			get(m, "started_at").Message().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 100, protowire.VarintType), 1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := mutated(t, c, "Claim", wire, change)
			if claim, err := c.decodeClaim(principal, bad); err == nil || claim.Valid() {
				t.Fatal("invalid data became a valid claim")
			}
		})
	}
	for name, bad := range map[string][]byte{
		"empty": nil, "malformed": {0xff}, "too-large": make([]byte, maxMessageBytes+1),
		"unknown-body-identity": append(append([]byte(nil), wire...), protowire.AppendString(protowire.AppendTag(nil, 100, protowire.BytesType), "worker-b")...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.decodeClaim(principal, bad); err == nil {
				t.Fatal("invalid wire accepted")
			}
		})
	}
	other := workerplane.Principal{WorkerIdentity: "worker-b"}
	if _, err := c.decodeClaim(other, wire); err == nil {
		t.Fatal("claim digest was not bound to the receiving principal")
	}
	if _, err := c.encodeClaim(other, original); err == nil {
		t.Fatal("encoder allowed a different Worker")
	}
	if _, err := c.decodeClaim(workerplane.Principal{}, wire); err == nil {
		t.Fatal("missing principal accepted")
	}
}

func TestReceiptPreservesAllSPIFields(t *testing.T) {
	c := testCodec(t)
	claim := makeClaim(t, config(resultclaim.StatusRunnerStuck, 0))
	for _, decision := range decisions[1:] {
		for _, phase := range phases[1:] {
			t.Run(decision+"/"+phase, func(t *testing.T) {
				ack := workerplane.ClaimAck{Decision: decision, AttemptPhase: phase, VerificationRequired: phase == "verifying"}
				wire, err := c.encodeReceipt(ack, claim)
				if err != nil {
					t.Fatal(err)
				}
				got, err := c.decodeReceipt(wire, claim)
				if err != nil || got != ack {
					t.Fatalf("receipt lost fields: %+v, %v", got, err)
				}
			})
		}
	}
}

func TestInvalidReceiptNeverBecomesAcknowledgement(t *testing.T) {
	c := testCodec(t)
	claim := makeClaim(t, config(resultclaim.StatusRunnerStuck, 0))
	wire, err := c.encodeReceipt(workerplane.ClaimAck{Decision: "new", AttemptPhase: "dispatched"}, claim)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []protoreflect.Name{"experimental_profile", "decision", "attempt_phase", "verification_required", "claim_sha256"} {
		t.Run("missing/"+string(name), func(t *testing.T) {
			bad := mutated(t, c, "Receipt", wire, func(m *dynamicpb.Message) { m.Clear(field(m, name)) })
			if _, err := c.decodeReceipt(bad, claim); err == nil {
				t.Fatal("missing receipt field accepted")
			}
		})
	}
	for name, change := range map[string]func(*dynamicpb.Message){
		"unknown-decision":  func(m *dynamicpb.Message) { put(m, "decision", protoreflect.ValueOfEnum(99)) },
		"unknown-phase":     func(m *dynamicpb.Message) { put(m, "attempt_phase", protoreflect.ValueOfEnum(-1)) },
		"inconsistent-flag": func(m *dynamicpb.Message) { put(m, "verification_required", protoreflect.ValueOfBool(true)) },
		"different-profile": func(m *dynamicpb.Message) { put(m, "experimental_profile", protoreflect.ValueOfUint32(2)) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.decodeReceipt(mutated(t, c, "Receipt", wire, change), claim); err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
	other := makeClaim(t, config(resultclaim.StatusReclaimed, 0))
	if _, err := c.decodeReceipt(wire, other); err == nil {
		t.Fatal("receipt for another claim accepted")
	}
	if _, err := c.encodeReceipt(workerplane.ClaimAck{}, claim); err == nil {
		t.Fatal("empty SPI acknowledgement encoded as success")
	}
}
