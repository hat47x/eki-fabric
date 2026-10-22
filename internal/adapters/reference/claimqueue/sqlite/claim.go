package sqlite

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
)

// This is private persistence data, not a Worker Plane wire format. Keep uint64
// values in typed JSON fields so SQLite's signed integers cannot truncate them.
type storedClaim struct {
	TaskID            ids.TaskID
	TaskIRDigest      string
	AttemptID         ids.AttemptID
	WorkerIdentity    string
	LeaseID           ids.LeaseID
	FenceEpoch        uint64
	WorkOrderDecision string
	ExecutorDigest    string
	Nonce             []byte
	Status            resultclaim.Status
	ExitCode          int
	OutputBytes       uint64
	// OutputDigest is empty for every status except completed. A queued entry
	// written before the field existed decodes with it empty, and a completed
	// entry without it is refused by resultclaim.NewClaim.
	OutputDigest string `json:"OutputDigest,omitempty"`
	StartedAt    time.Time
	EndedAt      time.Time
}

func encodeClaim(c resultclaim.Claim) ([]byte, error) {
	var outputDigest string
	if !c.OutputDigest().IsZero() {
		outputDigest = c.OutputDigest().Hex()
	}
	return json.Marshal(storedClaim{
		OutputDigest: outputDigest,
		TaskID:       c.TaskID(), TaskIRDigest: c.TaskIRDigest().Hex(), AttemptID: c.AttemptID(),
		WorkerIdentity: c.WorkerIdentity(), LeaseID: c.LeaseID(), FenceEpoch: c.FenceEpoch(),
		WorkOrderDecision: c.WorkOrderDecision(), ExecutorDigest: c.ExecutorDigest().Hex(),
		Nonce: c.Nonce(), Status: c.Status(), ExitCode: c.ExitCode(), OutputBytes: c.OutputBytes(),
		StartedAt: c.StartedAt(), EndedAt: c.EndedAt(),
	})
}

func decodeClaim(raw []byte) (resultclaim.Claim, error) {
	var value storedClaim
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return resultclaim.Claim{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return resultclaim.Claim{}, ErrInvalidEntry
	}
	taskIR, err := contract.ParseSHA256(value.TaskIRDigest)
	if err != nil {
		return resultclaim.Claim{}, err
	}
	executor, err := contract.ParseSHA256(value.ExecutorDigest)
	if err != nil {
		return resultclaim.Claim{}, err
	}
	var outputDigest contract.Digest
	if value.OutputDigest != "" {
		outputDigest, err = contract.ParseSHA256(value.OutputDigest)
		if err != nil {
			return resultclaim.Claim{}, err
		}
	}
	return resultclaim.NewClaim(resultclaim.ClaimConfig{
		OutputDigest: outputDigest,
		TaskID:       value.TaskID, TaskIRDigest: taskIR, AttemptID: value.AttemptID,
		WorkerIdentity: value.WorkerIdentity, LeaseID: value.LeaseID, FenceEpoch: value.FenceEpoch,
		WorkOrderDecision: value.WorkOrderDecision, ExecutorDigest: executor,
		Nonce: value.Nonce, Status: value.Status, ExitCode: value.ExitCode, OutputBytes: value.OutputBytes,
		StartedAt: value.StartedAt, EndedAt: value.EndedAt,
	})
}
