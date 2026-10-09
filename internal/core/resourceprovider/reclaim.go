package resourceprovider

import (
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	// ErrReclaimBeforeExpiry is returned when a slot is reported reclaimed before the
	// lease expired. Such a pair of instants cannot describe a reclaim after expiry,
	// so the caller must find out which input is wrong.
	ErrReclaimBeforeExpiry = errors.New("resourceprovider: slot reclaimed before lease expiry")
	// ErrReclaimLatencyOverflow is returned when the difference between the two
	// instants is too large for a time.Duration. time.Time.Sub saturates in that case,
	// so the result would be wrong without this check.
	ErrReclaimLatencyOverflow = errors.New("resourceprovider: reclaim latency is not representable")
)

// ReclaimLatency returns how long a slot stayed held after its lease expired. It is
// the time from lease expiry (expiredAt) to slot reclamation (reclaimedAt), which is
// the quantity that RQ-048 asks about.
//
// The function computes a difference between two instants that the caller supplies.
// It reads no clock, performs no I/O, and starts no goroutines. The two instants must
// come from the same time source. This package does not check that. A caller that
// compares a wall-clock reading with a monotonic reading gets a value that means
// nothing.
//
// Errors:
//   - ErrReclaimBeforeExpiry when reclaimedAt is before expiredAt.
//   - ErrReclaimLatencyOverflow when the difference exceeds the range of time.Duration
//     (about 292 years).
//
// Scope: this function gives a latency from two timestamps. It does not measure
// latency on a real cluster. Measurement against a real Kubernetes or Slurm installation
// is open under issue #67, and this function does not close it.
func ReclaimLatency(expiredAt, reclaimedAt time.Time) (time.Duration, error) {
	if reclaimedAt.Before(expiredAt) {
		return 0, fmt.Errorf("%w: reclaimed %s, expired %s", ErrReclaimBeforeExpiry,
			reclaimedAt.Format(time.RFC3339Nano), expiredAt.Format(time.RFC3339Nano))
	}
	latency := reclaimedAt.Sub(expiredAt)
	// time.Time.Sub returns the largest Duration when the true difference is larger.
	// That value is indistinguishable from a real result, so it is refused.
	if latency == math.MaxInt64 {
		return 0, fmt.Errorf("%w: from %s to %s", ErrReclaimLatencyOverflow,
			expiredAt.Format(time.RFC3339Nano), reclaimedAt.Format(time.RFC3339Nano))
	}
	return latency, nil
}
