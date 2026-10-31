package cbor

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// encodeTestApproval builds the closed value that binds the same fields as
// validPlacementWire, so the encoder can be compared byte for byte against
// the decoder fixture.
func encodeTestApproval(t *testing.T, evidence []decisionpayload.EvidenceRefConfig) decisionpayload.PlacementApproval {
	t.Helper()
	taskID, err := ids.ParseTaskID("tsk_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := ids.ParseAttemptID("atm_00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	approval, err := decisionpayload.NewPlacementApproval(decisionpayload.PlacementApprovalConfig{
		SchemaVersion:      decisionpayload.PlacementApprovalSchemaVersion,
		DecisionKind:       authority.DecisionPlacementApproval,
		DecisionID:         "plc-decision-001",
		AuthorityRole:      authority.RolePlacementAuthorization,
		AuthorityPrincipal: "control-plane/plc-1",
		IssuedAt:           time.UnixMilli(1_780_000_000_000).UTC(),
		ExpiresAt:          time.UnixMilli(1_780_000_120_000).UTC(),
		PolicyRevision:     "policy-42",
		TaskID:             taskID,
		TaskIRDigest:       mustTestDigest(t, bytes.Repeat([]byte{0x22}, 32)),
		AttemptID:          attemptID,
		WorkerIdentity:     "worker-a",
		Evidence:           evidence,
	})
	if err != nil {
		t.Fatalf("NewPlacementApproval() error = %v", err)
	}
	return approval
}

func mustTestDigest(t *testing.T, raw []byte) contract.Digest {
	t.Helper()
	digest, err := contract.ParseSHA256(hex.EncodeToString(raw))
	if err != nil {
		t.Fatalf("ParseSHA256() error = %v", err)
	}
	return digest
}

func TestEncodePlacementApprovalMatchesTheDecoderFixtureByteForByte(t *testing.T) {
	decoder := newTestDecoder(t)
	approval := encodeTestApproval(t, []decisionpayload.EvidenceRefConfig{{
		Kind:     "assurance",
		Identity: "worker-a/assurance",
		Digest:   mustTestDigest(t, bytes.Repeat([]byte{0x33}, 32)),
	}})
	payload, err := decoder.EncodePlacementApproval(approval)
	if err != nil {
		t.Fatalf("EncodePlacementApproval() error = %v", err)
	}
	if want := mustEncode(t, decoder, validPlacementWire()); !bytes.Equal(payload, want) {
		t.Fatalf("EncodePlacementApproval() = %x, want the decoder fixture bytes %x", payload, want)
	}
}

func TestEncodePlacementApprovalRoundTripsThroughTheStrictDecoder(t *testing.T) {
	decoder := newTestDecoder(t)
	// A producer builds the value before the payload bytes exist, so the
	// digest is unassigned and the codec layer derives it.
	approval := encodeTestApproval(t, []decisionpayload.EvidenceRefConfig{{
		Kind:     "assurance",
		Identity: "worker-a/assurance",
		Digest:   mustTestDigest(t, bytes.Repeat([]byte{0x33}, 32)),
	}})
	payload, err := decoder.EncodePlacementApproval(approval)
	if err != nil {
		t.Fatalf("EncodePlacementApproval() error = %v", err)
	}
	decoded, err := decoder.DecodePlacementApproval(payload)
	if err != nil {
		t.Fatalf("DecodePlacementApproval() error = %v", err)
	}
	if !decoded.Equal(approval) {
		t.Fatalf("decoded Approval = %#v, want the encoded value", decoded)
	}
	if decoded.PayloadDigest().String() != contract.SHA256(payload).String() {
		t.Fatalf("payload digest = %s, want %s", decoded.PayloadDigest(), contract.SHA256(payload))
	}
	again, err := decoder.EncodePlacementApproval(approval)
	if err != nil {
		t.Fatalf("EncodePlacementApproval() error = %v", err)
	}
	if !bytes.Equal(payload, again) {
		t.Fatalf("EncodePlacementApproval() is not deterministic: %x vs %x", payload, again)
	}
}

func TestEncodePlacementApprovalOmitsAbsentEvidence(t *testing.T) {
	decoder := newTestDecoder(t)
	approval := encodeTestApproval(t, nil)
	payload, err := decoder.EncodePlacementApproval(approval)
	if err != nil {
		t.Fatalf("EncodePlacementApproval() error = %v", err)
	}
	decoded, err := decoder.DecodePlacementApproval(payload)
	if err != nil {
		t.Fatalf("DecodePlacementApproval() error = %v", err)
	}
	if len(decoded.Evidence()) != 0 {
		t.Fatalf("Evidence() = %#v, want absent", decoded.Evidence())
	}
}

func TestEncodePlacementApprovalRejectsAnUninitializedDecoder(t *testing.T) {
	var nilDecoder *Decoder
	if _, err := nilDecoder.EncodePlacementApproval(encodeTestApproval(t, nil)); err == nil {
		t.Fatal("EncodePlacementApproval() on nil decoder error = nil, want error")
	}
	unwired := &Decoder{}
	if _, err := unwired.EncodePlacementApproval(encodeTestApproval(t, nil)); err == nil {
		t.Fatal("EncodePlacementApproval() on unwired decoder error = nil, want error")
	}
}
