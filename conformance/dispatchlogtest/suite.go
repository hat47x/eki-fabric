// Package dispatchlogtest is the contract that every dispatchlog.Log
// implementation must pass. Each adapter calls RunContract from its tests. An
// adapter that persists records also calls RunDurabilityContract.
package dispatchlogtest

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hat47x/eki-fabric/internal/spi/dispatchlog"
)

// Factory returns a new, empty Log for one subtest. Any cleanup the adapter
// needs is registered by the factory itself.
type Factory func(t testing.TB) dispatchlog.Log

// DurableFactory opens the Log stored at location. After closeLog is called,
// opening the same location again must show every record that was committed
// before the close.
type DurableFactory struct {
	Open func(t testing.TB, location string) (log dispatchlog.Log, closeLog func() error)
}

// RunContract checks the behavior that every Log must have. Each subtest uses a
// fresh Log.
func RunContract(t *testing.T, newLog Factory) {
	t.Helper()

	t.Run("first-attempt-records", func(t *testing.T) {
		log := newLog(t)
		effect := mustEffect(t, "first-attempt")

		mustRecord(t, log, effect, 1)
		assertLatest(t, log, effect, 1, true)
	})

	t.Run("no-attempt-reports-absent", func(t *testing.T) {
		log := newLog(t)
		assertLatest(t, log, mustEffect(t, "never-recorded"), 0, false)
	})

	t.Run("repeated-generation-is-already-recorded", func(t *testing.T) {
		ctx := context.Background()
		log := newLog(t)
		effect := mustEffect(t, "repeated")

		mustRecord(t, log, effect, 4)
		expectError(t, log.RecordAttempt(ctx, effect, 4), dispatchlog.ErrAlreadyRecorded)
		assertLatest(t, log, effect, 4, true)
	})

	t.Run("lower-generation-is-not-increasing", func(t *testing.T) {
		ctx := context.Background()
		log := newLog(t)
		effect := mustEffect(t, "lower")

		mustRecord(t, log, effect, 3)
		expectError(t, log.RecordAttempt(ctx, effect, 2), dispatchlog.ErrNotIncreasing)
		assertLatest(t, log, effect, 3, true)
	})

	t.Run("gaps-allowed-and-latest-follows-last-record", func(t *testing.T) {
		log := newLog(t)
		effect := mustEffect(t, "gaps")

		mustRecord(t, log, effect, 1)
		mustRecord(t, log, effect, 5)
		mustRecord(t, log, effect, 9)
		assertLatest(t, log, effect, 9, true)
	})

	t.Run("rejected-records-leave-latest-unchanged", func(t *testing.T) {
		ctx := context.Background()
		log := newLog(t)
		effect := mustEffect(t, "rejected")

		mustRecord(t, log, effect, 5)
		expectError(t, log.RecordAttempt(ctx, effect, 5), dispatchlog.ErrAlreadyRecorded)
		expectError(t, log.RecordAttempt(ctx, effect, 3), dispatchlog.ErrNotIncreasing)
		assertLatest(t, log, effect, 5, true)

		mustRecord(t, log, effect, 6)
		assertLatest(t, log, effect, 6, true)
	})

	t.Run("effects-are-independent", func(t *testing.T) {
		log := newLog(t)
		a := mustEffect(t, "effect-a")
		b := mustEffect(t, "effect-b")

		mustRecord(t, log, a, 7)
		mustRecord(t, log, b, 1)
		assertLatest(t, log, a, 7, true)
		assertLatest(t, log, b, 1, true)
	})

	t.Run("invalid-effect-id-is-rejected", func(t *testing.T) {
		ctx := context.Background()
		log := newLog(t)

		for _, raw := range invalidEffectIDs() {
			id := dispatchlog.EffectID(raw)
			expectError(t, log.RecordAttempt(ctx, id, 1), dispatchlog.ErrInvalidEffectID)
			_, _, err := log.Latest(ctx, id)
			expectError(t, err, dispatchlog.ErrInvalidEffectID)
		}
	})

	t.Run("boundary-effect-ids-are-accepted", func(t *testing.T) {
		log := newLog(t)

		for _, raw := range validEffectIDs() {
			mustRecord(t, log, mustEffect(t, raw), 1)
		}
	})

	t.Run("invalid-generation-is-rejected", func(t *testing.T) {
		ctx := context.Background()
		log := newLog(t)
		effect := mustEffect(t, "generation-bounds")

		expectError(t, log.RecordAttempt(ctx, effect, 0), dispatchlog.ErrInvalidGeneration)
		expectError(t, log.RecordAttempt(ctx, effect, dispatchlog.MaxGeneration+1), dispatchlog.ErrInvalidGeneration)
		assertLatest(t, log, effect, 0, false)

		mustRecord(t, log, effect, dispatchlog.MaxGeneration)
		assertLatest(t, log, effect, dispatchlog.MaxGeneration, true)
	})

	t.Run("cancelled-context-is-respected", func(t *testing.T) {
		log := newLog(t)
		effect := mustEffect(t, "cancelled")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		expectError(t, log.RecordAttempt(ctx, effect, 1), context.Canceled)
		_, _, err := log.Latest(ctx, effect)
		expectError(t, err, context.Canceled)
		assertLatest(t, log, effect, 0, false)
	})

	t.Run("concurrent-same-generation-has-one-winner", func(t *testing.T) {
		log := newLog(t)
		effect := mustEffect(t, "concurrent")
		const racers = 16

		start := make(chan struct{})
		results := make(chan error, racers)
		var wg sync.WaitGroup
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results <- log.RecordAttempt(context.Background(), effect, 1)
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		winners := 0
		for err := range results {
			switch {
			case err == nil:
				winners++
			case errors.Is(err, dispatchlog.ErrAlreadyRecorded):
			default:
				t.Errorf("RecordAttempt error = %v, want nil or ErrAlreadyRecorded", err)
			}
		}
		if winners != 1 {
			t.Fatalf("winners = %d, want exactly 1", winners)
		}
		assertLatest(t, log, effect, 1, true)
	})
}

// RunDurabilityContract checks that committed records survive a close and a
// reopen of the same location.
func RunDurabilityContract(t *testing.T, factory DurableFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("DurableFactory.Open is nil")
	}

	t.Run("records-survive-close-and-reopen", func(t *testing.T) {
		ctx := context.Background()
		location := filepath.Join(t.TempDir(), "dispatch.db")
		a := mustEffect(t, "durable-a")
		b := mustEffect(t, "durable-b")

		log, closeLog := factory.Open(t, location)
		mustRecord(t, log, a, 1)
		mustRecord(t, log, a, 4)
		mustRecord(t, log, b, 2)
		if err := closeLog(); err != nil {
			t.Fatalf("close first log: %v", err)
		}

		reopened, closeReopened := factory.Open(t, location)
		t.Cleanup(func() {
			if err := closeReopened(); err != nil {
				t.Errorf("close reopened log: %v", err)
			}
		})

		assertLatest(t, reopened, a, 4, true)
		assertLatest(t, reopened, b, 2, true)
		expectError(t, reopened.RecordAttempt(ctx, a, 4), dispatchlog.ErrAlreadyRecorded)
		expectError(t, reopened.RecordAttempt(ctx, a, 3), dispatchlog.ErrNotIncreasing)
		mustRecord(t, reopened, a, 5)
		assertLatest(t, reopened, a, 5, true)
	})
}

func invalidEffectIDs() []string {
	return []string{
		"",
		"has space",
		"slash/separated",
		"semi;colon",
		"new\nline",
		"caf~e",
		strings.Repeat("a", dispatchlog.MaxEffectIDLength+1),
	}
}

func validEffectIDs() []string {
	return []string{
		"a",
		"tsk.1_a:b-c",
		"ABC019",
		strings.Repeat("x", dispatchlog.MaxEffectIDLength),
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

func mustRecord(t testing.TB, log dispatchlog.Log, effect dispatchlog.EffectID, generation uint64) {
	t.Helper()
	if err := log.RecordAttempt(context.Background(), effect, generation); err != nil {
		t.Fatalf("RecordAttempt(%s, %d) error = %v", effect, generation, err)
	}
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

func expectError(t testing.TB, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
