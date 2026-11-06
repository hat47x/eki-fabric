package testprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// AssignmentsPath is the URL path of the assignment endpoint of the test
// profile. Like Path, it is NOT the Worker Protocol (#198). The Worker is the
// one the client certificate names; the request body names nobody.
const AssignmentsPath = "/test-profile/v1/assignments"

// MaxAssignmentsBytes bounds an assignments response a client will read. A
// signed Lease and WorkOrder are a few hundred bytes to a few KiB each.
const MaxAssignmentsBytes = 4 << 20

// maxRunningLeases bounds the running-Lease reports one request may carry. It
// matches the delivery listing's own bound: a Worker cannot hold more
// Attempts than the listing can name.
const maxRunningLeases = statestore.MaxAssignmentsPerList

type wireAssignment struct {
	AttemptID         string `json:"attempt_id"`
	LeaseID           string `json:"lease_id"`
	LeaseState        string `json:"lease_state"`
	LeaseEnvelope     []byte `json:"lease_envelope"`
	WorkOrderEnvelope []byte `json:"work_order_envelope"`
	// InputDigests are hex, the same spelling the rest of the API uses. Omitted
	// when the Task declared no inputs (ADR-0029 §5.3).
	InputDigests []string `json:"input_digests,omitempty"`
}

type wireAssignments struct {
	Assignments []wireAssignment `json:"assignments"`
}

// wireHeartbeat names one running Attempt of the polling Worker (ADR-0030
// §2.2): the Worker reports the Leases it is executing with each poll, and the
// Control Plane records the report as a liveness fact. Only the Lease ID is
// named; the Worker does not assert state, and the identifiers are hints the
// Control Plane checks against its own store.
type wireHeartbeat struct {
	LeaseID string `json:"lease_id"`
}

type wireAssignmentsRequest struct {
	// Running lists the Leases the Worker is currently executing, oldest
	// first. Nil means the Worker holds no running Lease.
	Running []wireHeartbeat `json:"running,omitempty"`
}

// EncodeAssignmentsRequest renders the request body: the running Leases of the
// polling Worker. The request carries no identity; the client certificate
// names the Worker.
func EncodeAssignmentsRequest(running []string) []byte {
	req := wireAssignmentsRequest{Running: make([]wireHeartbeat, 0, len(running))}
	for _, id := range running {
		req.Running = append(req.Running, wireHeartbeat{LeaseID: id})
	}
	raw, err := json.Marshal(req)
	if err != nil {
		// IDs are strings the caller already validated; marshal cannot fail.
		panic(err)
	}
	return raw
}

// DecodeAssignmentsRequest parses the request body. Unknown fields are
// refused, so a field that tries to name a Worker or smuggle state is an
// error instead of ignored data.
func DecodeAssignmentsRequest(raw []byte) ([]string, error) {
	var v wireAssignmentsRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("%w: assignments request: %v", ErrInvalidMessage, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: assignments request has trailing data", ErrInvalidMessage)
	}
	if len(v.Running) > maxRunningLeases {
		return nil, fmt.Errorf("%w: assignments request names %d running leases, at most %d are accepted",
			ErrInvalidMessage, len(v.Running), maxRunningLeases)
	}
	running := make([]string, 0, len(v.Running))
	seen := make(map[string]struct{}, len(v.Running))
	for _, r := range v.Running {
		if _, dup := seen[r.LeaseID]; dup {
			return nil, fmt.Errorf("%w: assignments request names Lease %s twice", ErrInvalidMessage, r.LeaseID)
		}
		seen[r.LeaseID] = struct{}{}
		running = append(running, r.LeaseID)
	}
	return running, nil
}

// EncodeAssignments renders the response body.
func EncodeAssignments(list []workerplane.Assignment) ([]byte, error) {
	out := wireAssignments{Assignments: make([]wireAssignment, 0, len(list))}
	for _, a := range list {
		if len(a.LeaseEnvelope) == 0 || len(a.WorkOrderEnvelope) == 0 {
			return nil, fmt.Errorf("%w: assignment without signed bytes", ErrInvalidMessage)
		}
		out.Assignments = append(out.Assignments, wireAssignment{
			AttemptID: a.AttemptID, LeaseID: a.LeaseID, LeaseState: a.LeaseState,
			LeaseEnvelope: a.LeaseEnvelope, WorkOrderEnvelope: a.WorkOrderEnvelope,
			InputDigests: digestHexes(a.InputDigests),
		})
	}
	return json.Marshal(out)
}

// DecodeAssignments parses a response body. It checks the shape only; the
// signed bytes are verified by the caller.
func DecodeAssignments(raw []byte) ([]workerplane.Assignment, error) {
	var in wireAssignments
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("%w: assignments: %v", ErrInvalidMessage, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: assignments have trailing data", ErrInvalidMessage)
	}
	out := make([]workerplane.Assignment, 0, len(in.Assignments))
	for _, a := range in.Assignments {
		if len(a.LeaseEnvelope) == 0 || len(a.WorkOrderEnvelope) == 0 {
			return nil, fmt.Errorf("%w: assignment without signed bytes", ErrInvalidMessage)
		}
		switch a.LeaseState {
		case workerplane.LeaseStateCurrent, workerplane.LeaseStateRevoked:
		default:
			return nil, fmt.Errorf("%w: lease_state %q", ErrInvalidMessage, a.LeaseState)
		}
		digests, err := parseDigestHexes(a.InputDigests)
		if err != nil {
			return nil, err
		}
		out = append(out, workerplane.Assignment{
			AttemptID: a.AttemptID, LeaseID: a.LeaseID, LeaseState: a.LeaseState,
			LeaseEnvelope: a.LeaseEnvelope, WorkOrderEnvelope: a.WorkOrderEnvelope,
			InputDigests: digests,
		})
	}
	return out, nil
}

// digestHexes renders digests for the wire. Nil stays nil, so a Task without inputs
// carries no field at all rather than an empty list.
func digestHexes(digests []contract.Digest) []string {
	if len(digests) == 0 {
		return nil
	}
	out := make([]string, 0, len(digests))
	for _, digest := range digests {
		out = append(out, digest.Hex())
	}
	return out
}

// parseDigestHexes reads the wire form. A value that is not a digest is an invalid
// message, not a Task without inputs.
func parseDigestHexes(values []string) ([]contract.Digest, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]contract.Digest, 0, len(values))
	for _, value := range values {
		digest, err := contract.ParseSHA256(value)
		if err != nil {
			return nil, fmt.Errorf("%w: input digest %q: %v", ErrInvalidMessage, value, err)
		}
		out = append(out, digest)
	}
	return out, nil
}
