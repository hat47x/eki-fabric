// Package replayguard defines the durable claims of the Worker's acceptance
// boundary.
//
// A WorkOrder carries a nonce that a Worker must spend exactly once. The guard
// is the boundary that answers "has this nonce already been used?" without
// trusting the caller's memory of previous decisions. The same boundary
// answers "has a newer instruction for this Task already been accepted?": the
// per-Task fence epoch a Worker must not run behind (#296). A guard is not
// execution authority: a claim only records what the Worker has accepted.
package replayguard

import (
	"context"
	"time"
)

// Guard claims a nonce for at most until, and records the fence epoch of a
// Task.
//
// Claim returns true only when the nonce was unused and is now owned by the
// caller. It returns false when the nonce is already claimed and still live.
//
// Fence returns true only when the epoch is current for the Task: no higher
// epoch was recorded and still lives. It returns false when a higher epoch was
// already accepted, which means the delivered Attempt is superseded. The same
// epoch is accepted again, so a re-issued WorkOrder for the same Attempt
// passes.
//
// An implementation must be safe for concurrent callers. Errors mean that the
// answer is unknown or that the guard is full. Callers must fail closed: they
// must not treat an error as permission to proceed.
type Guard interface {
	Claim(ctx context.Context, nonce []byte, until time.Time) (bool, error)

	// Fence records epoch as accepted for taskID until the record expires, and
	// reports whether the instruction is still current. A stale epoch writes
	// nothing.
	Fence(ctx context.Context, taskID string, epoch uint64, until time.Time) (bool, error)
}
