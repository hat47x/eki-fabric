package resultclaim

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

var timeRangeStatuses = []Status{StatusCompleted, StatusFailed, StatusReclaimed, StatusDeadlineExceeded,
	StatusCancelled, StatusOutputExceeded, StatusRunnerStuck}

func timeRangeConfig(status Status) ClaimConfig {
	cfg := claimConfig()
	cfg.Status = status
	if status != StatusCompleted {
		cfg.OutputDigest = contract.Digest{}
	}
	return cfg
}

// In Go 1.27, shifting by 2^64 nanoseconds made the old digest alias a
// different century. Rejection must not depend on an undefined UnixNano value.
func TestNewClaimRejectsAliasedTimestampWindows(t *testing.T) {
	for _, status := range timeRangeStatuses {
		for _, direction := range []int{1, -1} {
			t.Run(fmt.Sprintf("%s/%d", status, direction), func(t *testing.T) {
				cfg := timeRangeConfig(status)
				original, err := NewClaim(cfg)
				if err != nil {
					t.Fatal(err)
				}
				shift := func(v time.Time) time.Time {
					if direction > 0 {
						return v.Add(time.Duration(math.MaxInt64)).Add(time.Duration(math.MaxInt64)).Add(2)
					}
					return v.Add(time.Duration(math.MinInt64)).Add(time.Duration(math.MinInt64))
				}
				cfg.StartedAt, cfg.EndedAt = shift(cfg.StartedAt), shift(cfg.EndedAt)
				if cfg.StartedAt.Equal(original.StartedAt()) || cfg.EndedAt.Sub(cfg.StartedAt) != original.EndedAt().Sub(original.StartedAt()) {
					t.Fatal("fixture must move the observation while preserving the window length")
				}
				got, err := NewClaim(cfg)
				if !errors.Is(err, ErrInvalidClaim) || got.Valid() {
					t.Fatalf("accepted an out-of-range window: error=%v, Equal=%v, same Digest=%v", err, got.Equal(original), got.Digest() == original.Digest())
				}
			})
		}
	}
}

func TestNewClaimTimestampRangeEndpoints(t *testing.T) {
	minimum, maximum := time.Unix(0, math.MinInt64), time.Unix(0, math.MaxInt64)
	for _, status := range timeRangeStatuses {
		for _, tc := range []struct {
			name    string
			start   time.Time
			end     time.Time
			invalid bool
		}{
			{"minimum", minimum, minimum.Add(time.Nanosecond), false},
			{"maximum", maximum.Add(-time.Nanosecond), maximum, false},
			{"epoch", time.Unix(0, 0), time.Unix(0, 1), false},
			{"equal", baseTime, baseTime, false},
			{"start-one-nanosecond-before", minimum.Add(-time.Nanosecond), minimum, true},
			{"end-one-nanosecond-after", maximum, maximum.Add(time.Nanosecond), true},
		} {
			t.Run(string(status)+"/"+tc.name, func(t *testing.T) {
				cfg := timeRangeConfig(status)
				cfg.StartedAt, cfg.EndedAt = tc.start, tc.end
				got, err := NewClaim(cfg)
				if tc.invalid {
					if !errors.Is(err, ErrInvalidClaim) || got.Valid() {
						t.Fatalf("out-of-range timestamp accepted: %v", err)
					}
					return
				}
				if err != nil || !got.StartedAt().Equal(tc.start) || !got.EndedAt().Equal(tc.end) {
					t.Fatalf("valid endpoint was lost: %v", err)
				}
				cfg.StartedAt, cfg.EndedAt = tc.start.In(time.FixedZone("fixture", 9*3600)), tc.end.In(time.FixedZone("fixture", 9*3600))
				zoned, err := NewClaim(cfg)
				if err != nil || !zoned.Equal(got) || zoned.Digest() != got.Digest() {
					t.Fatalf("timezone changed the observation: %v", err)
				}
			})
		}
	}
}

func TestInRangeClaimDigestCompatibility(t *testing.T) {
	// Captured from main 3e53660 before adding timestamp range validation.
	for status, expected := range map[Status]string{
		StatusCompleted:        "sha256:51256963041ab2836b39bdfa8cd5aee5619acddb58e1e8be1b940c2e33fbf977",
		StatusFailed:           "sha256:55e2592d66e859cef5f5db553cbf5d8fda83565f3dc65e93a106d7f3506c67f4",
		StatusReclaimed:        "sha256:584dd731920cf687d5f7bb7eb785080d4c17e81dff9e388fcd404152e9d76a73",
		StatusDeadlineExceeded: "sha256:f8f4ae11b81770389745770250354fa0b0ef56fe8f42f3bbe07291c7898fcb2b",
		StatusCancelled:        "sha256:2478dc4a0a8e21c1aced6e96a721790a4a2d7084379225c437e59487e5c19782",
		StatusOutputExceeded:   "sha256:1b2c8d9b609a47c0f87a015212b8e13943995d6119a2c86003d5d75a3a1b0428",
		StatusRunnerStuck:      "sha256:b6889e4a6b7a6b167b15c33432a0c1d438a74ec910599422dcd533db03b33d3f",
	} {
		c, err := NewClaim(timeRangeConfig(status))
		if err != nil {
			t.Fatal(err)
		}
		if c.Digest().String() != expected {
			t.Fatalf("%s: compatible claim digest changed: %s", status, c.Digest())
		}
	}
}
