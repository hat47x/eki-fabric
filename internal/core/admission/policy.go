package admission

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidPolicy = errors.New("invalid admission policy")

// Policy is the operator-controlled limit on what may be admitted. It comes
// from administrative configuration, never from a request.
type Policy struct {
	// ApprovedExecutors lists executor IDs that may be requested. Anything else
	// is refused before it can be queued. Executor version resolution belongs
	// to Executor Admission (EXE-1), which M0 does not have; the requested
	// version constraint is recorded in the Task IR but not resolved here.
	ApprovedExecutors []string

	// PerTask are hard ceilings for one Task's resource request.
	PerTask ResourceCeiling

	// MaxAttempts caps reliability.max_attempts.
	MaxAttempts uint32

	// MaxOutstandingTasks caps one principal's non-terminal Tasks (RQ-017).
	MaxOutstandingTasks uint32

	// MaxDeadlineHorizon bounds how far in the future a deadline may be. Zero
	// means no bound.
	MaxDeadlineHorizon time.Duration
}

// ResourceCeiling bounds one Task. A zero field is invalid: every dimension
// needs an explicit ceiling.
type ResourceCeiling struct {
	CPUMillis          int64
	MemoryBytes        int64
	WallTimeSeconds    int64
	OutputBytesMax     int64
	NetworkBytesMax    int64
	TemporaryDiskBytes int64
}

func (p Policy) validate() error {
	if len(p.ApprovedExecutors) == 0 {
		return fmt.Errorf("%w: at least one approved executor is required", ErrInvalidPolicy)
	}
	for _, id := range p.ApprovedExecutors {
		if !executorIDPattern.MatchString(id) {
			return fmt.Errorf("%w: approved executor %q is not a valid executor id", ErrInvalidPolicy, id)
		}
	}
	c := p.PerTask
	if c.CPUMillis <= 0 || c.MemoryBytes <= 0 || c.WallTimeSeconds <= 0 || c.OutputBytesMax <= 0 || c.NetworkBytesMax < 0 || c.TemporaryDiskBytes < 0 {
		return fmt.Errorf("%w: every per-task ceiling must be set", ErrInvalidPolicy)
	}
	if p.MaxAttempts < 1 || p.MaxAttempts > 32 {
		return fmt.Errorf("%w: max attempts must be between 1 and 32", ErrInvalidPolicy)
	}
	if p.MaxOutstandingTasks < 1 {
		return fmt.Errorf("%w: a per-principal outstanding task cap is required", ErrInvalidPolicy)
	}
	if p.MaxDeadlineHorizon < 0 {
		return fmt.Errorf("%w: negative deadline horizon", ErrInvalidPolicy)
	}
	return nil
}

// check applies policy to a validated request. It returns the first violation.
func (p Policy) check(r NormalizedRequest, now time.Time) *Error {
	if r.ExtensionCount > 0 {
		return unprocessable("EXTENSION_NOT_SUPPORTED", "EXTENSION", "no extension is registered")
	}

	approved := false
	for _, id := range p.ApprovedExecutors {
		if id == r.Executor.ID {
			approved = true
			break
		}
	}
	if !approved {
		return unprocessable("EXECUTOR_NOT_APPROVED", "EXECUTOR", "the requested executor is not approved")
	}

	// There is no Artifact Store yet, so no input can be authorized. Failing
	// closed is the only safe answer; an Artifact is never trusted by name.
	if len(r.Inputs) > 0 {
		return unprocessable("ARTIFACT_NOT_AVAILABLE", "ARTIFACT", "artifact inputs are not available in this deployment")
	}
	if r.Resources.GPURequested {
		return unprocessable("GPU_NOT_SUPPORTED", "RESOURCE", "gpu resources are not available in this deployment")
	}

	c := p.PerTask
	res := r.Resources
	for _, v := range []struct {
		name       string
		got, limit int64
	}{
		{"cpu_millis", res.CPUMillis, c.CPUMillis},
		{"memory_bytes", res.MemoryBytes, c.MemoryBytes},
		{"wall_time_seconds", res.WallTimeSeconds, c.WallTimeSeconds},
		{"output_bytes_max", res.OutputBytesMax, c.OutputBytesMax},
		{"network_bytes_max", res.NetworkBytesMax, c.NetworkBytesMax},
		{"temporary_disk_bytes", res.TemporaryDiskBytes, c.TemporaryDiskBytes},
	} {
		if v.got > v.limit {
			return unprocessable("RESOURCE_BUDGET_EXCEEDED", "RESOURCE", fmt.Sprintf("resources.%s exceeds the per-task limit", v.name))
		}
	}
	if r.Reliability.MaxAttempts > p.MaxAttempts {
		return unprocessable("RETRY_BUDGET_EXCEEDED", "RETRY", "reliability.max_attempts exceeds the policy limit")
	}

	if !r.Deadline.IsZero() {
		if !r.Deadline.After(now) {
			return unprocessable("DEADLINE_IN_PAST", "DEADLINE", "the deadline has already passed")
		}
		if p.MaxDeadlineHorizon > 0 && r.Deadline.After(now.Add(p.MaxDeadlineHorizon)) {
			return unprocessable("DEADLINE_TOO_FAR", "DEADLINE", "the deadline is beyond the policy horizon")
		}
	}
	return nil
}

// ValidatePolicy reports whether p is complete enough to admit work. New calls
// it too; it is exported so configuration can be checked before any resource is
// opened.
func ValidatePolicy(p Policy) error { return p.validate() }
