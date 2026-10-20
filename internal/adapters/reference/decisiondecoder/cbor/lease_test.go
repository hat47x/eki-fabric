package cbor

import (
	"bytes"
	"errors"
	"testing"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func validLease(t *testing.T) decisionpayload.Lease {
	t.Helper()
	issued := time.UnixMilli(1_780_000_000_000).UTC()
	lease, err := decisionpayload.NewLease(decisionpayload.LeaseConfig{
		SchemaVersion:      decisionpayload.LeaseSchemaVersion,
		DecisionKind:       authority.DecisionLease,
		DecisionID:         "lease-decision-001",
		AuthorityRole:      authority.RoleLease,
		AuthorityPrincipal: "control-plane/lse-1",
		IssuedAt:           issued,
		ExpiresAt:          issued.Add(time.Minute),
		PolicyRevision:     "policy-42",
		TaskID:             ids.TaskID("tsk_00000000000000000000000000000001"),
		TaskIRDigest:       contract.SHA256([]byte("taskir")),
		AttemptID:          ids.AttemptID("atm_00000000000000000000000000000001"),
		WorkerIdentity:     "worker-a",
		LeaseID:            ids.LeaseID("les_00000000000000000000000000000001"),
		FenceEpoch:         7,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestEncodeLeaseIsDeterministicAndRoundTrips(t *testing.T) {
	codec := newTestDecoder(t)
	lease := validLease(t)
	first, err := codec.EncodeLease(lease)
	if err != nil {
		t.Fatalf("EncodeLease() error = %v", err)
	}
	second, err := codec.EncodeLease(lease)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("EncodeLease() is not deterministic: err=%v", err)
	}
	got, err := codec.DecodeLease(first)
	if err != nil {
		t.Fatalf("DecodeLease() error = %v", err)
	}
	if !got.Equal(lease) {
		t.Fatalf("round trip changed Lease: %#v", got)
	}
}

// TestEncodeLeaseMatchesCDDLShape checks the integer-keyed map shape of
// `lease-v1` in spec/decisions/v1/decisions.cddl.
func TestEncodeLeaseMatchesCDDLShape(t *testing.T) {
	codec := newTestDecoder(t)
	lease := validLease(t)
	payload, err := codec.EncodeLease(lease)
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
	if err := fxcbor.Unmarshal(top[2], &kind); err != nil || kind != "eki.lease/v1" {
		t.Fatalf("key 2 = %q err=%v", kind, err)
	}
	var auth map[uint64]string
	if err := fxcbor.Unmarshal(top[4], &auth); err != nil || auth[1] != "LSE-1" || auth[2] != "control-plane/lse-1" {
		t.Fatalf("key 4 = %#v err=%v", auth, err)
	}
	var expires uint64
	if err := fxcbor.Unmarshal(top[6], &expires); err != nil || expires != uint64(lease.ExpiresAt().UnixMilli()) {
		t.Fatalf("key 6 = %d err=%v", expires, err)
	}
	var body map[uint64]fxcbor.RawMessage
	if err := fxcbor.Unmarshal(top[8], &body); err != nil || len(body) != 7 {
		t.Fatalf("body keys = %d err=%v, want 7", len(body), err)
	}
	var leaseID string
	if err := fxcbor.Unmarshal(body[5], &leaseID); err != nil || leaseID != lease.LeaseID().String() {
		t.Fatalf("body 5 = %q err=%v", leaseID, err)
	}
	var fence uint64
	if err := fxcbor.Unmarshal(body[6], &fence); err != nil || fence != 7 {
		t.Fatalf("body 6 = %d err=%v", fence, err)
	}
}

func TestDecodeLeaseRejectsHostileInput(t *testing.T) {
	codec := newTestDecoder(t)
	payload, err := codec.EncodeLease(validLease(t))
	if err != nil {
		t.Fatal(err)
	}
	var wire leaseWire
	if err := fxcbor.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}

	t.Run("placement-payload", func(t *testing.T) {
		if _, err := codec.DecodeLease(mustEncode(t, codec, validPlacementWire())); !errors.Is(err, ErrInvalidLeasePayload) {
			t.Fatalf("Placement payload decoded as Lease: %v", err)
		}
	})
	t.Run("unknown-field", func(t *testing.T) {
		mutated := append([]byte{0xa9}, payload[1:]...)
		mutated = append(mutated, 0x09, 0x00)
		if _, err := codec.DecodeLease(mutated); !errors.Is(err, ErrInvalidLeasePayload) {
			t.Fatalf("unknown field error = %v", err)
		}
	})
	t.Run("indefinite-length", func(t *testing.T) {
		mutated := append([]byte{0xbf}, payload[1:]...)
		mutated = append(mutated, 0xff)
		if _, err := codec.DecodeLease(mutated); !errors.Is(err, ErrInvalidLeasePayload) {
			t.Fatalf("indefinite-length error = %v", err)
		}
	})
	t.Run("non-shortest-integer", func(t *testing.T) {
		mutated := mustEncode(t, codec, map[uint64]any{
			1: fxcbor.RawMessage{0x18, 0x01},
			2: wire.DecisionKind, 3: wire.DecisionID, 4: wire.Authority,
			5: wire.IssuedAtMillis, 6: wire.ExpiresAtMillis, 7: wire.PolicyRevision, 8: wire.Body,
		})
		if _, err := codec.DecodeLease(mutated); err == nil {
			t.Fatal("non-shortest integer encoding was accepted")
		}
	})
	t.Run("wrong-role", func(t *testing.T) {
		mutated := wire
		mutated.Authority.Role = string(authority.RolePlacementAuthorization)
		if _, err := codec.DecodeLease(mustEncode(t, codec, mutated)); !errors.Is(err, ErrInvalidLeasePayload) {
			t.Fatalf("PLC-1 role error = %v", err)
		}
	})
	t.Run("zero-fence", func(t *testing.T) {
		mutated := wire
		mutated.Body.FenceEpoch = 0
		if _, err := codec.DecodeLease(mustEncode(t, codec, mutated)); !errors.Is(err, ErrInvalidLeasePayload) {
			t.Fatalf("zero FenceEpoch error = %v", err)
		}
	})
}
