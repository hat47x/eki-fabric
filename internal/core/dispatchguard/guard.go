// Package dispatchguard sends an external call only after its dispatch attempt
// is durably recorded. This is the commit-before-dispatch rule. Send records the
// attempt through a dispatchlog.Log and calls the send function only when the
// record succeeded. When recording fails, the call is never made.
//
// Send calls the send function at most once and never retries it. If the
// context ends after the record is committed, Send returns the context error
// without calling the send function. The record then exists with no call. The
// retry rule treats that as a dispatch whose result is unknown. It can hold an
// effect, but by itself it cannot cause a duplicate.
//
// The guard covers only calls made inside Send. It cannot stop other code from
// calling the external system, and it cannot detect a Log that reports success
// without durable storage. In that case a crash can erase a record whose call
// was already sent. Latest would then report no dispatch, and a non-idempotent
// call could be repeated.
package dispatchguard

import (
	"context"
	"errors"

	"github.com/hat47x/eki-fabric/internal/spi/dispatchlog"
)

// ErrNilLog is returned when a Guard has no dispatch log.
var ErrNilLog = errors.New("dispatch guard: nil dispatch log")

// ErrNilSend is returned when Send is called without a send function.
var ErrNilSend = errors.New("dispatch guard: nil send function")

// ErrNilContext is returned when Send is called without a context.
var ErrNilContext = errors.New("dispatch guard: nil context")

// Guard sends external calls only after their dispatch attempt is recorded. The
// zero Guard has no log, so every Send on it fails with ErrNilLog and sends
// nothing.
type Guard struct {
	log dispatchlog.Log
}

// New returns a Guard that records attempts in log. A nil log is rejected.
func New(log dispatchlog.Log) (Guard, error) {
	if log == nil {
		return Guard{}, ErrNilLog
	}
	return Guard{log: log}, nil
}

// Send records a dispatch attempt for generation of effect, and then calls send.
//
// The inputs and the context are checked before anything is recorded. If the
// record fails, Send returns that error unchanged and does not call send. If the
// record succeeds, Send calls send once, passes it ctx, and returns send's error
// unchanged. Send does not retry send.
func (g Guard) Send(ctx context.Context, effect dispatchlog.EffectID, generation uint64, send func(context.Context) error) error {
	if g.log == nil {
		return ErrNilLog
	}
	if send == nil {
		return ErrNilSend
	}
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := effect.Validate(); err != nil {
		return err
	}
	if err := dispatchlog.ValidateGeneration(generation); err != nil {
		return err
	}

	if err := g.log.RecordAttempt(ctx, effect, generation); err != nil {
		return err
	}
	// The record is committed now. If the context ended while it was being
	// written, stop before the call leaves the process.
	if err := ctx.Err(); err != nil {
		return err
	}
	return send(ctx)
}
