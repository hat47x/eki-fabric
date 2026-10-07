// Package attemptstate defines the durable lifecycle state of one Task execution attempt.
package attemptstate

import (
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var ErrInvalidAttemptRecord = errors.New("invalid attempt record")

type AttemptPhase string

const (
	AttemptPhaseCreated    AttemptPhase = "created"
	AttemptPhasePlacing    AttemptPhase = "placing"
	AttemptPhaseAssigned   AttemptPhase = "assigned"
	AttemptPhaseDispatched AttemptPhase = "dispatched"
	AttemptPhaseClaimed    AttemptPhase = "claimed"
	AttemptPhaseVerifying  AttemptPhase = "verifying"
	AttemptPhaseClosed     AttemptPhase = "closed"
)

type CloseReason string

const (
	CloseReasonNone                      CloseReason = ""
	CloseReasonResultAccepted            CloseReason = "result_accepted"
	CloseReasonResultRejected            CloseReason = "result_rejected"
	CloseReasonExecutionFailureConfirmed CloseReason = "execution_failure_confirmed"
	CloseReasonWorkerLost                CloseReason = "worker_lost"
	CloseReasonLeaseExpired              CloseReason = "lease_expired"
	CloseReasonReclaimed                 CloseReason = "reclaimed"
	CloseReasonCancelled                 CloseReason = "cancelled"
	CloseReasonSuperseded                CloseReason = "superseded"
	CloseReasonDeadline                  CloseReason = "deadline"
	CloseReasonBudgetExhausted           CloseReason = "budget_exhausted"
	CloseReasonUnknownExternalEffect     CloseReason = "unknown_external_effect"
	CloseReasonPolicyRevoked             CloseReason = "policy_revoked"
	CloseReasonStaleAttempt              CloseReason = "stale_attempt"
)

type AttemptRecord struct {
	ID             ids.AttemptID
	TaskID         ids.TaskID
	TaskIRDigest   contract.Digest
	Number         uint64
	Phase          AttemptPhase
	WorkerIdentity string
	LeaseID        ids.LeaseID
	CloseReason    CloseReason
	Revision       uint64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func New(id ids.AttemptID, taskID ids.TaskID, taskIRDigest contract.Digest, number uint64, now time.Time) (AttemptRecord, error) {
	record := AttemptRecord{
		ID:           id,
		TaskID:       taskID,
		TaskIRDigest: taskIRDigest,
		Number:       number,
		Phase:        AttemptPhaseCreated,
		Revision:     1,
		CreatedAt:    now.UTC(),
		UpdatedAt:    now.UTC(),
	}
	if err := record.Validate(); err != nil {
		return AttemptRecord{}, err
	}
	return record, nil
}

// ValidateInitial checks the only state from which a durable Attempt may be
// created. Later phases must be reached through ValidateTransition.
func (r AttemptRecord) ValidateInitial() error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Phase != AttemptPhaseCreated || r.Revision != 1 {
		return fmt.Errorf("%w: new Attempt must start at created revision 1", ErrInvalidAttemptRecord)
	}
	return nil
}

func (r AttemptRecord) Validate() error {
	if _, err := ids.ParseAttemptID(r.ID.String()); err != nil {
		return fmt.Errorf("%w: attempt ID: %v", ErrInvalidAttemptRecord, err)
	}
	if _, err := ids.ParseTaskID(r.TaskID.String()); err != nil {
		return fmt.Errorf("%w: task ID: %v", ErrInvalidAttemptRecord, err)
	}
	if r.TaskIRDigest.IsZero() {
		return fmt.Errorf("%w: TaskIR digest is required", ErrInvalidAttemptRecord)
	}
	if r.Number == 0 || r.Number > math.MaxInt64 {
		return fmt.Errorf("%w: attempt number must fit positive int64", ErrInvalidAttemptRecord)
	}
	if !validPhase(r.Phase) {
		return fmt.Errorf("%w: unknown phase %q", ErrInvalidAttemptRecord, r.Phase)
	}
	if r.WorkerIdentity != "" && (len(r.WorkerIdentity) > 256 || !utf8.ValidString(r.WorkerIdentity)) {
		return fmt.Errorf("%w: invalid Worker identity", ErrInvalidAttemptRecord)
	}
	if r.LeaseID != "" {
		if _, err := ids.ParseLeaseID(r.LeaseID.String()); err != nil {
			return fmt.Errorf("%w: lease ID: %v", ErrInvalidAttemptRecord, err)
		}
		if r.WorkerIdentity == "" {
			return fmt.Errorf("%w: Lease binding requires Worker identity", ErrInvalidAttemptRecord)
		}
	}
	if r.Revision == 0 || r.Revision > math.MaxInt64 {
		return fmt.Errorf("%w: revision must fit positive int64", ErrInvalidAttemptRecord)
	}
	if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) {
		return fmt.Errorf("%w: invalid timestamps", ErrInvalidAttemptRecord)
	}

	switch r.Phase {
	case AttemptPhaseCreated:
		if r.WorkerIdentity != "" || r.LeaseID != "" {
			return fmt.Errorf("%w: created Attempt cannot already bind Worker or Lease", ErrInvalidAttemptRecord)
		}
	case AttemptPhasePlacing:
		if r.WorkerIdentity == "" || r.LeaseID != "" {
			return fmt.Errorf("%w: placing Attempt requires Worker and no Lease", ErrInvalidAttemptRecord)
		}
	case AttemptPhaseAssigned, AttemptPhaseDispatched, AttemptPhaseClaimed, AttemptPhaseVerifying:
		if r.WorkerIdentity == "" || r.LeaseID == "" {
			return fmt.Errorf("%w: phase %q requires Worker and Lease", ErrInvalidAttemptRecord, r.Phase)
		}
	case AttemptPhaseClosed:
		if !validCloseReason(r.CloseReason) || r.CloseReason == CloseReasonNone {
			return fmt.Errorf("%w: closed Attempt requires close reason", ErrInvalidAttemptRecord)
		}
	}
	if r.Phase != AttemptPhaseClosed && r.CloseReason != CloseReasonNone {
		return fmt.Errorf("%w: non-closed Attempt cannot carry close reason %q", ErrInvalidAttemptRecord, r.CloseReason)
	}
	return nil
}

func validPhase(v AttemptPhase) bool {
	switch v {
	case AttemptPhaseCreated, AttemptPhasePlacing, AttemptPhaseAssigned, AttemptPhaseDispatched, AttemptPhaseClaimed, AttemptPhaseVerifying, AttemptPhaseClosed:
		return true
	default:
		return false
	}
}

func validCloseReason(v CloseReason) bool {
	switch v {
	case CloseReasonNone,
		CloseReasonResultAccepted,
		CloseReasonResultRejected,
		CloseReasonExecutionFailureConfirmed,
		CloseReasonWorkerLost,
		CloseReasonLeaseExpired,
		CloseReasonReclaimed,
		CloseReasonCancelled,
		CloseReasonSuperseded,
		CloseReasonDeadline,
		CloseReasonBudgetExhausted,
		CloseReasonUnknownExternalEffect,
		CloseReasonPolicyRevoked,
		CloseReasonStaleAttempt:
		return true
	default:
		return false
	}
}
