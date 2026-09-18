package recovery

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"time"
)

type Priority int

const (
	PriorityBestEffort Priority = iota
	PriorityNormal
	PriorityUrgent
)

type InterruptedAttempt struct {
	TaskID             string
	Priority           Priority
	Deadline           time.Time
	Retryable          bool
	AttemptsUsed       int
	MaxAttempts        int
	EstimatedTransfer  uint64
	EstimatedComputeMs uint64
}

type RecoveryPolicy struct {
	BaseBackoff          time.Duration
	MaxBackoff           time.Duration
	MaxReleasesPerWindow int
	MaxTransferPerWindow uint64
	Window               time.Duration
}

type Pending struct {
	TaskID             string
	Priority           Priority
	Deadline           time.Time
	Attempt            int
	NotBefore          time.Time
	EstimatedTransfer  uint64
	EstimatedComputeMs uint64
}

type Hold struct {
	TaskID string
	Reason string
}

type Intake struct {
	Pending []Pending
	Held    []Hold
}

type WindowResult struct {
	Released      []Pending
	Deferred      []Pending
	TransferBytes uint64
}

func ValidatePolicy(p RecoveryPolicy) error {
	if p.BaseBackoff <= 0 {
		return errors.New("BaseBackoff must be positive")
	}
	if p.MaxBackoff < p.BaseBackoff {
		return errors.New("MaxBackoff must be >= BaseBackoff")
	}
	if p.MaxReleasesPerWindow <= 0 {
		return errors.New("MaxReleasesPerWindow must be positive")
	}
	if p.MaxTransferPerWindow == 0 {
		return errors.New("MaxTransferPerWindow must be positive")
	}
	if p.Window <= 0 {
		return errors.New("Window must be positive")
	}
	return nil
}

// deterministicFullJitter creates a reproducible value in [0, cap).
// Production may use a different jitter source; determinism is useful for the Spike/tests.
func deterministicFullJitter(taskID string, attempt int, cap time.Duration) time.Duration {
	if cap <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s:%d", taskID, attempt)
	return time.Duration(h.Sum64() % uint64(cap))
}

func backoffCap(base, max time.Duration, attempt int) time.Duration {
	if attempt <= 1 {
		return base
	}
	d := base
	for i := 1; i < attempt; i++ {
		if d >= max/2 {
			return max
		}
		d *= 2
		if d >= max {
			return max
		}
	}
	return d
}

// IntakeReclaim honors reclaim immediately: interrupted attempts stop owning capacity.
// Only tasks that are explicitly retryable and still inside their attempt budget enter recovery.
func IntakeReclaim(now time.Time, attempts []InterruptedAttempt, policy RecoveryPolicy) (Intake, error) {
	if err := ValidatePolicy(policy); err != nil {
		return Intake{}, err
	}
	out := Intake{}
	seen := map[string]struct{}{}

	for _, a := range attempts {
		if a.TaskID == "" {
			return Intake{}, errors.New("TaskID is required")
		}
		if _, ok := seen[a.TaskID]; ok {
			return Intake{}, fmt.Errorf("duplicate interrupted TaskID %q", a.TaskID)
		}
		seen[a.TaskID] = struct{}{}

		if !a.Retryable {
			out.Held = append(out.Held, Hold{TaskID: a.TaskID, Reason: "task is not safe for automatic retry"})
			continue
		}
		nextAttempt := a.AttemptsUsed + 1
		if a.MaxAttempts <= 0 || nextAttempt > a.MaxAttempts {
			out.Held = append(out.Held, Hold{TaskID: a.TaskID, Reason: "retry budget exhausted"})
			continue
		}

		cap := backoffCap(policy.BaseBackoff, policy.MaxBackoff, nextAttempt)
		jitter := deterministicFullJitter(a.TaskID, nextAttempt, cap)
		out.Pending = append(out.Pending, Pending{
			TaskID:             a.TaskID,
			Priority:           a.Priority,
			Deadline:           a.Deadline,
			Attempt:            nextAttempt,
			NotBefore:          now.Add(jitter),
			EstimatedTransfer:  a.EstimatedTransfer,
			EstimatedComputeMs: a.EstimatedComputeMs,
		})
	}
	return out, nil
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
	return a.TaskID < b.TaskID
}

// ReleaseWindow is the re-admission boundary. Backoff controls eligibility time;
// this function separately caps how much work and network transfer may re-enter per window.
func ReleaseWindow(now time.Time, pending []Pending, policy RecoveryPolicy) (WindowResult, error) {
	if err := ValidatePolicy(policy); err != nil {
		return WindowResult{}, err
	}

	ordered := append([]Pending(nil), pending...)
	sort.SliceStable(ordered, func(i, j int) bool { return lessPending(ordered[i], ordered[j]) })

	out := WindowResult{}
	for _, p := range ordered {
		if now.Before(p.NotBefore) {
			out.Deferred = append(out.Deferred, p)
			continue
		}
		if len(out.Released) >= policy.MaxReleasesPerWindow {
			out.Deferred = append(out.Deferred, p)
			continue
		}
		if p.EstimatedTransfer > policy.MaxTransferPerWindow {
			out.Deferred = append(out.Deferred, p)
			continue
		}
		if out.TransferBytes > policy.MaxTransferPerWindow-p.EstimatedTransfer {
			out.Deferred = append(out.Deferred, p)
			continue
		}
		out.Released = append(out.Released, p)
		out.TransferBytes += p.EstimatedTransfer
	}
	return out, nil
}

// NaiveImmediateRetry exists only to make amplification visible in the Spike.
func NaiveImmediateRetry(attempts []InterruptedAttempt) (retries int, transfer uint64) {
	for _, a := range attempts {
		if !a.Retryable {
			continue
		}
		retries++
		transfer += a.EstimatedTransfer
	}
	return retries, transfer
}
