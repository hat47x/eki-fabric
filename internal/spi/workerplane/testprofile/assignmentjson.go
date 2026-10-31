package testprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// AssignmentsPath is the URL path of the assignment endpoint of the test
// profile. Like Path, it is NOT the Worker Protocol (#198). The Worker is the
// one the client certificate names; the request body names nobody.
const AssignmentsPath = "/test-profile/v1/assignments"

// MaxAssignmentsBytes bounds an assignments response a client will read. A
// signed Lease and WorkOrder are a few hundred bytes to a few KiB each.
const MaxAssignmentsBytes = 4 << 20

type wireAssignment struct {
	AttemptID         string `json:"attempt_id"`
	LeaseID           string `json:"lease_id"`
	LeaseState        string `json:"lease_state"`
	LeaseEnvelope     []byte `json:"lease_envelope"`
	WorkOrderEnvelope []byte `json:"work_order_envelope"`
}

type wireAssignments struct {
	Assignments []wireAssignment `json:"assignments"`
}

// EncodeAssignmentsRequest renders the request body: an empty object. The
// request carries no identity and no filter.
func EncodeAssignmentsRequest() []byte { return []byte("{}") }

// DecodeAssignmentsRequest accepts only the empty object, so a field that
// tries to name a Worker is refused instead of ignored.
func DecodeAssignmentsRequest(raw []byte) error {
	var v struct{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("%w: assignments request: %v", ErrInvalidMessage, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%w: assignments request has trailing data", ErrInvalidMessage)
	}
	return nil
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
		out = append(out, workerplane.Assignment{
			AttemptID: a.AttemptID, LeaseID: a.LeaseID, LeaseState: a.LeaseState,
			LeaseEnvelope: a.LeaseEnvelope, WorkOrderEnvelope: a.WorkOrderEnvelope,
		})
	}
	return out, nil
}
