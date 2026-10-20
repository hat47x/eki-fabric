package cbor

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func mustRef(t *testing.T, kind authority.DecisionKind, id, seed string) decisionpayload.DecisionRef {
	t.Helper()
	ref, err := decisionpayload.NewDecisionRef(decisionpayload.DecisionRefConfig{
		Kind:          kind,
		DecisionID:    id,
		PayloadDigest: contract.SHA256([]byte(seed)),
	})
	if err != nil {
		t.Fatalf("NewDecisionRef() error = %v", err)
	}
	return ref
}

func validWorkOrder(t *testing.T) decisionpayload.WorkOrder {
	t.Helper()
	return mustWorkOrder(t, func(cfg *decisionpayload.WorkOrderConfig) {})
}

func mustWorkOrder(t *testing.T, mutate func(*decisionpayload.WorkOrderConfig)) decisionpayload.WorkOrder {
	t.Helper()
	issued := time.UnixMilli(1_780_000_000_000).UTC()
	cfg := decisionpayload.WorkOrderConfig{
		SchemaVersion:      decisionpayload.WorkOrderSchemaVersion,
		DecisionKind:       authority.DecisionWorkOrder,
		DecisionID:         "work-order-decision-001",
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: "control-plane/wor-1",
		IssuedAt:           issued,
		ExpiresAt:          issued.Add(time.Minute),
		PolicyRevision:     "policy-42",
		WorkOrderID:        "work-order-001",
		TaskID:             ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest:       contract.SHA256([]byte("taskir")),
		AttemptID:          ids.AttemptID("atm_00000000000000000000000000000001"),
		WorkerIdentity:     "worker-a",
		PlacementRef:       mustRef(t, authority.DecisionPlacementApproval, "placement-001", "placement"),
		LeaseRef:           mustRef(t, authority.DecisionLease, "lease-001", "lease"),
		ExecutorApproval:   mustRef(t, authority.DecisionExecutorApproval, "executor-001", "executor"),
		ExecutorDigest:     contract.SHA256([]byte("executable")),
		ReadGrants:         []decisionpayload.DecisionRef{mustRef(t, authority.DecisionArtifactReadGrant, "read-001", "read")},
		WriteGrants:        []decisionpayload.DecisionRef{mustRef(t, authority.DecisionArtifactWriteGrant, "write-001", "write")},
		Limits: decisionpayload.ResourceLimits{
			CPUMillis:          4000,
			MemoryBytes:        1 << 30,
			TemporaryDiskBytes: 1 << 30,
			WallTimeMillis:     600_000,
			OutputBytesMaximum: 1 << 20,
			NetworkBytesMax:    1 << 20,
		},
		Sandbox:            decisionpayload.SandboxPolicy{Profile: "sandbox/container/v1"},
		ExternalEffectMode: decisionpayload.WorkOrderExternalEffectNone,
		Nonce:              bytes.Repeat([]byte{0x5a}, 16),
	}
	mutate(&cfg)
	order, err := decisionpayload.NewWorkOrder(cfg)
	if err != nil {
		t.Fatalf("NewWorkOrder() error = %v", err)
	}
	return order
}

func TestEncodeWorkOrderIsDeterministicAndRoundTrips(t *testing.T) {
	codec := newTestDecoder(t)
	order := validWorkOrder(t)
	first, err := codec.EncodeWorkOrder(order)
	if err != nil {
		t.Fatalf("EncodeWorkOrder() error = %v", err)
	}
	second, err := codec.EncodeWorkOrder(order)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("EncodeWorkOrder() is not deterministic: err=%v", err)
	}
	got, err := codec.DecodeWorkOrder(first)
	if err != nil {
		t.Fatalf("DecodeWorkOrder() error = %v", err)
	}
	if !got.Equal(order) {
		t.Fatal("round trip changed the WorkOrder")
	}
}

func TestEncodeWorkOrderWithGPUAndNetworkProfileRoundTrips(t *testing.T) {
	codec := newTestDecoder(t)
	order := mustWorkOrder(t, func(cfg *decisionpayload.WorkOrderConfig) {
		cfg.Sandbox.NetworkProfile = "network/isolated/v1"
		cfg.Sandbox.GPU = &decisionpayload.GPULimit{DeviceCount: 2, MinimumVRAMBytes: 24 << 30}
	})
	payload, err := codec.EncodeWorkOrder(order)
	if err != nil {
		t.Fatalf("EncodeWorkOrder() error = %v", err)
	}
	got, err := codec.DecodeWorkOrder(payload)
	if err != nil {
		t.Fatalf("DecodeWorkOrder() error = %v", err)
	}
	if !got.Equal(order) {
		t.Fatal("round trip changed the optional sandbox fields")
	}
	if got.Sandbox().GPU == nil || got.Sandbox().GPU.DeviceCount != 2 {
		t.Fatalf("GPU limit = %+v, want 2 devices", got.Sandbox().GPU)
	}
}

func TestEncodeWorkOrderMatchesCDDLShape(t *testing.T) {
	codec := newTestDecoder(t)
	payload, err := codec.EncodeWorkOrder(validWorkOrder(t))
	if err != nil {
		t.Fatal(err)
	}
	var top map[uint64]fxcbor.RawMessage
	if err := fxcbor.Unmarshal(payload, &top); err != nil {
		t.Fatal(err)
	}
	if len(top) != 8 {
		t.Fatalf("top-level keys = %d, want 8", len(top))
	}
	var kind string
	if err := fxcbor.Unmarshal(top[2], &kind); err != nil || kind != "eki.work-order/v1" {
		t.Fatalf("key 2 = %q err=%v", kind, err)
	}
	var auth map[uint64]string
	if err := fxcbor.Unmarshal(top[4], &auth); err != nil || auth[1] != "WOR-1" {
		t.Fatalf("key 4 = %#v err=%v", auth, err)
	}
	var body map[uint64]fxcbor.RawMessage
	if err := fxcbor.Unmarshal(top[8], &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 15 {
		t.Fatalf("body keys = %d, want 15", len(body))
	}
	var readGrants []map[uint64]fxcbor.RawMessage
	if err := fxcbor.Unmarshal(body[10], &readGrants); err != nil || len(readGrants) != 1 {
		t.Fatalf("read grants = %d err=%v, want 1", len(readGrants), err)
	}
	var nonce []byte
	if err := fxcbor.Unmarshal(body[15], &nonce); err != nil || len(nonce) != 16 {
		t.Fatalf("nonce length = %d err=%v, want 16", len(nonce), err)
	}
}

func TestDecodeWorkOrderRejectsNonDeterministicOrdering(t *testing.T) {
	codec := newTestDecoder(t)
	wire := mustWorkOrderWire(t, validWorkOrder(t))
	payload := encodeTopLevelInOrder(t, codec, wire, []uint64{2, 1, 3, 4, 5, 6, 7, 8})
	if _, err := codec.DecodeWorkOrder(payload); !errors.Is(err, ErrNonDeterministicWorkOrderPayload) {
		t.Fatalf("non-deterministic payload error = %v, want ErrNonDeterministicWorkOrderPayload", err)
	}
}

func TestDecodeWorkOrderRejectsUnknownField(t *testing.T) {
	codec := newTestDecoder(t)
	wire := mustWorkOrderWire(t, validWorkOrder(t))
	payload := encodeTopLevelWithExtraKey(t, codec, wire, 9)
	if _, err := codec.DecodeWorkOrder(payload); !errors.Is(err, ErrInvalidWorkOrderPayload) {
		t.Fatalf("unknown field error = %v, want ErrInvalidWorkOrderPayload", err)
	}
}

func TestDecodeWorkOrderRejectsTooManyReadGrants(t *testing.T) {
	codec := newTestDecoder(t)
	wire := mustWorkOrderWire(t, validWorkOrder(t))
	wire.Body.ReadGrants = make([]decisionRefWire, decisionpayload.MaxWorkOrderReadGrants+1)
	for i := range wire.Body.ReadGrants {
		wire.Body.ReadGrants[i] = encodeRef(mustRef(t, authority.DecisionArtifactReadGrant, fmt.Sprintf("read-%03d", i), "read"))
	}
	payload, err := codec.encode.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.DecodeWorkOrder(payload); !errors.Is(err, ErrInvalidWorkOrderPayload) {
		t.Fatalf("too many read grants error = %v, want ErrInvalidWorkOrderPayload", err)
	}
}

func TestDecodeWorkOrderRejectsTruncatedPayload(t *testing.T) {
	codec := newTestDecoder(t)
	payload, err := codec.EncodeWorkOrder(validWorkOrder(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.DecodeWorkOrder(payload[:len(payload)/2]); !errors.Is(err, ErrInvalidWorkOrderPayload) {
		t.Fatalf("truncated payload error = %v, want ErrInvalidWorkOrderPayload", err)
	}
	if _, err := codec.DecodeWorkOrder(nil); !errors.Is(err, ErrInvalidWorkOrderPayload) {
		t.Fatalf("empty payload error = %v, want ErrInvalidWorkOrderPayload", err)
	}
}

func mustWorkOrderWire(t *testing.T, order decisionpayload.WorkOrder) workOrderWire {
	t.Helper()
	payload, err := newTestDecoder(t).EncodeWorkOrder(order)
	if err != nil {
		t.Fatal(err)
	}
	var wire workOrderWire
	if err := fxcbor.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	return wire
}

func encodeTopLevelInOrder(t *testing.T, decoder *Decoder, wire workOrderWire, order []uint64) []byte {
	t.Helper()
	values := map[uint64]any{
		1: wire.SchemaVersion,
		2: wire.DecisionKind,
		3: wire.DecisionID,
		4: wire.Authority,
		5: wire.IssuedAtMillis,
		6: wire.ExpiresAtMillis,
		7: wire.PolicyRevision,
		8: wire.Body,
	}
	out := []byte{0xa8}
	for _, key := range order {
		encodedKey, err := decoder.encode.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		encodedValue, err := decoder.encode.Marshal(values[key])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, encodedKey...)
		out = append(out, encodedValue...)
	}
	return out
}

func encodeTopLevelWithExtraKey(t *testing.T, decoder *Decoder, wire workOrderWire, extraKey uint64) []byte {
	t.Helper()
	values := map[uint64]any{
		1: wire.SchemaVersion,
		2: wire.DecisionKind,
		3: wire.DecisionID,
		4: wire.Authority,
		5: wire.IssuedAtMillis,
		6: wire.ExpiresAtMillis,
		7: wire.PolicyRevision,
		8: wire.Body,
	}
	out := []byte{0xa9}
	for key := uint64(1); key <= 8; key++ {
		encodedKey, err := decoder.encode.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		encodedValue, err := decoder.encode.Marshal(values[key])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, encodedKey...)
		out = append(out, encodedValue...)
	}
	encodedKey, err := decoder.encode.Marshal(extraKey)
	if err != nil {
		t.Fatal(err)
	}
	encodedValue, err := decoder.encode.Marshal("unexpected")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, encodedKey...)
	out = append(out, encodedValue...)
	return out
}
