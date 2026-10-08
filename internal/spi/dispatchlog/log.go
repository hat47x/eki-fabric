// Package dispatchlog is the storage port for dispatch attempt records.
//
// Before a call that changes state outside the system is sent, the caller
// records a dispatch attempt for that effect and generation, and waits until
// the record is durable. A later reader that finds no record for a generation
// can conclude that no call for that generation was sent. The retry rule for
// non-idempotent effects in plan/adr/0007-provisional-side-effect-retry-policy.md
// depends on this property.
//
// Generations are chosen by the caller and must strictly increase per effect.
// Gaps are allowed. Recording a generation equal to the latest recorded one
// returns ErrAlreadyRecorded. Recording a lower generation returns
// ErrNotIncreasing. So an attempt for one generation is recorded at most once.
//
// An error from RecordAttempt does not say whether the record was written. The
// write may have committed while its acknowledgement was lost. The caller must
// not send the call in that case, and when it reconciles later it must read
// Latest rather than rely on its own memory.
package dispatchlog

import (
	"context"
	"errors"
)

// ErrNotIncreasing is returned when a generation is lower than the latest
// recorded generation for the same effect.
var ErrNotIncreasing = errors.New("dispatch generation is not greater than the latest recorded generation")

// ErrAlreadyRecorded is returned when a generation equal to the latest recorded
// generation for the same effect is recorded again.
var ErrAlreadyRecorded = errors.New("dispatch generation is already recorded")

// ErrInvalidEffectID is returned for an EffectID that is empty, too long, or
// contains a character outside the allowed set.
var ErrInvalidEffectID = errors.New("invalid effect ID")

// ErrInvalidGeneration is returned for generation zero, or for a generation
// above MaxGeneration.
var ErrInvalidGeneration = errors.New("invalid dispatch generation")

// Log is the durable record of dispatch attempts. Implementations must be safe
// for concurrent use by multiple goroutines.
type Log interface {
	// RecordAttempt durably records that a dispatch attempt for generation of
	// effect is about to happen. It returns nil only after the record is durable.
	// The caller must not send the call unless it receives nil.
	RecordAttempt(ctx context.Context, effect EffectID, generation uint64) error

	// Latest returns the highest generation recorded for effect. ok is false when
	// no attempt has been recorded for effect.
	Latest(ctx context.Context, effect EffectID) (generation uint64, ok bool, err error)
}
