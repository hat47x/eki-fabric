// Package testprofile is the JSON body of the process-level test profile of
// the Worker Plane. It is NOT the Worker Protocol (#198) and is not an
// enrollment or authentication decision.
//
// The Worker Protocol (spec/worker/v1/worker.proto) is not adopted for
// ResultClaim yet, and the mapping between the Core claim and that protocol
// is tracked in Issue #198. This package carries the Core claim as plain JSON
// so that the Agent process and the Control Plane process can be connected and
// tested end to end. It makes no claim of compatibility with worker.proto, and
// nothing here may be read as the wire contract.
//
// Both sides use the same encoder and decoder, so a field cannot be added on
// one side only. The decoder refuses unknown fields, trailing data and any
// claim that resultclaim.NewClaim refuses.
package testprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// Path is the URL path of the claim endpoint of the test profile.
const Path = "/test-profile/v1/claims"

// ContentType identifies the test profile's JSON bodies.
const ContentType = "application/json"

// ErrInvalidMessage means a body is not a well-formed test-profile message.
var ErrInvalidMessage = errors.New("invalid test profile message")

type wireClaim struct {
	TaskID            ids.TaskID         `json:"task_id"`
	TaskIRDigest      string             `json:"task_ir_digest"`
	AttemptID         ids.AttemptID      `json:"attempt_id"`
	WorkerIdentity    string             `json:"worker_identity"`
	LeaseID           ids.LeaseID        `json:"lease_id"`
	FenceEpoch        uint64             `json:"fence_epoch"`
	WorkOrderDecision string             `json:"work_order_decision"`
	ExecutorDigest    string             `json:"executor_digest"`
	Nonce             []byte             `json:"nonce"`
	Status            resultclaim.Status `json:"status"`
	ExitCode          int                `json:"exit_code"`
	OutputBytes       uint64             `json:"output_bytes"`
	OutputDigest      string             `json:"output_digest,omitempty"`
	StartedAt         time.Time          `json:"started_at"`
	EndedAt           time.Time          `json:"ended_at"`
}

type wireAck struct {
	Decision             string `json:"decision"`
	AttemptPhase         string `json:"attempt_phase"`
	VerificationRequired bool   `json:"verification_required"`
}

// EncodeClaim renders a claim as the test profile's request body.
func EncodeClaim(c resultclaim.Claim) ([]byte, error) {
	if !c.Valid() {
		return nil, resultclaim.ErrInvalidClaim
	}
	w := wireClaim{
		TaskID: c.TaskID(), TaskIRDigest: c.TaskIRDigest().Hex(), AttemptID: c.AttemptID(),
		WorkerIdentity: c.WorkerIdentity(), LeaseID: c.LeaseID(), FenceEpoch: c.FenceEpoch(),
		WorkOrderDecision: c.WorkOrderDecision(), ExecutorDigest: c.ExecutorDigest().Hex(),
		Nonce: c.Nonce(), Status: c.Status(), ExitCode: c.ExitCode(), OutputBytes: c.OutputBytes(),
		StartedAt: c.StartedAt(), EndedAt: c.EndedAt(),
	}
	if !c.OutputDigest().IsZero() {
		w.OutputDigest = c.OutputDigest().Hex()
	}
	return json.Marshal(w)
}

// DecodeClaim parses a request body into a claim.
func DecodeClaim(raw []byte) (resultclaim.Claim, error) {
	var w wireClaim
	if err := decodeStrict(raw, &w); err != nil {
		return resultclaim.Claim{}, err
	}
	taskIR, err := contract.ParseSHA256(w.TaskIRDigest)
	if err != nil {
		return resultclaim.Claim{}, fmt.Errorf("%w: task_ir_digest", ErrInvalidMessage)
	}
	executor, err := contract.ParseSHA256(w.ExecutorDigest)
	if err != nil {
		return resultclaim.Claim{}, fmt.Errorf("%w: executor_digest", ErrInvalidMessage)
	}
	var output contract.Digest
	if w.OutputDigest != "" {
		if output, err = contract.ParseSHA256(w.OutputDigest); err != nil {
			return resultclaim.Claim{}, fmt.Errorf("%w: output_digest", ErrInvalidMessage)
		}
	}
	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: w.TaskID, TaskIRDigest: taskIR, AttemptID: w.AttemptID,
		WorkerIdentity: w.WorkerIdentity, LeaseID: w.LeaseID, FenceEpoch: w.FenceEpoch,
		WorkOrderDecision: w.WorkOrderDecision, ExecutorDigest: executor, Nonce: w.Nonce,
		Status: w.Status, ExitCode: w.ExitCode, OutputBytes: w.OutputBytes, OutputDigest: output,
		StartedAt: w.StartedAt, EndedAt: w.EndedAt,
	})
	if err != nil {
		return resultclaim.Claim{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	return claim, nil
}

// EncodeAck renders an acknowledgement as the response body.
func EncodeAck(a workerplane.ClaimAck) ([]byte, error) {
	return json.Marshal(wireAck{
		Decision: a.Decision, AttemptPhase: a.AttemptPhase, VerificationRequired: a.VerificationRequired,
	})
}

// DecodeAck parses a response body. Whether the acknowledgement is meaningful
// is checked by claimclient, not here.
func DecodeAck(raw []byte) (workerplane.ClaimAck, error) {
	var w wireAck
	if err := decodeStrict(raw, &w); err != nil {
		return workerplane.ClaimAck{}, err
	}
	return workerplane.ClaimAck{
		Decision: w.Decision, AttemptPhase: w.AttemptPhase, VerificationRequired: w.VerificationRequired,
	}, nil
}

func decodeStrict(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing data", ErrInvalidMessage)
	}
	return nil
}
