// Package externaleffecttest is the contract that every External Effect Adapter
// must pass. An adapter sends a call that changes state outside the system.
//
// The contract is the commit-before-dispatch rule in
// plan/adr/0007-provisional-side-effect-retry-policy.md, section 4. Before the
// call leaves the process, the adapter records its dispatch attempt durably. The
// call runs at most once per Send, and it never runs when the record is missing,
// when the record's outcome is unknown, or when the context has ended.
//
// The suite checks the order from inside the call, where the recorded state is
// observable. It injects faults around the record to check the failure paths.
//
// Sender matches dispatchguard.Guard.Send, the reference implementation. The
// interface an adapter must expose is not decided yet, so Sender is provisional.
package externaleffecttest

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/spi/dispatchlog"
)

// Sender is the operation an External Effect Adapter exposes to its caller. It
// records the attempt through its dispatch log, and then calls send.
type Sender interface {
	Send(ctx context.Context, effect dispatchlog.EffectID, generation uint64, send func(context.Context) error) error
}

// Subject tells the suite how to build the adapter under test.
type Subject struct {
	// NewLog returns a fresh, empty dispatch log for one subtest.
	NewLog func(t testing.TB) dispatchlog.Log
	// NewSender returns a Sender that records its attempts through log.
	NewSender func(t testing.TB, log dispatchlog.Log) Sender
}

var (
	errInjected = errors.New("externaleffecttest: injected fault")
	errCall     = errors.New("externaleffecttest: external call failed")
)

// RunCommitBeforeDispatch checks the commit-before-dispatch rule. Each subtest
// uses a fresh log, and a fault wrapper sits between the sender and that log.
func RunCommitBeforeDispatch(t *testing.T, s Subject) {
	t.Helper()
	if s.NewLog == nil || s.NewSender == nil {
		t.Fatal("externaleffecttest: Subject needs NewLog and NewSender")
	}

	setup := func(t *testing.T) (Sender, *faultLog) {
		t.Helper()
		fl := &faultLog{inner: s.NewLog(t)}
		return s.NewSender(t, fl), fl
	}

	t.Run("call-sees-its-record", func(t *testing.T) {
		sender, fl := setup(t)
		effect := mustEffect(t, "record-first")
		calls := 0

		err := sender.Send(context.Background(), effect, 1, func(context.Context) error {
			calls++
			// The record must already be visible when the call starts. Read it from
			// the underlying log, which the fault wrapper does not intercept.
			assertLatestInCall(t, fl.inner, effect, 1)
			return nil
		})
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		expectCalls(t, calls, 1)
	})

	t.Run("failed-record-sends-nothing", func(t *testing.T) {
		sender, fl := setup(t)
		effect := mustEffect(t, "record-failed")
		fl.failBefore = errInjected
		calls := 0

		err := sender.Send(context.Background(), effect, 1, countingCall(&calls))
		expectError(t, err, errInjected)
		expectCalls(t, calls, 0)
		assertLatest(t, fl.inner, effect, 0, false)
	})

	t.Run("lost-acknowledgement-sends-nothing", func(t *testing.T) {
		sender, fl := setup(t)
		effect := mustEffect(t, "record-unacknowledged")
		// The store commits the record and then loses the acknowledgement. The
		// sender cannot tell this from a failed write, so it must not send.
		fl.lostAck = errInjected
		calls := 0

		err := sender.Send(context.Background(), effect, 1, countingCall(&calls))
		expectError(t, err, errInjected)
		expectCalls(t, calls, 0)
		assertLatest(t, fl.inner, effect, 1, true)
	})

	t.Run("cancelled-before-record-sends-nothing", func(t *testing.T) {
		sender, fl := setup(t)
		effect := mustEffect(t, "cancelled-early")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0

		err := sender.Send(ctx, effect, 1, countingCall(&calls))
		expectError(t, err, context.Canceled)
		expectCalls(t, calls, 0)
		assertLatest(t, fl.inner, effect, 0, false)
	})

	t.Run("cancelled-after-record-sends-nothing", func(t *testing.T) {
		sender, fl := setup(t)
		effect := mustEffect(t, "cancelled-late")
		ctx, cancel := context.WithCancel(context.Background())
		// The record commits, and then the context ends before the call starts.
		// The record stays, with no call behind it. The retry rule reads that as
		// an unknown outcome, so the call must not start here.
		fl.afterRecord = cancel
		calls := 0

		err := sender.Send(ctx, effect, 1, countingCall(&calls))
		expectError(t, err, context.Canceled)
		expectCalls(t, calls, 0)
		assertLatest(t, fl.inner, effect, 1, true)
	})

	t.Run("call-error-is-returned-and-not-retried", func(t *testing.T) {
		sender, fl := setup(t)
		effect := mustEffect(t, "call-failed")
		calls := 0

		err := sender.Send(context.Background(), effect, 1, func(context.Context) error {
			calls++
			return errCall
		})
		expectError(t, err, errCall)
		expectCalls(t, calls, 1)
		assertLatest(t, fl.inner, effect, 1, true)
	})

	t.Run("repeated-generation-sends-nothing", func(t *testing.T) {
		sender, _ := setup(t)
		effect := mustEffect(t, "repeated")
		calls := 0

		if err := sender.Send(context.Background(), effect, 2, countingCall(&calls)); err != nil {
			t.Fatalf("first Send() error = %v", err)
		}
		err := sender.Send(context.Background(), effect, 2, countingCall(&calls))
		expectError(t, err, dispatchlog.ErrAlreadyRecorded)
		expectCalls(t, calls, 1)
	})

	t.Run("lower-generation-sends-nothing", func(t *testing.T) {
		sender, _ := setup(t)
		effect := mustEffect(t, "lower")
		calls := 0

		if err := sender.Send(context.Background(), effect, 5, countingCall(&calls)); err != nil {
			t.Fatalf("first Send() error = %v", err)
		}
		err := sender.Send(context.Background(), effect, 3, countingCall(&calls))
		expectError(t, err, dispatchlog.ErrNotIncreasing)
		expectCalls(t, calls, 1)
	})

	t.Run("invalid-input-sends-nothing", func(t *testing.T) {
		sender, fl := setup(t)
		calls := 0

		err := sender.Send(context.Background(), dispatchlog.EffectID(""), 1, countingCall(&calls))
		expectError(t, err, dispatchlog.ErrInvalidEffectID)
		expectCalls(t, calls, 0)

		effect := mustEffect(t, "zero-generation")
		err = sender.Send(context.Background(), effect, 0, countingCall(&calls))
		expectError(t, err, dispatchlog.ErrInvalidGeneration)
		expectCalls(t, calls, 0)
		assertLatest(t, fl.inner, effect, 0, false)
	})
}

// faultLog wraps a dispatch log and injects faults around RecordAttempt. Latest
// is passed through unchanged, so the suite can read the true state.
type faultLog struct {
	inner dispatchlog.Log
	// failBefore makes RecordAttempt fail without recording anything.
	failBefore error
	// lostAck records the attempt, and then returns this error.
	lostAck error
	// afterRecord runs after a successful record, before RecordAttempt returns.
	afterRecord func()
}

func (f *faultLog) RecordAttempt(ctx context.Context, effect dispatchlog.EffectID, generation uint64) error {
	if f.failBefore != nil {
		return f.failBefore
	}
	if err := f.inner.RecordAttempt(ctx, effect, generation); err != nil {
		return err
	}
	if f.afterRecord != nil {
		f.afterRecord()
	}
	return f.lostAck
}

func (f *faultLog) Latest(ctx context.Context, effect dispatchlog.EffectID) (uint64, bool, error) {
	return f.inner.Latest(ctx, effect)
}

func countingCall(calls *int) func(context.Context) error {
	return func(context.Context) error {
		*calls++
		return nil
	}
}

func mustEffect(t testing.TB, raw string) dispatchlog.EffectID {
	t.Helper()
	id, err := dispatchlog.ParseEffectID(raw)
	if err != nil {
		t.Fatalf("ParseEffectID(%q) error = %v", raw, err)
	}
	return id
}

func assertLatest(t testing.TB, log dispatchlog.Log, effect dispatchlog.EffectID, want uint64, wantOK bool) {
	t.Helper()
	got, ok, err := log.Latest(context.Background(), effect)
	if err != nil {
		t.Fatalf("Latest(%s) error = %v", effect, err)
	}
	if got != want || ok != wantOK {
		t.Fatalf("Latest(%s) = (%d, %t), want (%d, %t)", effect, got, ok, want, wantOK)
	}
}

// assertLatestInCall runs inside the call, so it reports with Errorf and does
// not stop the call.
func assertLatestInCall(t testing.TB, log dispatchlog.Log, effect dispatchlog.EffectID, want uint64) {
	t.Helper()
	got, ok, err := log.Latest(context.Background(), effect)
	if err != nil {
		t.Errorf("record not readable when the call started: %v", err)
		return
	}
	if !ok || got != want {
		t.Errorf("record when the call started = (%d, %t), want (%d, true): the call ran before its attempt was recorded", got, ok, want)
	}
}

func expectCalls(t testing.TB, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("external call ran %d times, want %d", got, want)
	}
}

func expectError(t testing.TB, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("Send() error = %v, want %v", err, want)
	}
}
