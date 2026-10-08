// Package memory is an in-process dispatchlog.Log for tests and local runs.
//
// Records live only in process memory and are lost when the process exits. A
// restart between a recorded attempt and a later reconciliation then hides the
// dispatch, which breaks the durability that commit-before-dispatch relies on.
// This adapter therefore runs only the shared contract. It must not back a
// Control Plane that can restart while a call is in flight.
package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/hat47x/eki-fabric/internal/spi/dispatchlog"
)

// Log is an in-memory dispatchlog.Log. It is safe for concurrent use, and the
// zero value is ready to use.
type Log struct {
	mu     sync.Mutex
	latest map[dispatchlog.EffectID]uint64
}

var _ dispatchlog.Log = (*Log)(nil)

// New returns an empty Log.
func New() *Log {
	return &Log{latest: make(map[dispatchlog.EffectID]uint64)}
}

// RecordAttempt implements dispatchlog.Log.
func (l *Log) RecordAttempt(ctx context.Context, effect dispatchlog.EffectID, generation uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := effect.Validate(); err != nil {
		return err
	}
	if err := dispatchlog.ValidateGeneration(generation); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.latest == nil {
		l.latest = make(map[dispatchlog.EffectID]uint64)
	}
	current, ok := l.latest[effect]
	switch {
	case !ok || generation > current:
		l.latest[effect] = generation
		return nil
	case generation == current:
		return fmt.Errorf("%w: effect %s generation %d", dispatchlog.ErrAlreadyRecorded, effect, generation)
	default:
		return fmt.Errorf("%w: effect %s generation %d is below recorded %d", dispatchlog.ErrNotIncreasing, effect, generation, current)
	}
}

// Latest implements dispatchlog.Log.
func (l *Log) Latest(ctx context.Context, effect dispatchlog.EffectID) (uint64, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if err := effect.Validate(); err != nil {
		return 0, false, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	generation, ok := l.latest[effect]
	return generation, ok, nil
}
