package cbor

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestDecodePlacementApprovalAcceptsExactDeterministicP2Payload(t *testing.T) {
	decoder := newTestDecoder(t)
	wire := validPlacementWire()
	payload := mustEncode(t, decoder, wire)

	got, err := decoder.DecodePlacementApproval(payload)
	if err != nil {
		t.Fatalf("DecodePlacementApproval() error = %v", err)
	}
	if !got.Valid() ||
		got.SchemaVersion() != 1 ||
		got.DecisionKind() != authority.DecisionPlacementApproval ||
		got.AuthorityRole() != authority.RolePlacementAuthorization ||
		got.DecisionID() != wire.DecisionID ||
		got.AuthorityPrincipal() != wire.Authority.Principal ||
		got.TaskID().String() != wire.Body.TaskID ||
		got.AttemptID().String() != wire.Body.AttemptID ||
		got.WorkerIdentity() != wire.Body.WorkerIdentity ||
		got.PolicyRevision() != wire.PolicyRevision {
		t.Fatalf("decoded Placement Approval = %#v", got)
	}
	if got.PayloadDigest().String() != contract.SHA256(payload).String() {
		t.Fatalf("payload digest = %s, want %s", got.PayloadDigest(), contract.SHA256(payload))
	}
	if len(got.Evidence()) != 1 {
		t.Fatalf("evidence count = %d, want 1", len(got.Evidence()))
	}
}

func TestDecodePlacementApprovalPreservesPresentEmptyEvidence(t *testing.T) {
	decoder := newTestDecoder(t)
	wire := validPlacementWire()
	empty := []evidenceWire{}
	wire.Body.Evidence = &empty
	payload := mustEncode(t, decoder, wire)

	got, err := decoder.DecodePlacementApproval(payload)
	if err != nil {
		t.Fatalf("DecodePlacementApproval() error = %v", err)
	}
	if len(got.Evidence()) != 0 {
		t.Fatalf("Evidence() = %#v, want empty", got.Evidence())
	}
}

func TestDecodePlacementApprovalRejectsClosedSchemaAndSemanticMismatches(t *testing.T) {
	decoder := newTestDecoder(t)
	valid := validPlacementWire()
	tests := []struct {
		name   string
		mutate func(*placementApprovalWire)
	}{
		{name: "schema", mutate: func(w *placementApprovalWire) { w.SchemaVersion = 2 }},
		{name: "kind", mutate: func(w *placementApprovalWire) { w.DecisionKind = string(authority.DecisionLease) }},
		{name: "role", mutate: func(w *placementApprovalWire) { w.Authority.Role = string(authority.RoleLease) }},
		{name: "empty-principal", mutate: func(w *placementApprovalWire) { w.Authority.Principal = "" }},
		{name: "reversed-window", mutate: func(w *placementApprovalWire) { w.ExpiresAtMillis = w.IssuedAtMillis }},
		{name: "task", mutate: func(w *placementApprovalWire) { w.Body.TaskID = "tsk_bad" }},
		{name: "attempt", mutate: func(w *placementApprovalWire) { w.Body.AttemptID = "atm_bad" }},
		{name: "worker", mutate: func(w *placementApprovalWire) { w.Body.WorkerIdentity = "" }},
		{name: "digest-algorithm", mutate: func(w *placementApprovalWire) { w.Body.TaskIRDigest.Algorithm = 2 }},
		{name: "digest-size", mutate: func(w *placementApprovalWire) { w.Body.TaskIRDigest.Value = make([]byte, 31) }},
		{name: "timestamp-overflow", mutate: func(w *placementApprovalWire) { w.IssuedAtMillis = math.MaxUint64 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := valid
			tt.mutate(&wire)
			payload := mustEncode(t, decoder, wire)
			got, err := decoder.DecodePlacementApproval(payload)
			if !errors.Is(err, ErrInvalidPlacementApprovalPayload) {
				t.Fatalf("DecodePlacementApproval() error = %v, want ErrInvalidPlacementApprovalPayload", err)
			}
			if got.Valid() {
				t.Fatalf("invalid wire returned valid PlacementApproval: %#v", got)
			}
		})
	}
}

func TestDecodePlacementApprovalRejectsUnknownField(t *testing.T) {
	decoder := newTestDecoder(t)
	payload := mustEncodeTopLevelInOrder(t, decoder, validPlacementWire(), []uint64{1, 2, 3, 4, 5, 6, 7, 8})
	payload[0] = 0xa9
	payload = append(payload, 0x09, 0x00)

	if _, err := decoder.DecodePlacementApproval(payload); !errors.Is(err, ErrInvalidPlacementApprovalPayload) {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestDecodePlacementApprovalRejectsDuplicateMapKey(t *testing.T) {
	decoder := newTestDecoder(t)
	payload := mustEncode(t, decoder, validPlacementWire())
	payload[0] = 0xa9
	payload = append(payload, 0x01, 0x01)

	if _, err := decoder.DecodePlacementApproval(payload); !errors.Is(err, ErrInvalidPlacementApprovalPayload) {
		t.Fatalf("duplicate map key error = %v", err)
	}
}

func TestDecodePlacementApprovalRejectsIndefiniteLength(t *testing.T) {
	decoder := newTestDecoder(t)
	payload := mustEncode(t, decoder, validPlacementWire())
	payload[0] = 0xbf
	payload = append(payload, 0xff)

	if _, err := decoder.DecodePlacementApproval(payload); !errors.Is(err, ErrInvalidPlacementApprovalPayload) {
		t.Fatalf("indefinite-length error = %v", err)
	}
}

func TestDecodePlacementApprovalRejectsNonDeterministicMapOrdering(t *testing.T) {
	decoder := newTestDecoder(t)
	payload := mustEncodeTopLevelInOrder(t, decoder, validPlacementWire(), []uint64{2, 1, 3, 4, 5, 6, 7, 8})

	if _, err := decoder.DecodePlacementApproval(payload); !errors.Is(err, ErrNonDeterministicPayload) {
		t.Fatalf("non-deterministic payload error = %v, want ErrNonDeterministicPayload", err)
	}
}

func TestDecodePlacementApprovalRejectsEvidenceAboveCDDLBound(t *testing.T) {
	decoder := newTestDecoder(t)
	wire := validPlacementWire()
	evidence := make([]evidenceWire, 17)
	for i := range evidence {
		evidence[i] = evidenceWire{
			Kind:     "assurance",
			Identity: "worker-a/assurance",
			Digest:   digestWire{Algorithm: 1, Value: bytes.Repeat([]byte{byte(i + 1)}, 32)},
		}
	}
	wire.Body.Evidence = &evidence
	payload := mustEncode(t, decoder, wire)

	if _, err := decoder.DecodePlacementApproval(payload); !errors.Is(err, ErrInvalidPlacementApprovalPayload) {
		t.Fatalf("oversized evidence error = %v", err)
	}
}

func newTestDecoder(t *testing.T) *Decoder {
	t.Helper()
	decoder, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return decoder
}

func validPlacementWire() placementApprovalWire {
	evidence := []evidenceWire{{
		Kind:     "assurance",
		Identity: "worker-a/assurance",
		Digest:   digestWire{Algorithm: 1, Value: bytes.Repeat([]byte{0x33}, 32)},
	}}
	return placementApprovalWire{
		SchemaVersion:   1,
		DecisionKind:    string(authority.DecisionPlacementApproval),
		DecisionID:      "plc-decision-001",
		Authority:       authorityWire{Role: string(authority.RolePlacementAuthorization), Principal: "control-plane/plc-1"},
		IssuedAtMillis:  1_780_000_000_000,
		ExpiresAtMillis: 1_780_000_120_000,
		PolicyRevision:  "policy-42",
		Body: placementBodyWire{
			TaskID:         "tsk_00000000000000000000000000000001",
			TaskIRDigest:   digestWire{Algorithm: 1, Value: bytes.Repeat([]byte{0x22}, 32)},
			AttemptID:      "atm_00000000000000000000000000000001",
			WorkerIdentity: "worker-a",
			Evidence:       &evidence,
		},
	}
}

func mustEncode(t *testing.T, decoder *Decoder, value any) []byte {
	t.Helper()
	payload, err := decoder.encode.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustEncodeTopLevelInOrder(t *testing.T, decoder *Decoder, wire placementApprovalWire, order []uint64) []byte {
	t.Helper()
	if len(order) != 8 {
		t.Fatalf("top-level order length = %d, want 8", len(order))
	}
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
