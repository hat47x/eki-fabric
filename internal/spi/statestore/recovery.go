package statestore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/recovery"
)

// ErrInvalidRecovery means a recovery operation is malformed on its own, before
// any stored state is read.
var ErrInvalidRecovery = errors.New("invalid recovery operation")

// RecoveryWindow is the durable state of the recovery budget: the window that is
// open, what it consumed, and the sequence of the last pass that was committed.
type RecoveryWindow struct {
	PolicyRevision uint64
	Budget         recovery.Budget
	LastSequence   uint64
}

// RecoveryRelease is one pass of recovery.Release, ready to commit. ExpectedBudget
// is the budget the pass started from, exactly as LoadRecoveryWindow returned it
// (the zero value when no window is stored yet).
type RecoveryRelease struct {
	Policy         recovery.Policy
	Now            time.Time
	ExpectedBudget recovery.Budget
	Decision       recovery.Decision
}

// Fingerprint identifies the content of a pass, so that a repeat of it can be
// told from a different pass that reuses the same sequence.
func (r RecoveryRelease) Fingerprint() contract.Digest {
	h := sha256.New()
	h.Write([]byte("eki.recovery-release/v1"))
	buf := make([]byte, 8)
	put := func(n uint64) {
		binary.BigEndian.PutUint64(buf, n)
		h.Write(buf)
	}
	put(r.Decision.PolicyRevision)
	put(r.Decision.Sequence)
	put(uint64(r.Decision.Budget.WindowStart.UnixNano()))
	put(uint64(r.Decision.Budget.Released))
	put(r.Decision.Budget.TransferBytes)
	put(r.Decision.Budget.ComputeMillis)
	put(uint64(len(r.Decision.Permits)))
	for _, permit := range r.Decision.Permits {
		digest := permit.Digest()
		h.Write([]byte(digest.String()))
	}
	put(uint64(len(r.Decision.Expired)))
	for _, expired := range r.Decision.Expired {
		h.Write([]byte(expired.TaskID.String()))
		h.Write([]byte(expired.PreviousAttemptID.String()))
	}
	return contract.SHA256(h.Sum(nil))
}

// Validate checks the pass without reading any stored state.
func (r RecoveryRelease) Validate() error {
	if r.Now.IsZero() {
		return fmt.Errorf("%w: the time of the pass is required", ErrInvalidRecovery)
	}
	if err := recovery.CheckRelease(r.Now, r.Policy, r.ExpectedBudget, r.Decision); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRecovery, err)
	}
	return nil
}

// RecoveryAdmission turns a Permit into the next Attempt. The Permit is the one
// stored for PreviousAttemptID. NewAttempt must be the initial record of the
// attempt the Permit admits.
type RecoveryAdmission struct {
	PreviousAttemptID ids.AttemptID
	NewAttempt        attemptstate.AttemptRecord
	// PolicyRevision is the recovery policy the caller considers current.
	PolicyRevision uint64
	Now            time.Time
}

// Validate checks the admission without reading any stored state.
func (a RecoveryAdmission) Validate() error {
	switch {
	case a.PreviousAttemptID == "":
		return fmt.Errorf("%w: the previous Attempt is required", ErrInvalidRecovery)
	case a.PolicyRevision == 0:
		return fmt.Errorf("%w: the current policy revision is required", ErrInvalidRecovery)
	case a.Now.IsZero():
		return fmt.Errorf("%w: the time is required", ErrInvalidRecovery)
	}
	if err := a.NewAttempt.ValidateInitial(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRecovery, err)
	}
	return nil
}

// StoredPermit is a Permit with what became of it.
type StoredPermit struct {
	Permit recovery.Permit
	// SpentBy is the Attempt created from the Permit. It is empty while the
	// Permit is unused.
	SpentBy ids.AttemptID
}

// RecoveryStore is the durable owner of the recovery state: the retries that
// wait, the budget a window has consumed, and the Permits that were issued.
//
// Every operation is atomic. A Task has at most one pending retry, and a
// previous Attempt yields at most one Permit and one new Attempt.
//
//   - EnqueueRecovery fails with ErrNotFound when the previous Attempt or its Task
//     is unknown, and with ErrConflict when the previous Attempt is not closed
//     with an interruption reason, does not match the Pending, the Task cannot
//     take a new Attempt, or the Task already waits for a retry of another
//     Attempt. A Pending already stored for the same previous Attempt is
//     returned as stored, with replayed true: the stored one wins, so a later
//     pass cannot move its NotBefore.
//   - CommitRecoveryRelease fails with ErrConflict when the sequence does not
//     follow the stored one (a repeat of the stored pass is reported as replayed
//     and changes nothing), when ExpectedBudget is not the stored budget, or when
//     a Permit or an expired retry has no matching Pending.
//   - AdmitRecoveryAttempt fails with the recovery package's Permit errors when
//     the Permit is expired, not yet valid, or for another policy revision, with
//     ErrAlreadyExists when it was used, and with ErrConflict when the Task
//     cannot take the Attempt. It creates the Attempt and marks the Permit used
//     together, or does neither.
type RecoveryStore interface {
	EnqueueRecovery(ctx context.Context, pending recovery.Pending) (stored recovery.Pending, replayed bool, err error)
	LoadRecoveryPending(ctx context.Context) ([]recovery.Pending, error)
	LoadRecoveryWindow(ctx context.Context) (RecoveryWindow, bool, error)
	CommitRecoveryRelease(ctx context.Context, release RecoveryRelease) (replayed bool, err error)
	LoadRecoveryPermit(ctx context.Context, previous ids.AttemptID) (StoredPermit, bool, error)
	AdmitRecoveryAttempt(ctx context.Context, admission RecoveryAdmission) error
}
