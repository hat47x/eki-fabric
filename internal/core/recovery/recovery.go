package recovery

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"math"
	"sort"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var (
	ErrInvalidPolicy        = errors.New("invalid recovery policy")
	ErrInvalidInterruption  = errors.New("invalid interruption")
	ErrDuplicateConflict    = errors.New("conflicting duplicate interruption")
	ErrInvalidPending       = errors.New("invalid pending retry")
	ErrClockRegression      = errors.New("current time is before the recovery window")
	ErrInvalidPermit        = errors.New("invalid recovery permit")
	ErrPermitSubject        = errors.New("recovery permit belongs to another subject")
	ErrPermitPolicyRevision = errors.New("recovery permit was issued under another policy revision")
	ErrPermitNotYetValid    = errors.New("recovery permit is not valid yet")
	ErrPermitExpired        = errors.New("recovery permit has expired")
)

// Priority orders releases inside a window. It is not budget authority.
type Priority uint8

const (
	PriorityBestEffort Priority = iota
	PriorityNormal
	PriorityUrgent
)

func (p Priority) valid() bool { return p <= PriorityUrgent }

// Contract is the part of a Task's reliability contract that the recovery
// boundary needs (RQ-011). It never guesses these values from the Task's
// payload; the caller reads them from the durable Task state.
type Contract struct {
	TaskID       ids.TaskID
	TaskIRDigest contract.Digest
	// Retryable is the Task's explicit statement that a new Attempt is safe.
	// A Task that does not say so is never retried automatically.
	Retryable bool
	// MaxAttempts bounds the total number of Attempts, including the first.
	// It is part of the Task contract and cannot be raised by a recovery.
	MaxAttempts uint32
	// Deadline is the point in time after which a new Attempt is useless.
	// The zero value means that the Task has no deadline.
	Deadline time.Time
}

// Interruption is one Attempt that lost its execution authority. A repeated
// delivery of the same AttemptID is normal and is collapsed; a different
// record that claims the same AttemptID is rejected.
type Interruption struct {
	AttemptID              ids.AttemptID
	AttemptNumber          uint32
	Contract               Contract
	Priority               Priority
	EstimatedTransferBytes uint64
	EstimatedComputeMillis uint64
}

// Policy is the recovery policy. It is identified by Revision so that a Permit
// can be bound to the policy that produced it.
type Policy struct {
	Revision                  uint64
	BaseBackoff               time.Duration
	MaxBackoff                time.Duration
	Window                    time.Duration
	MaxReleasesPerWindow      uint32
	MaxTransferBytesPerWindow uint64
	MaxComputeMillisPerWindow uint64
	PermitTTL                 time.Duration
}

// Validate rejects policies that would make a budget meaningless. An
// unbounded budget is deliberately not expressible.
func (p Policy) Validate() error {
	switch {
	case p.Revision == 0:
		return fmt.Errorf("%w: Revision must be positive", ErrInvalidPolicy)
	case p.BaseBackoff <= 0:
		return fmt.Errorf("%w: BaseBackoff must be positive", ErrInvalidPolicy)
	case p.MaxBackoff < p.BaseBackoff:
		return fmt.Errorf("%w: MaxBackoff must be at least BaseBackoff", ErrInvalidPolicy)
	case p.Window <= 0:
		return fmt.Errorf("%w: Window must be positive", ErrInvalidPolicy)
	case p.MaxReleasesPerWindow == 0:
		return fmt.Errorf("%w: MaxReleasesPerWindow must be positive", ErrInvalidPolicy)
	case p.MaxTransferBytesPerWindow == 0:
		return fmt.Errorf("%w: MaxTransferBytesPerWindow must be positive", ErrInvalidPolicy)
	case p.MaxComputeMillisPerWindow == 0:
		return fmt.Errorf("%w: MaxComputeMillisPerWindow must be positive", ErrInvalidPolicy)
	case p.PermitTTL <= 0:
		return fmt.Errorf("%w: PermitTTL must be positive", ErrInvalidPolicy)
	}
	return nil
}

// HoldReason explains why a retry will not happen automatically.
type HoldReason string

const (
	HoldNotRetryable         HoldReason = "not_retryable"
	HoldRetryBudgetExhausted HoldReason = "retry_budget_exhausted"
	HoldDeadlinePassed       HoldReason = "deadline_passed"
	HoldDeadlineUnreachable  HoldReason = "deadline_unreachable"
)

// Hold is a Task that recovery refuses to retry automatically. The Task stays
// in its current state for a human or an upper-layer decision; it is not
// silently marked failed.
type Hold struct {
	TaskID    ids.TaskID
	AttemptID ids.AttemptID
	Attempt   uint32
	Reason    HoldReason
	Detail    string
}

// Pending is an admitted retry that has not been released yet. It must be
// persisted by the caller: recomputing it with a later "now" would move the
// backoff and defeat the point of jitter.
type Pending struct {
	TaskID                 ids.TaskID
	TaskIRDigest           contract.Digest
	PreviousAttemptID      ids.AttemptID
	PreviousAttemptNumber  uint32
	NextAttemptNumber      uint32
	MaxAttempts            uint32
	Priority               Priority
	Deadline               time.Time
	NotBefore              time.Time
	EstimatedTransferBytes uint64
	EstimatedComputeMillis uint64
}

// Intake classifies interrupted Attempts. Nothing is released here: the
// returned Pending values carry the earliest time at which Release may admit
// them.
type Intake struct {
	Pending    []Pending
	Held       []Hold
	Duplicates int
}

// Classify classifies the interrupted Attempts read from durable state.
//
// Retryable is evaluated before the attempt budget, so a non-retryable Task is
// never retried even if it still has attempts left. A retry is held, not
// deferred, when it can never be useful: the attempt budget is exhausted, the
// deadline has passed, or the jittered start is at or after the deadline.
func Classify(now time.Time, policy Policy, interrupted []Interruption) (Intake, error) {
	if err := policy.Validate(); err != nil {
		return Intake{}, err
	}
	if now.IsZero() {
		return Intake{}, fmt.Errorf("%w: now is required", ErrInvalidInterruption)
	}
	now = now.UTC()

	out := Intake{}
	seen := make(map[string]string, len(interrupted))
	for _, item := range interrupted {
		if err := validateInterruption(item); err != nil {
			return Intake{}, err
		}
		key := item.Contract.TaskID.String() + "\x00" + item.AttemptID.String()
		fingerprint := interruptionFingerprint(item)
		if previous, ok := seen[key]; ok {
			if previous != fingerprint {
				return Intake{}, fmt.Errorf("%w: %s", ErrDuplicateConflict, item.AttemptID)
			}
			out.Duplicates++
			continue
		}
		seen[key] = fingerprint

		if !item.Contract.Retryable {
			out.Held = append(out.Held, Hold{
				TaskID: item.Contract.TaskID, AttemptID: item.AttemptID,
				Attempt: item.AttemptNumber, Reason: HoldNotRetryable,
				Detail: "the Task does not declare retryable execution",
			})
			continue
		}
		next := item.AttemptNumber + 1
		if item.AttemptNumber == 0 || item.AttemptNumber == math.MaxUint32 || next > item.Contract.MaxAttempts {
			out.Held = append(out.Held, Hold{
				TaskID: item.Contract.TaskID, AttemptID: item.AttemptID,
				Attempt: item.AttemptNumber, Reason: HoldRetryBudgetExhausted,
				Detail: "the attempt budget of the Task is exhausted",
			})
			continue
		}
		if deadline := item.Contract.Deadline.UTC(); !deadline.IsZero() && !now.Before(deadline) {
			out.Held = append(out.Held, Hold{
				TaskID: item.Contract.TaskID, AttemptID: item.AttemptID,
				Attempt: item.AttemptNumber, Reason: HoldDeadlinePassed,
				Detail: "the Task deadline has already passed",
			})
			continue
		}

		notBefore := now.Add(backoff(policy, item.Contract.TaskID.String(), next))
		if deadline := item.Contract.Deadline.UTC(); !deadline.IsZero() && !notBefore.Before(deadline) {
			out.Held = append(out.Held, Hold{
				TaskID: item.Contract.TaskID, AttemptID: item.AttemptID,
				Attempt: item.AttemptNumber, Reason: HoldDeadlineUnreachable,
				Detail: "the earliest jittered retry is not before the Task deadline",
			})
			continue
		}

		out.Pending = append(out.Pending, Pending{
			TaskID:                 item.Contract.TaskID,
			TaskIRDigest:           item.Contract.TaskIRDigest,
			PreviousAttemptID:      item.AttemptID,
			PreviousAttemptNumber:  item.AttemptNumber,
			NextAttemptNumber:      next,
			MaxAttempts:            item.Contract.MaxAttempts,
			Priority:               item.Priority,
			Deadline:               item.Contract.Deadline.UTC(),
			NotBefore:              notBefore,
			EstimatedTransferBytes: item.EstimatedTransferBytes,
			EstimatedComputeMillis: item.EstimatedComputeMillis,
		})
	}
	return out, nil
}

// Budget is the part of a recovery window that has already been consumed. The
// caller persists it with the window start and passes it back unchanged.
type Budget struct {
	WindowStart   time.Time
	Released      uint32
	TransferBytes uint64
	ComputeMillis uint64
}

// DeferReason explains why an eligible retry did not re-enter in this window.
type DeferReason string

const (
	DeferNotYetEligible     DeferReason = "not_yet_eligible"
	DeferCountBudget        DeferReason = "release_count_budget"
	DeferTransferBudget     DeferReason = "transfer_budget"
	DeferComputeBudget      DeferReason = "compute_budget"
	DeferSingleItemTooLarge DeferReason = "single_item_exceeds_window_budget"
)

// Deferred is a retry that stays in the pending queue for a later window.
type Deferred struct {
	Pending
	Reason DeferReason
}

// Decision is the result of one release pass. Outbox consumers must deliver
// only the returned Permits; Deferred, Expired, and the holds from Classify
// grant no execution.
type Decision struct {
	PolicyRevision uint64
	Sequence       uint64
	Permits        []Permit
	Deferred       []Deferred
	// Expired lists pending retries whose Task deadline passed while they
	// waited. They must be removed from the pending queue; keeping them would
	// let a later pass believe that the Task can still run.
	Expired []Pending
	Budget  Budget
}

// Release admits as many pending retries as the window budgets allow.
//
// Ordering is deterministic: priority (urgent first), then deadline (earlier
// first, with no deadline last), then NotBefore, then Task ID. The ordering
// only decides who goes first; every candidate, including an urgent one, is
// still charged against the same three budgets.
//
// Sequence must increase monotonically for each release pass. It is bound into
// the Permit so that two passes cannot produce indistinguishable evidence.
func Release(now time.Time, policy Policy, pending []Pending, budget Budget, sequence uint64) (Decision, error) {
	if err := policy.Validate(); err != nil {
		return Decision{}, err
	}
	if now.IsZero() {
		return Decision{}, fmt.Errorf("%w: now is required", ErrInvalidPending)
	}
	if sequence == 0 {
		return Decision{}, fmt.Errorf("%w: sequence must be positive", ErrInvalidPending)
	}
	now = now.UTC()

	budget, err := advanceWindow(now, policy, budget)
	if err != nil {
		return Decision{}, err
	}
	if err := validateBudget(policy, budget); err != nil {
		return Decision{}, err
	}
	for _, item := range pending {
		if err := validatePending(item); err != nil {
			return Decision{}, err
		}
	}

	ordered := append([]Pending(nil), pending...)
	sort.SliceStable(ordered, func(i, j int) bool { return lessPending(ordered[i], ordered[j]) })

	// One Task must have at most one pending retry. A repeated delivery of the
	// same record is collapsed; two different records for the same Task mean
	// the queue is inconsistent, so the pass fails closed instead of admitting
	// the Task twice.
	seen := make(map[ids.TaskID]string, len(ordered))
	deduped := make([]Pending, 0, len(ordered))
	for _, item := range ordered {
		fingerprint := pendingFingerprint(item)
		if previous, ok := seen[item.TaskID]; ok {
			if previous != fingerprint {
				return Decision{}, fmt.Errorf("%w: Task %s has two different pending retries", ErrDuplicateConflict, item.TaskID)
			}
			continue
		}
		seen[item.TaskID] = fingerprint
		deduped = append(deduped, item)
	}

	decision := Decision{PolicyRevision: policy.Revision, Sequence: sequence, Budget: budget}
	for _, item := range deduped {
		if !item.Deadline.IsZero() && !now.Before(item.Deadline) {
			decision.Expired = append(decision.Expired, item)
			continue
		}
		if now.Before(item.NotBefore) {
			decision.Deferred = append(decision.Deferred, Deferred{Pending: item, Reason: DeferNotYetEligible})
			continue
		}
		if item.EstimatedTransferBytes > policy.MaxTransferBytesPerWindow ||
			item.EstimatedComputeMillis > policy.MaxComputeMillisPerWindow {
			decision.Deferred = append(decision.Deferred, Deferred{Pending: item, Reason: DeferSingleItemTooLarge})
			continue
		}
		if decision.Budget.Released >= policy.MaxReleasesPerWindow {
			decision.Deferred = append(decision.Deferred, Deferred{Pending: item, Reason: DeferCountBudget})
			continue
		}
		if item.EstimatedComputeMillis > policy.MaxComputeMillisPerWindow-decision.Budget.ComputeMillis {
			decision.Deferred = append(decision.Deferred, Deferred{Pending: item, Reason: DeferComputeBudget})
			continue
		}
		if item.EstimatedTransferBytes > policy.MaxTransferBytesPerWindow-decision.Budget.TransferBytes {
			decision.Deferred = append(decision.Deferred, Deferred{Pending: item, Reason: DeferTransferBudget})
			continue
		}

		permit, err := issuePermit(now, policy, sequence, item)
		if err != nil {
			return Decision{}, err
		}
		decision.Permits = append(decision.Permits, permit)
		decision.Budget.Released++
		decision.Budget.TransferBytes += item.EstimatedTransferBytes
		decision.Budget.ComputeMillis += item.EstimatedComputeMillis
	}
	return decision, nil
}

// advanceWindow opens a new window when the current one has elapsed. A clock
// that moves backwards is not accepted: it could otherwise reset a budget.
func advanceWindow(now time.Time, policy Policy, budget Budget) (Budget, error) {
	if budget.WindowStart.IsZero() {
		budget.WindowStart = now
		return budget, nil
	}
	start := budget.WindowStart.UTC()
	if now.Before(start) {
		return Budget{}, fmt.Errorf("%w: window starts at %s, now is %s", ErrClockRegression, start, now)
	}
	if now.Sub(start) >= policy.Window {
		return Budget{WindowStart: now}, nil
	}
	budget.WindowStart = start
	return budget, nil
}

// validateBudget rejects a consumed budget that is already outside the policy.
// A corrupt or rolled-back counter must fail closed instead of underflowing
// the remaining-budget arithmetic.
func validateBudget(policy Policy, budget Budget) error {
	switch {
	case budget.Released > policy.MaxReleasesPerWindow:
		return fmt.Errorf("%w: consumed releases %d exceed the window budget %d", ErrInvalidPending, budget.Released, policy.MaxReleasesPerWindow)
	case budget.TransferBytes > policy.MaxTransferBytesPerWindow:
		return fmt.Errorf("%w: consumed transfer %d exceeds the window budget %d", ErrInvalidPending, budget.TransferBytes, policy.MaxTransferBytesPerWindow)
	case budget.ComputeMillis > policy.MaxComputeMillisPerWindow:
		return fmt.Errorf("%w: consumed compute %d exceeds the window budget %d", ErrInvalidPending, budget.ComputeMillis, policy.MaxComputeMillisPerWindow)
	}
	return nil
}

func lessPending(a, b Pending) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	az := a.Deadline.IsZero()
	bz := b.Deadline.IsZero()
	if az != bz {
		return !az
	}
	if !az && !a.Deadline.Equal(b.Deadline) {
		return a.Deadline.Before(b.Deadline)
	}
	if !a.NotBefore.Equal(b.NotBefore) {
		return a.NotBefore.Before(b.NotBefore)
	}
	return a.TaskID.String() < b.TaskID.String()
}

// Permit is evidence that one new Attempt was admitted by the recovery
// boundary. The zero value is invalid, and the fields are unexported so that a
// Scheduler, a Client, or a Worker cannot mint one.
type Permit struct {
	taskID                 ids.TaskID
	taskIRDigest           contract.Digest
	previousAttemptID      ids.AttemptID
	previousAttemptNumber  uint32
	nextAttemptNumber      uint32
	policyRevision         uint64
	sequence               uint64
	issuedAt               time.Time
	notBefore              time.Time
	expiresAt              time.Time
	estimatedTransferBytes uint64
	estimatedComputeMillis uint64
}

func issuePermit(now time.Time, policy Policy, sequence uint64, item Pending) (Permit, error) {
	expires := now.Add(policy.PermitTTL)
	if !item.Deadline.IsZero() && item.Deadline.Before(expires) {
		expires = item.Deadline
	}
	if !expires.After(item.NotBefore) {
		return Permit{}, fmt.Errorf("%w: permit would expire before it becomes usable", ErrInvalidPending)
	}
	return Permit{
		taskID:                 item.TaskID,
		taskIRDigest:           item.TaskIRDigest,
		previousAttemptID:      item.PreviousAttemptID,
		previousAttemptNumber:  item.PreviousAttemptNumber,
		nextAttemptNumber:      item.NextAttemptNumber,
		policyRevision:         policy.Revision,
		sequence:               sequence,
		issuedAt:               now,
		notBefore:              item.NotBefore,
		expiresAt:              expires,
		estimatedTransferBytes: item.EstimatedTransferBytes,
		estimatedComputeMillis: item.EstimatedComputeMillis,
	}, nil
}

func (p Permit) TaskID() ids.TaskID               { return p.taskID }
func (p Permit) TaskIRDigest() contract.Digest    { return p.taskIRDigest }
func (p Permit) PreviousAttemptID() ids.AttemptID { return p.previousAttemptID }
func (p Permit) PreviousAttemptNumber() uint32    { return p.previousAttemptNumber }
func (p Permit) NextAttemptNumber() uint32        { return p.nextAttemptNumber }
func (p Permit) PolicyRevision() uint64           { return p.policyRevision }
func (p Permit) Sequence() uint64                 { return p.sequence }
func (p Permit) IssuedAt() time.Time              { return p.issuedAt }
func (p Permit) NotBefore() time.Time             { return p.notBefore }
func (p Permit) ExpiresAt() time.Time             { return p.expiresAt }
func (p Permit) EstimatedTransferBytes() uint64   { return p.estimatedTransferBytes }
func (p Permit) EstimatedComputeMillis() uint64   { return p.estimatedComputeMillis }

// Subject identifies the Task and previous Attempt that a Permit is spent on,
// together with the policy revision the caller currently considers current.
type Subject struct {
	TaskID            ids.TaskID
	TaskIRDigest      contract.Digest
	PreviousAttemptID ids.AttemptID
	PolicyRevision    uint64
}

// Validate checks the Permit against the subject and the current time. It is
// intentionally the only way to consume a Permit: a caller that skips it has
// no evidence that the admission is still valid.
//
// The checks are ordered so that the returned error names the first failed
// condition. A stale policy revision is reported before an expiry, because the
// policy change is the more important fact to record.
func (p Permit) Validate(now time.Time, subject Subject) error {
	if err := p.validateShape(); err != nil {
		return err
	}
	if subject.TaskID != p.taskID || subject.TaskIRDigest != p.taskIRDigest || subject.PreviousAttemptID != p.previousAttemptID {
		return fmt.Errorf("%w: %s/%s", ErrPermitSubject, p.taskID, p.previousAttemptID)
	}
	if subject.PolicyRevision != p.policyRevision {
		return fmt.Errorf("%w: permit %d, current %d", ErrPermitPolicyRevision, p.policyRevision, subject.PolicyRevision)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: now is required", ErrInvalidPermit)
	}
	if now.UTC().Before(p.notBefore) {
		return fmt.Errorf("%w: valid from %s", ErrPermitNotYetValid, p.notBefore)
	}
	if !now.UTC().Before(p.expiresAt) {
		return fmt.Errorf("%w: expired at %s", ErrPermitExpired, p.expiresAt)
	}
	return nil
}

func (p Permit) validateShape() error {
	switch {
	case p.taskID == "":
		return fmt.Errorf("%w: TaskID is required", ErrInvalidPermit)
	case p.taskIRDigest.IsZero():
		return fmt.Errorf("%w: TaskIRDigest is required", ErrInvalidPermit)
	case p.previousAttemptID == "":
		return fmt.Errorf("%w: previous AttemptID is required", ErrInvalidPermit)
	case p.previousAttemptNumber == 0:
		return fmt.Errorf("%w: previous attempt number must be positive", ErrInvalidPermit)
	case p.nextAttemptNumber != p.previousAttemptNumber+1:
		return fmt.Errorf("%w: next attempt must be previous attempt + 1", ErrInvalidPermit)
	case p.policyRevision == 0:
		return fmt.Errorf("%w: PolicyRevision is required", ErrInvalidPermit)
	case p.sequence == 0:
		return fmt.Errorf("%w: Sequence is required", ErrInvalidPermit)
	case p.issuedAt.IsZero() || p.expiresAt.IsZero():
		return fmt.Errorf("%w: issuance and expiry are required", ErrInvalidPermit)
	case p.expiresAt.Before(p.issuedAt):
		return fmt.Errorf("%w: expiry is before issuance", ErrInvalidPermit)
	case p.expiresAt.Before(p.notBefore):
		return fmt.Errorf("%w: expiry is before the usable time", ErrInvalidPermit)
	}
	return nil
}

// Digest returns the canonical digest of the Permit. It is stable across
// restarts and changes when any bound field changes, so it can be stored with
// the new Attempt and compared later.
func (p Permit) Digest() contract.Digest {
	h := sha256.New()
	h.Write([]byte("eki.recovery-permit/v1"))
	for _, field := range []string{
		p.taskID.String(),
		p.taskIRDigest.String(),
		p.previousAttemptID.String(),
	} {
		writeField(h, field)
	}
	buf := make([]byte, 8)
	for _, number := range []uint64{
		uint64(p.previousAttemptNumber),
		uint64(p.nextAttemptNumber),
		p.policyRevision,
		p.sequence,
		uint64(p.issuedAt.UnixNano()),
		uint64(p.notBefore.UnixNano()),
		uint64(p.expiresAt.UnixNano()),
		p.estimatedTransferBytes,
		p.estimatedComputeMillis,
	} {
		binary.BigEndian.PutUint64(buf, number)
		h.Write(buf)
	}
	return contract.SHA256(h.Sum(nil))
}

func writeField(h hash.Hash, value string) {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(len(value)))
	_, _ = h.Write(buf)
	_, _ = h.Write([]byte(value))
}

func validateInterruption(item Interruption) error {
	switch {
	case item.Contract.TaskID == "":
		return fmt.Errorf("%w: TaskID is required", ErrInvalidInterruption)
	case item.Contract.TaskIRDigest.IsZero():
		return fmt.Errorf("%w: TaskIRDigest is required", ErrInvalidInterruption)
	case item.AttemptID == "":
		return fmt.Errorf("%w: AttemptID is required", ErrInvalidInterruption)
	case !item.Priority.valid():
		return fmt.Errorf("%w: unknown priority %d", ErrInvalidInterruption, item.Priority)
	case item.Contract.MaxAttempts == 0:
		return fmt.Errorf("%w: MaxAttempts must be positive", ErrInvalidInterruption)
	}
	return nil
}

func validatePending(item Pending) error {
	switch {
	case item.TaskID == "":
		return fmt.Errorf("%w: TaskID is required", ErrInvalidPending)
	case item.TaskIRDigest.IsZero():
		return fmt.Errorf("%w: TaskIRDigest is required", ErrInvalidPending)
	case item.PreviousAttemptID == "":
		return fmt.Errorf("%w: previous AttemptID is required", ErrInvalidPending)
	case item.PreviousAttemptNumber == 0:
		return fmt.Errorf("%w: previous attempt number must be positive", ErrInvalidPending)
	case item.NextAttemptNumber != item.PreviousAttemptNumber+1:
		return fmt.Errorf("%w: next attempt must be previous attempt + 1", ErrInvalidPending)
	case item.MaxAttempts == 0 || item.NextAttemptNumber > item.MaxAttempts:
		return fmt.Errorf("%w: next attempt exceeds the attempt budget", ErrInvalidPending)
	case !item.Priority.valid():
		return fmt.Errorf("%w: unknown priority %d", ErrInvalidPending, item.Priority)
	case item.NotBefore.IsZero():
		return fmt.Errorf("%w: NotBefore is required", ErrInvalidPending)
	}
	return nil
}

// interruptionFingerprint identifies the facts of one Attempt. Two records for
// the same AttemptID must agree on all of them; a disagreement means the
// upstream state is inconsistent, so the pass fails closed instead of choosing
// one record.
func interruptionFingerprint(item Interruption) string {
	deadline := item.Contract.Deadline.UTC()
	return fmt.Sprintf(
		"%s|%s|%d|%t|%d|%d|%d|%d|%d",
		item.Contract.TaskID, item.Contract.TaskIRDigest.String(),
		item.AttemptNumber, item.Contract.Retryable, item.Contract.MaxAttempts,
		deadline.UnixNano(), item.Priority,
		item.EstimatedTransferBytes, item.EstimatedComputeMillis,
	)
}

// pendingFingerprint identifies one queued retry. The fingerprint is used only
// to tell an exact duplicate delivery from two inconsistent records for the
// same Task; it is not an authority value.
func pendingFingerprint(item Pending) string {
	return fmt.Sprintf(
		"%s|%s|%s|%d|%d|%d|%d|%d|%d|%d|%d",
		item.TaskID, item.TaskIRDigest.String(), item.PreviousAttemptID,
		item.PreviousAttemptNumber, item.NextAttemptNumber, item.MaxAttempts,
		item.Priority, item.Deadline.UnixNano(), item.NotBefore.UnixNano(),
		item.EstimatedTransferBytes, item.EstimatedComputeMillis,
	)
}

// backoff returns the full-jitter delay for the next Attempt. The value stays
// in [0, cap) and depends only on the Task and attempt number, so the same
// interruption produces the same NotBefore after a restart. A production
// deployment may replace the jitter source, but must keep it deterministic per
// persisted Pending.
func backoff(policy Policy, taskID string, attempt uint32) time.Duration {
	limit := backoffCap(policy, attempt)
	if limit <= 0 {
		return 0
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("eki.recovery-jitter/v1\x00%s\x00%d", taskID, attempt)))
	return time.Duration(binary.BigEndian.Uint64(digest[:8]) % uint64(limit))
}

func backoffCap(policy Policy, attempt uint32) time.Duration {
	if attempt <= 1 {
		return policy.BaseBackoff
	}
	d := policy.BaseBackoff
	for i := uint32(1); i < attempt; i++ {
		if d >= policy.MaxBackoff/2 {
			return policy.MaxBackoff
		}
		d *= 2
		if d >= policy.MaxBackoff {
			return policy.MaxBackoff
		}
	}
	return d
}
