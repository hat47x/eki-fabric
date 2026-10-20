// Package resultclaim defines what a Worker reports when an Attempt ends, and
// how the Control Plane treats the report.
//
// A claim is not an accepted result (RQ-019). It is evidence that the Worker
// stopped executing and what it observed. The Control Plane records it, closes
// or leaves the Attempt according to the claim and the current state, and sends
// a completed outcome to Result Verification. A claim never revives a Lease and
// never becomes an AcceptedResult by itself.
package resultclaim

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var (
	ErrInvalidClaim = errors.New("invalid result claim")
	ErrConflict     = errors.New("a different result claim was already recorded for this attempt")
)

const (
	MinNonceBytes = 16
	MaxNonceBytes = 32
)

// Status is the observed end of one Attempt. The names match the Agent's
// outcome statuses so that the transport adapter can map them without guessing.
type Status string

const (
	StatusCompleted        Status = "completed"
	StatusFailed           Status = "failed"
	StatusReclaimed        Status = "reclaimed"
	StatusDeadlineExceeded Status = "deadline_exceeded"
	StatusCancelled        Status = "cancelled"
	StatusOutputExceeded   Status = "output_exceeded"
	StatusRunnerStuck      Status = "runner_stuck"
)

func (s Status) valid() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusReclaimed, StatusDeadlineExceeded,
		StatusCancelled, StatusOutputExceeded, StatusRunnerStuck:
		return true
	}
	return false
}

// ClaimConfig is the wire-independent content of one ResultClaim.
type ClaimConfig struct {
	TaskID            ids.TaskID
	TaskIRDigest      contract.Digest
	AttemptID         ids.AttemptID
	WorkerIdentity    string
	LeaseID           ids.LeaseID
	FenceEpoch        uint64
	WorkOrderDecision string
	ExecutorDigest    contract.Digest
	Nonce             []byte
	Status            Status
	ExitCode          int
	OutputBytes       uint64
	StartedAt         time.Time
	EndedAt           time.Time
}

type Claim struct {
	taskID            ids.TaskID
	taskIRDigest      contract.Digest
	attemptID         ids.AttemptID
	workerIdentity    string
	leaseID           ids.LeaseID
	fenceEpoch        uint64
	workOrderDecision string
	executorDigest    contract.Digest
	nonce             []byte
	status            Status
	exitCode          int
	outputBytes       uint64
	startedAt         time.Time
	endedAt           time.Time
}

func NewClaim(cfg ClaimConfig) (Claim, error) {
	switch {
	case cfg.TaskID == "":
		return Claim{}, fmt.Errorf("%w: TaskID is required", ErrInvalidClaim)
	case cfg.TaskIRDigest.IsZero():
		return Claim{}, fmt.Errorf("%w: TaskIR digest is required", ErrInvalidClaim)
	case cfg.AttemptID == "":
		return Claim{}, fmt.Errorf("%w: AttemptID is required", ErrInvalidClaim)
	case !validText(cfg.WorkerIdentity, 256):
		return Claim{}, fmt.Errorf("%w: worker identity is required", ErrInvalidClaim)
	case cfg.LeaseID == "":
		return Claim{}, fmt.Errorf("%w: LeaseID is required", ErrInvalidClaim)
	case cfg.FenceEpoch == 0:
		return Claim{}, fmt.Errorf("%w: fence epoch must be positive", ErrInvalidClaim)
	case !validText(cfg.WorkOrderDecision, 128):
		return Claim{}, fmt.Errorf("%w: WorkOrder decision is required", ErrInvalidClaim)
	case cfg.ExecutorDigest.IsZero():
		return Claim{}, fmt.Errorf("%w: executor digest is required", ErrInvalidClaim)
	case len(cfg.Nonce) < MinNonceBytes || len(cfg.Nonce) > MaxNonceBytes:
		return Claim{}, fmt.Errorf("%w: nonce must contain %d..%d bytes", ErrInvalidClaim, MinNonceBytes, MaxNonceBytes)
	case !cfg.Status.valid():
		return Claim{}, fmt.Errorf("%w: unknown status %q", ErrInvalidClaim, cfg.Status)
	case cfg.StartedAt.IsZero() || cfg.EndedAt.IsZero() || cfg.EndedAt.Before(cfg.StartedAt):
		return Claim{}, fmt.Errorf("%w: started/ended window is invalid", ErrInvalidClaim)
	case cfg.Status == StatusCompleted && cfg.ExitCode != 0:
		return Claim{}, fmt.Errorf("%w: a completed claim needs exit code 0", ErrInvalidClaim)
	}
	return Claim{
		taskID: cfg.TaskID, taskIRDigest: cfg.TaskIRDigest, attemptID: cfg.AttemptID,
		workerIdentity: cfg.WorkerIdentity, leaseID: cfg.LeaseID, fenceEpoch: cfg.FenceEpoch,
		workOrderDecision: cfg.WorkOrderDecision, executorDigest: cfg.ExecutorDigest,
		nonce: append([]byte(nil), cfg.Nonce...), status: cfg.Status, exitCode: cfg.ExitCode,
		outputBytes: cfg.OutputBytes, startedAt: cfg.StartedAt.UTC(), endedAt: cfg.EndedAt.UTC(),
	}, nil
}

func validText(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes && utf8.ValidString(value)
}

func (c Claim) TaskID() ids.TaskID              { return c.taskID }
func (c Claim) TaskIRDigest() contract.Digest   { return c.taskIRDigest }
func (c Claim) AttemptID() ids.AttemptID        { return c.attemptID }
func (c Claim) WorkerIdentity() string          { return c.workerIdentity }
func (c Claim) LeaseID() ids.LeaseID            { return c.leaseID }
func (c Claim) FenceEpoch() uint64              { return c.fenceEpoch }
func (c Claim) WorkOrderDecision() string       { return c.workOrderDecision }
func (c Claim) ExecutorDigest() contract.Digest { return c.executorDigest }
func (c Claim) Nonce() []byte                   { return append([]byte(nil), c.nonce...) }
func (c Claim) Status() Status                  { return c.status }
func (c Claim) ExitCode() int                   { return c.exitCode }
func (c Claim) OutputBytes() uint64             { return c.outputBytes }
func (c Claim) StartedAt() time.Time            { return c.startedAt }
func (c Claim) EndedAt() time.Time              { return c.endedAt }

func (c Claim) Valid() bool { return c.taskID != "" && c.attemptID != "" && c.status.valid() }

// Equal reports whether two claims carry the same observation.
func (c Claim) Equal(other Claim) bool {
	if c.taskID != other.taskID || c.taskIRDigest != other.taskIRDigest ||
		c.attemptID != other.attemptID || c.workerIdentity != other.workerIdentity ||
		c.leaseID != other.leaseID || c.fenceEpoch != other.fenceEpoch ||
		c.workOrderDecision != other.workOrderDecision || c.executorDigest != other.executorDigest ||
		!bytesEqual(c.nonce, other.nonce) || c.status != other.status ||
		c.exitCode != other.exitCode || c.outputBytes != other.outputBytes ||
		!c.startedAt.Equal(other.startedAt) || !c.endedAt.Equal(other.endedAt) {
		return false
	}
	return true
}

// Digest is the canonical identity of the claim. Two claims with the same digest
// are the same observation, so a repeated delivery can be collapsed.
func (c Claim) Digest() contract.Digest {
	h := sha256.New()
	h.Write([]byte("eki.result-claim/v1"))
	for _, field := range []string{
		c.taskID.String(), c.taskIRDigest.String(), c.attemptID.String(),
		c.workerIdentity, c.leaseID.String(), c.workOrderDecision, c.executorDigest.String(),
		string(c.status),
	} {
		size := make([]byte, 8)
		binary.BigEndian.PutUint64(size, uint64(len(field)))
		h.Write(size)
		h.Write([]byte(field))
	}
	buf := make([]byte, 8)
	for _, number := range []uint64{
		c.fenceEpoch, uint64(c.exitCode), c.outputBytes,
		uint64(c.startedAt.UnixNano()), uint64(c.endedAt.UnixNano()),
	} {
		binary.BigEndian.PutUint64(buf, number)
		h.Write(buf)
	}
	h.Write(c.nonce)
	return contract.SHA256(h.Sum(nil))
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Kind is how the Control Plane treats a received claim.
type Kind string

const (
	// KindNew: the first claim for an open Attempt. Record it and apply the
	// outcome to the Attempt.
	KindNew Kind = "new"
	// KindDuplicate: the same claim again. Record nothing and change nothing.
	KindDuplicate Kind = "duplicate"
	// KindStale: the claim is from an older fence epoch. Keep it as evidence,
	// but do not close the current Attempt or revive anything.
	KindStale Kind = "stale"
	// KindLate: the Attempt is already closed. Keep the claim as evidence and
	// do not reopen the Attempt.
	KindLate Kind = "late"
)

// Existing is the durable state the Control Plane has for the Attempt, read in
// the same operation that records the claim.
type Existing struct {
	// AttemptID is the Attempt the claim names. It must already exist.
	AttemptID ids.AttemptID
	// AttemptOpen is false when the Attempt is closed.
	AttemptOpen bool
	// CurrentFence is the latest fence epoch the Control Plane accepted for the
	// Task. Zero means none has been accepted yet.
	CurrentFence uint64
	// Recorded is the claim already stored for this Attempt, if any.
	Recorded *Claim
}

// Decision tells the store what to do with one received claim.
type Decision struct {
	Kind   Kind
	Record bool
	// CloseAttempt is true only for a new claim on an open Attempt.
	CloseAttempt bool
	// Reason is a stable code for the audit record.
	Reason string
}

// Decide compares a received claim with the current state. An inconsistent
// claim (a different claim for the same Attempt) is rejected; the first claim
// wins. Everything else is classified, never inferred from the Worker's word
// alone.
func Decide(claim Claim, existing Existing) (Decision, error) {
	if !claim.Valid() {
		return Decision{}, fmt.Errorf("%w: claim is not constructed", ErrInvalidClaim)
	}
	if existing.AttemptID != claim.AttemptID() {
		return Decision{}, fmt.Errorf("%w: claim is for another Attempt", ErrInvalidClaim)
	}
	if existing.Recorded != nil && !existing.Recorded.Equal(claim) {
		return Decision{}, ErrConflict
	}
	if claim.FenceEpoch() < existing.CurrentFence {
		return Decision{Kind: KindStale, Record: true, Reason: "fence_epoch_behind_current"}, nil
	}
	if existing.Recorded != nil {
		return Decision{Kind: KindDuplicate, Record: false, Reason: "exact_replay"}, nil
	}
	if !existing.AttemptOpen {
		return Decision{Kind: KindLate, Record: true, Reason: "attempt_already_closed"}, nil
	}
	return Decision{Kind: KindNew, Record: true, CloseAttempt: true, Reason: "first_claim"}, nil
}

// ClosesAttempt reports whether the outcome should move an open Attempt to a
// terminal state. runner_stuck is not a close: the platform must stop the
// process first, and the Agent reports a follow-up claim.
func (s Status) ClosesAttempt() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusReclaimed, StatusDeadlineExceeded,
		StatusCancelled, StatusOutputExceeded:
		return true
	case StatusRunnerStuck:
		return false
	}
	return false
}

// NeedsVerification reports whether a completed claim should go to Result
// Verification. It does not mean the result is accepted.
func (s Status) NeedsVerification() bool { return s == StatusCompleted }
