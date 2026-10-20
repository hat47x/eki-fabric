// Package replayguard defines the durable claim of a one-time nonce.
//
// A WorkOrder carries a nonce that a Worker must spend exactly once. The guard
// is the boundary that answers "has this nonce already been used?" without
// trusting the caller's memory of previous decisions. A guard is not execution
// authority: claiming a nonce only records that the Worker has seen this
// instruction before.
package replayguard

import (
	"context"
	"time"
)

// Guard claims a nonce for at most until.
//
// Claim returns true only when the nonce was unused and is now owned by the
// caller. It returns false when the nonce is already claimed and still live.
// An implementation must be safe for concurrent callers.
//
// Errors mean that the answer is unknown or that the guard is full. Callers must
// fail closed: they must not treat an error as permission to proceed.
type Guard interface {
	Claim(ctx context.Context, nonce []byte, until time.Time) (bool, error)
}
