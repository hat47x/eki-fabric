package statestore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

var (
	// ErrInvalidAdmission is returned when an Admission is malformed. It is a
	// programming error in the caller, not a Client mistake: the admission
	// service validates Client input before building an Admission.
	ErrInvalidAdmission = errors.New("invalid task admission")
	// ErrIdempotencyConflict is returned when the same principal reuses an
	// idempotency key with a different request fingerprint.
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
	// ErrQuotaExceeded is returned when a principal already has the maximum
	// number of outstanding (non-terminal) tasks.
	ErrQuotaExceeded = errors.New("principal outstanding task quota exceeded")
)

const (
	MaxPrincipalBytes      = 256
	MinIdempotencyKeyBytes = 8
	MaxIdempotencyKeyBytes = 128
	MaxAdmissionAttempts   = 32
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// Admission is the durable record that binds an admitted Task to the
// principal that submitted it and to the request that produced it.
type Admission struct {
	Task taskstate.TaskRecord
	// Principal is the authenticated submitter. It scopes idempotency keys and
	// decides who may observe or cancel the Task.
	Principal string
	// IdempotencyKey is the Client-supplied key, scoped to Principal.
	IdempotencyKey string
	// RequestFingerprint is the digest of the normalized request. The same key
	// with the same fingerprint converges on one Task; a different fingerprint
	// is a conflict.
	RequestFingerprint contract.Digest
	// MaxAttempts is the retry budget fixed at admission.
	MaxAttempts uint32
}

func (a Admission) Validate() error {
	if err := a.Task.Validate(); err != nil {
		return err
	}
	if n := len(a.Principal); n == 0 || n > MaxPrincipalBytes || !utf8.ValidString(a.Principal) {
		return fmt.Errorf("%w: principal must be 1..%d valid UTF-8 bytes", ErrInvalidAdmission, MaxPrincipalBytes)
	}
	if !ValidIdempotencyKey(a.IdempotencyKey) {
		return fmt.Errorf("%w: idempotency key must be %d..%d characters of [A-Za-z0-9._:-]", ErrInvalidAdmission, MinIdempotencyKeyBytes, MaxIdempotencyKeyBytes)
	}
	if a.RequestFingerprint.IsZero() {
		return fmt.Errorf("%w: request fingerprint is required", ErrInvalidAdmission)
	}
	if a.MaxAttempts < 1 || a.MaxAttempts > MaxAdmissionAttempts {
		return fmt.Errorf("%w: max attempts must be 1..%d", ErrInvalidAdmission, MaxAdmissionAttempts)
	}
	return nil
}

// AdmitLimit bounds what one admission may add. It is enforced inside the same
// transaction as the insert, so concurrent submissions cannot overshoot it.
type AdmitLimit struct {
	// MaxOutstandingTasks caps a principal's non-terminal tasks. Zero means no
	// cap; production configuration must set one (RQ-017).
	MaxOutstandingTasks uint32
}

type AdmitResult struct {
	// Admission is the stored record. On a replay it is the original record
	// with the Task's current state, not the Admission the caller passed in.
	Admission Admission
	// Replayed is true when the idempotency key had already been admitted with
	// the same fingerprint, so no new Task was created.
	Replayed bool
}

// AdmissionStore records Task admissions atomically with their idempotency
// and quota checks.
type AdmissionStore interface {
	// AdmitTask creates the Task and its Admission, or returns the existing
	// result for a repeated (principal, key, fingerprint). It returns
	// ErrIdempotencyConflict for the same key with a different fingerprint and
	// ErrQuotaExceeded when limit would be exceeded. A replay is never
	// rejected for quota.
	AdmitTask(ctx context.Context, admission Admission, limit AdmitLimit) (AdmitResult, error)
	// LoadAdmission returns the Admission for a Task with the Task's current
	// state, or ErrNotFound.
	LoadAdmission(ctx context.Context, id ids.TaskID) (Admission, error)
}

// Store is a durable-capable store of Tasks and their admissions.
type Store interface {
	TaskStore
	AdmissionStore
	LeaseStore
}

// ValidIdempotencyKey reports whether key satisfies the P0 Idempotency-Key
// constraints. Callers check it before building an Admission so a malformed
// key is a Client error, not a storage error.
func ValidIdempotencyKey(key string) bool {
	return len(key) >= MinIdempotencyKeyBytes &&
		len(key) <= MaxIdempotencyKeyBytes &&
		idempotencyKeyPattern.MatchString(key)
}
