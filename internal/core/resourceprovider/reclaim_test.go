package resourceprovider

import (
	"errors"
	"math"
	"testing"
	"time"
)

// TestReclaimLatency covers each case of the latency function: a reclaim after
// expiry, a reclaim at the same instant, a reclaim before expiry, and the overflow
// boundary.
func TestReclaimLatency(t *testing.T) {
	expired := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

	t.Run("reclaim after expiry returns the difference", func(t *testing.T) {
		got, err := ReclaimLatency(expired, expired.Add(1500*time.Millisecond))
		if err != nil {
			t.Fatalf("ReclaimLatency() error = %v", err)
		}
		if got != 1500*time.Millisecond {
			t.Fatalf("ReclaimLatency() = %v, want 1.5s", got)
		}
	})

	t.Run("reclaim at the expiry instant has zero latency", func(t *testing.T) {
		got, err := ReclaimLatency(expired, expired)
		if err != nil {
			t.Fatalf("ReclaimLatency() error = %v", err)
		}
		if got != 0 {
			t.Fatalf("ReclaimLatency() = %v, want 0", got)
		}
	})

	t.Run("reclaim one nanosecond before expiry is refused", func(t *testing.T) {
		got, err := ReclaimLatency(expired, expired.Add(-time.Nanosecond))
		if !errors.Is(err, ErrReclaimBeforeExpiry) {
			t.Fatalf("ReclaimLatency() error = %v, want ErrReclaimBeforeExpiry", err)
		}
		if got != 0 {
			t.Fatalf("ReclaimLatency() = %v with an error, want 0", got)
		}
	})

	t.Run("largest representable latency is returned", func(t *testing.T) {
		origin := time.Unix(0, 0)
		got, err := ReclaimLatency(origin, origin.Add(time.Duration(math.MaxInt64-1)))
		if err != nil {
			t.Fatalf("ReclaimLatency() error = %v", err)
		}
		if got != time.Duration(math.MaxInt64-1) {
			t.Fatalf("ReclaimLatency() = %v, want MaxInt64-1 ns", got)
		}
	})

	t.Run("difference beyond the Duration range is refused", func(t *testing.T) {
		origin := time.Unix(0, 0)
		_, err := ReclaimLatency(origin, origin.AddDate(1000, 0, 0))
		if !errors.Is(err, ErrReclaimLatencyOverflow) {
			t.Fatalf("ReclaimLatency() error = %v, want ErrReclaimLatencyOverflow", err)
		}
	})

	t.Run("zero instant as expiry overflows and is refused", func(t *testing.T) {
		_, err := ReclaimLatency(time.Time{}, time.Unix(0, 0))
		if !errors.Is(err, ErrReclaimLatencyOverflow) {
			t.Fatalf("ReclaimLatency() error = %v, want ErrReclaimLatencyOverflow", err)
		}
	})
}
