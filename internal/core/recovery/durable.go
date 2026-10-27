package recovery

import (
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// Validate reports whether the Pending is well formed. A durable store calls it
// before it keeps one.
func (p Pending) Validate() error { return validatePending(p) }

// PermitRecord is the stored form of a Permit. Only a State Store adapter reads
// and writes it, for rows that Release produced. Digest binds every other field,
// so a row that was edited after it was written is refused when it is read back.
type PermitRecord struct {
	TaskID                 ids.TaskID
	TaskIRDigest           contract.Digest
	PreviousAttemptID      ids.AttemptID
	PreviousAttemptNumber  uint32
	NextAttemptNumber      uint32
	PolicyRevision         uint64
	Sequence               uint64
	IssuedAt               time.Time
	NotBefore              time.Time
	ExpiresAt              time.Time
	EstimatedTransferBytes uint64
	EstimatedComputeMillis uint64
	Digest                 contract.Digest
}

// Record returns the stored form of the Permit.
func (p Permit) Record() PermitRecord {
	return PermitRecord{
		TaskID: p.taskID, TaskIRDigest: p.taskIRDigest,
		PreviousAttemptID: p.previousAttemptID, PreviousAttemptNumber: p.previousAttemptNumber,
		NextAttemptNumber: p.nextAttemptNumber, PolicyRevision: p.policyRevision, Sequence: p.sequence,
		IssuedAt: p.issuedAt, NotBefore: p.notBefore, ExpiresAt: p.expiresAt,
		EstimatedTransferBytes: p.estimatedTransferBytes, EstimatedComputeMillis: p.estimatedComputeMillis,
		Digest: p.Digest(),
	}
}

// RestorePermit rebuilds a Permit from a stored row. It exists for State Store
// adapters. The Permit that comes back is only as trustworthy as the row: the
// digest check refuses a row whose fields no longer match what Release issued,
// and Permit.Validate still decides whether it may be used now.
func RestorePermit(r PermitRecord) (Permit, error) {
	p := Permit{
		taskID: r.TaskID, taskIRDigest: r.TaskIRDigest,
		previousAttemptID: r.PreviousAttemptID, previousAttemptNumber: r.PreviousAttemptNumber,
		nextAttemptNumber: r.NextAttemptNumber, policyRevision: r.PolicyRevision, sequence: r.Sequence,
		issuedAt: r.IssuedAt.UTC(), notBefore: r.NotBefore.UTC(), expiresAt: r.ExpiresAt.UTC(),
		estimatedTransferBytes: r.EstimatedTransferBytes, estimatedComputeMillis: r.EstimatedComputeMillis,
	}
	if err := p.validateShape(); err != nil {
		return Permit{}, err
	}
	if p.Digest() != r.Digest {
		return Permit{}, fmt.Errorf("%w: the stored digest does not match the stored fields", ErrInvalidPermit)
	}
	return p, nil
}

// CheckRelease verifies a Decision against the Budget the pass started from. A
// durable store runs it before it commits the Decision, so a caller cannot
// under-report what a pass consumed, replay a Permit under another sequence or
// policy, or carry a budget over a window boundary that did not happen.
//
// before is the budget as stored, before the window is advanced. now is the time
// the pass ran.
func CheckRelease(now time.Time, policy Policy, before Budget, d Decision) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if d.Sequence == 0 {
		return fmt.Errorf("%w: the decision has no sequence", ErrInvalidPending)
	}
	if d.PolicyRevision != policy.Revision {
		return fmt.Errorf("%w: the decision is for policy %d, not %d", ErrInvalidPending, d.PolicyRevision, policy.Revision)
	}
	base, err := advanceWindow(now.UTC(), policy, before)
	if err != nil {
		return err
	}
	want := base
	seen := make(map[ids.TaskID]struct{}, len(d.Permits))
	for _, permit := range d.Permits {
		if err := permit.validateShape(); err != nil {
			return err
		}
		if permit.policyRevision != policy.Revision || permit.sequence != d.Sequence {
			return fmt.Errorf("%w: a Permit does not belong to sequence %d of policy %d", ErrInvalidPermit, d.Sequence, policy.Revision)
		}
		if _, dup := seen[permit.taskID]; dup {
			return fmt.Errorf("%w: Task %s has two Permits in one pass", ErrDuplicateConflict, permit.taskID)
		}
		seen[permit.taskID] = struct{}{}
		want.Released++
		want.TransferBytes += permit.estimatedTransferBytes
		want.ComputeMillis += permit.estimatedComputeMillis
	}
	if !sameBudget(want, d.Budget) {
		return fmt.Errorf("%w: the decision reports a budget that does not match what its Permits consumed", ErrInvalidPending)
	}
	return validateBudget(policy, d.Budget)
}

func sameBudget(a, b Budget) bool {
	return a.WindowStart.Equal(b.WindowStart) && a.Released == b.Released &&
		a.TransferBytes == b.TransferBytes && a.ComputeMillis == b.ComputeMillis
}
