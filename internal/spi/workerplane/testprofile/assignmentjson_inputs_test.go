package testprofile

import (
	"encoding/json"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// The digests travel with the assignment, in declaration order, so the Worker can
// fetch the inputs itself (ADR-0029 §5.3).
func TestInputDigestsRoundTripThroughTheWire(t *testing.T) {
	first := contract.SHA256([]byte("input-a"))
	second := contract.SHA256([]byte("input-b"))
	raw, err := EncodeAssignments([]workerplane.Assignment{{
		AttemptID: "atm_1", LeaseID: "lse_1", LeaseState: workerplane.LeaseStateCurrent,
		LeaseEnvelope: []byte("lease"), WorkOrderEnvelope: []byte("order"),
		InputDigests: []contract.Digest{first, second},
	}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAssignments(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded[0].InputDigests
	if len(got) != 2 || got[0].Hex() != first.Hex() || got[1].Hex() != second.Hex() {
		t.Fatalf("InputDigests = %v, want the declared order", got)
	}
}

// A Task without inputs carries no field, and a message that names a value that is
// not a digest is refused rather than read as "no inputs".
func TestInputDigestsAreOmittedAndValidated(t *testing.T) {
	raw, err := EncodeAssignments([]workerplane.Assignment{{
		AttemptID: "atm_1", LeaseID: "lse_1", LeaseState: workerplane.LeaseStateCurrent,
		LeaseEnvelope: []byte("lease"), WorkOrderEnvelope: []byte("order"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Assignments []map[string]json.RawMessage `json:"assignments"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire.Assignments[0]["input_digests"]; ok {
		t.Fatal("a Task without inputs carried an input_digests field")
	}

	bad := []byte(`{"assignments":[{"attempt_id":"a","lease_id":"l","lease_state":"current","lease_envelope":"bGVhc2U=","work_order_envelope":"b3JkZXI=","input_digests":["not-a-digest"]}]}`)
	if _, err := DecodeAssignments(bad); err == nil {
		t.Fatal("DecodeAssignments() accepted a value that is not a digest")
	}
}
