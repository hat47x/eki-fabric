package teideleg

import (
	"testing"
	"time"
)

func TestSimulatedLifecycleNegatives(t *testing.T) {
	now := time.Date(2026, 11, 11, 5, 0, 0, 0, time.UTC)
	base := SimulatedEvidence{
		LatestAttempt: 1, LatestFence: 10,
		LeaseExpiresAt: now.Add(time.Minute),
		Deadline:       now.Add(2 * time.Minute),
		ResultAttempt:  1, ResultFence: 10,
		ResultAt:     now.Add(30 * time.Second),
		ResultDigest: "dddd", ExpectedDigest: "dddd",
		ArtifactsTotal: 1, ArtifactsAvailable: 1,
	}
	cases := []struct {
		name   string
		mutate func(*SimulatedEvidence)
		want   Outcome
		reason string
	}{
		{
			name: "current attempt and fence",
			want: OutcomeAccepted,
		},
		{
			name: "stale attempt",
			mutate: func(e *SimulatedEvidence) {
				e.LatestAttempt, e.LatestFence = 2, 20
			},
			want:   OutcomeRejected,
			reason: "古い Attempt",
		},
		{
			name: "stale fence with the same attempt number",
			mutate: func(e *SimulatedEvidence) {
				e.LatestFence = 11
			},
			want:   OutcomeRejected,
			reason: "Fence",
		},
		{
			name: "lease expired before the result arrived",
			mutate: func(e *SimulatedEvidence) {
				e.LeaseExpiresAt = now
			},
			want:   OutcomeRejected,
			reason: "Lease",
		},
		{
			name: "partial artifact receipt",
			mutate: func(e *SimulatedEvidence) {
				e.ArtifactsTotal, e.ArtifactsAvailable = 2, 0
			},
			want:   OutcomeUndetermined,
			reason: "部分的",
		},
		{
			name: "artifact body fails re-hashing",
			mutate: func(e *SimulatedEvidence) {
				e.ArtifactMismatch = 1
			},
			want:   OutcomeRejected,
			reason: "digest",
		},
		{
			name: "cache or toolchain difference",
			mutate: func(e *SimulatedEvidence) {
				e.ResultDigest = "cccc"
			},
			want:   OutcomeRejected,
			reason: "一致しない",
		},
		{
			name: "no result at all",
			mutate: func(e *SimulatedEvidence) {
				e.ResultAt = time.Time{}
			},
			want:   OutcomeUndetermined,
			reason: "報告がない",
		},
		{
			name: "duplicate report of the same attempt is suppressed",
			mutate: func(e *SimulatedEvidence) {
				e.DuplicateResults = 1
			},
			want: OutcomeAccepted,
		},
		{
			name: "result after TEI's wait limit",
			mutate: func(e *SimulatedEvidence) {
				e.Deadline = now.Add(-time.Second)
			},
			want:   OutcomeUndetermined,
			reason: "期限",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := base
			if tc.mutate != nil {
				tc.mutate(&ev)
			}
			r := JudgeSimulated(ev)
			if r.Outcome != tc.want {
				t.Fatalf("outcome = %s, want %s: %s", r.Outcome, tc.want, r.Reason)
			}
			if tc.reason != "" && !contains(r.Reason, tc.reason) {
				t.Errorf("reason = %q, want it to mention %q", r.Reason, tc.reason)
			}
			if len(r.NotChecked) == 0 {
				t.Error("a simulated receipt must say that the fields are not part of the public contract")
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
