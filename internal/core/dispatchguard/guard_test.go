package dispatchguard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/spi/dispatchlog"
)

const testEffect dispatchlog.EffectID = "notify.mail:1"

var errStorage = errors.New("storage unavailable")

// fakeLog is a dispatchlog.Log whose RecordAttempt returns recordErr. If cancel
// is set, RecordAttempt runs it first, so a test can end the context while the
// record is being written. It records the order of events in events.
type fakeLog struct {
	recordErr error
	cancel    context.CancelFunc
	calls     int
	events    []string
}

func (f *fakeLog) RecordAttempt(_ context.Context, _ dispatchlog.EffectID, _ uint64) error {
	f.calls++
	f.events = append(f.events, "record")
	if f.cancel != nil {
		f.cancel()
	}
	return f.recordErr
}

func (f *fakeLog) Latest(_ context.Context, _ dispatchlog.EffectID) (uint64, bool, error) {
	return 0, false, nil
}

func newGuard(t *testing.T, log dispatchlog.Log) Guard {
	t.Helper()
	g, err := New(log)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return g
}

func TestSendRecordsAttemptBeforeCallingSend(t *testing.T) {
	log := &fakeLog{}
	g := newGuard(t, log)

	err := g.Send(context.Background(), testEffect, 1, func(context.Context) error {
		log.events = append(log.events, "send")
		return nil
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	want := []string{"record", "send"}
	if !slices.Equal(log.events, want) {
		t.Fatalf("event order = %v, want %v", log.events, want)
	}
}

func TestSendDoesNotCallSendWhenRecordFails(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "storage failure", err: errStorage},
		{name: "wrapped storage failure", err: fmt.Errorf("commit dispatch record: %w", errStorage)},
		{name: "already recorded", err: dispatchlog.ErrAlreadyRecorded},
		{name: "not increasing", err: dispatchlog.ErrNotIncreasing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &fakeLog{recordErr: tc.err}
			g := newGuard(t, log)
			sends := 0

			err := g.Send(context.Background(), testEffect, 2, func(context.Context) error {
				sends++
				return nil
			})
			if err != tc.err {
				t.Fatalf("Send() error = %v, want the record error %v unchanged", err, tc.err)
			}
			if sends != 0 {
				t.Fatalf("send was called %d times after a failed record, want 0", sends)
			}
			if log.calls != 1 {
				t.Fatalf("RecordAttempt calls = %d, want 1 (no retry of the record)", log.calls)
			}
		})
	}
}

func TestSendCallsSendOnceAndReturnsItsErrorUnchanged(t *testing.T) {
	sendErr := errors.New("external system rejected the call")
	log := &fakeLog{}
	g := newGuard(t, log)
	sends := 0

	err := g.Send(context.Background(), testEffect, 3, func(context.Context) error {
		sends++
		return sendErr
	})
	if err != sendErr {
		t.Fatalf("Send() error = %v, want send's error %v unchanged", err, sendErr)
	}
	if sends != 1 {
		t.Fatalf("send was called %d times, want exactly 1 (no retry)", sends)
	}
	if log.calls != 1 {
		t.Fatalf("RecordAttempt calls = %d, want 1", log.calls)
	}
}

func TestSendReturnsNilWhenSendSucceeds(t *testing.T) {
	g := newGuard(t, &fakeLog{})
	sends := 0

	err := g.Send(context.Background(), testEffect, 1, func(context.Context) error {
		sends++
		return nil
	})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	if sends != 1 {
		t.Fatalf("send was called %d times, want 1", sends)
	}
}

func TestSendPassesContextToSend(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "marker")
	g := newGuard(t, &fakeLog{})

	err := g.Send(ctx, testEffect, 1, func(got context.Context) error {
		if got.Value(key{}) != "marker" {
			return errors.New("send received a context without the caller's value")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestInvalidInputIsRejectedBeforeRecording(t *testing.T) {
	cases := []struct {
		name       string
		effect     dispatchlog.EffectID
		generation uint64
		want       error
	}{
		{name: "empty effect", effect: "", generation: 1, want: dispatchlog.ErrInvalidEffectID},
		{name: "effect with space", effect: "bad id", generation: 1, want: dispatchlog.ErrInvalidEffectID},
		{
			name:       "effect too long",
			effect:     dispatchlog.EffectID(strings.Repeat("e", dispatchlog.MaxEffectIDLength+1)),
			generation: 1,
			want:       dispatchlog.ErrInvalidEffectID,
		},
		{name: "zero generation", effect: testEffect, generation: 0, want: dispatchlog.ErrInvalidGeneration},
		{name: "generation above maximum", effect: testEffect, generation: dispatchlog.MaxGeneration + 1, want: dispatchlog.ErrInvalidGeneration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &fakeLog{}
			g := newGuard(t, log)
			sends := 0

			err := g.Send(context.Background(), tc.effect, tc.generation, func(context.Context) error {
				sends++
				return nil
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Send() error = %v, want %v", err, tc.want)
			}
			if log.calls != 0 {
				t.Fatalf("RecordAttempt calls = %d, want 0", log.calls)
			}
			if sends != 0 {
				t.Fatalf("send was called %d times, want 0", sends)
			}
		})
	}
}

func TestNilSendIsRejectedBeforeRecording(t *testing.T) {
	log := &fakeLog{}
	g := newGuard(t, log)

	err := g.Send(context.Background(), testEffect, 1, nil)
	if !errors.Is(err, ErrNilSend) {
		t.Fatalf("Send(nil) error = %v, want ErrNilSend", err)
	}
	if log.calls != 0 {
		t.Fatalf("RecordAttempt calls = %d, want 0 for an uncallable send", log.calls)
	}
}

func TestNilLogIsRejected(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrNilLog) {
		t.Fatalf("New(nil) error = %v, want ErrNilLog", err)
	}

	var g Guard
	sends := 0
	err := g.Send(context.Background(), testEffect, 1, func(context.Context) error {
		sends++
		return nil
	})
	if !errors.Is(err, ErrNilLog) {
		t.Fatalf("zero Guard Send() error = %v, want ErrNilLog", err)
	}
	if sends != 0 {
		t.Fatalf("send was called %d times through a zero Guard, want 0", sends)
	}
}

func TestNilContextIsRejected(t *testing.T) {
	log := &fakeLog{}
	g := newGuard(t, log)
	var ctx context.Context
	sends := 0

	err := g.Send(ctx, testEffect, 1, func(context.Context) error {
		sends++
		return nil
	})
	if !errors.Is(err, ErrNilContext) {
		t.Fatalf("Send(nil ctx) error = %v, want ErrNilContext", err)
	}
	if log.calls != 0 || sends != 0 {
		t.Fatalf("RecordAttempt calls = %d, send calls = %d, want 0 and 0", log.calls, sends)
	}
}

func TestContextCancelledBeforeRecordDoesNotRecordOrSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	log := &fakeLog{}
	g := newGuard(t, log)
	sends := 0

	err := g.Send(ctx, testEffect, 1, func(context.Context) error {
		sends++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() error = %v, want context.Canceled", err)
	}
	if log.calls != 0 || sends != 0 {
		t.Fatalf("RecordAttempt calls = %d, send calls = %d, want 0 and 0", log.calls, sends)
	}
}

func TestContextCancelledDuringRecordDoesNotSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The record succeeds, but the context ends while it is being written.
	log := &fakeLog{cancel: cancel}
	g := newGuard(t, log)
	sends := 0

	err := g.Send(ctx, testEffect, 1, func(context.Context) error {
		sends++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() error = %v, want context.Canceled", err)
	}
	if sends != 0 {
		t.Fatalf("send was called %d times after cancellation, want 0", sends)
	}
	if log.calls != 1 {
		t.Fatalf("RecordAttempt calls = %d, want 1", log.calls)
	}
}
