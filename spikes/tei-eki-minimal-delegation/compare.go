package teideleg

import (
	"fmt"
	"sort"
	"time"
)

// Comparison is the cost report for one operation across two paths.
//
// It exists because a single sample is not a baseline: the same `tei validate`
// on one host ranged from 0.15s to over 9s during this work. The report states
// what was measured, how many times, and what it does not measure.
type Comparison struct {
	Schema string `json:"schema"`
	// Operation is the TEI operation the numbers belong to.
	Operation string `json:"operation"`
	// MeasuredAt is when the report was assembled.
	MeasuredAt time.Time `json:"measured_at"`

	Local LocalStats `json:"local"`
	// EKI is the delegation round trip. WaitMillis is the polling the caller
	// chose; subtract it to get the envelope cost.
	EKI EKIRoundTrip `json:"eki_round_trip"`

	// Caveat is never empty. It states the limit of the comparison so a reader
	// cannot take the numbers as a statement about execution cost.
	Caveat []string `json:"caveat"`
}

// LocalStats summarises n local runs of the same command.
type LocalStats struct {
	Samples   int   `json:"samples"`
	MinMillis int64 `json:"min_millis"`
	Median    int64 `json:"median_millis"`
	MaxMillis int64 `json:"max_millis"`
	// OutputSHA256 is the digest every successful sample produced. An empty
	// value means the samples disagreed, which makes the local path itself
	// unverifiable.
	OutputSHA256     string `json:"output_sha256,omitempty"`
	OutputStable     bool   `json:"output_stable"`
	ExitCodes        []int  `json:"exit_codes"`
	CacheState       string `json:"cache_state,omitempty"`
	ExecutableSHA256 string `json:"executable_sha256,omitempty"`
}

// EKIRoundTrip summarises n delegations of the same projection.
type EKIRoundTrip struct {
	Samples   int   `json:"samples"`
	MinMillis int64 `json:"min_millis"`
	Median    int64 `json:"median_millis"`
	MaxMillis int64 `json:"max_millis"`
	// Envelope* is the round trip minus the polling the caller chose. It is
	// what the delegation envelope costs, which is the number that compares
	// with a local run.
	EnvelopeMinMillis    int64 `json:"envelope_min_millis"`
	EnvelopeMedianMillis int64 `json:"envelope_median_millis"`
	EnvelopeMaxMillis    int64 `json:"envelope_max_millis"`
	MedianWaitMillis     int64 `json:"median_wait_millis"`
	MedianRequests       int   `json:"median_requests"`
	// Outcomes counts each receipt outcome.
	Outcomes map[string]int `json:"outcomes"`
	// Attempts is the largest attempt_count any sample reported.
	Attempts int `json:"attempts_observed,omitempty"`
}

// Compare assembles the report. localMillis comes from LocalRun values;
// traces come from delegations that the caller already recorded.
//
// It refuses to invent numbers: a report with no sample says so rather than
// reporting zero, and an empty trace set leaves the EKI section empty.
func Compare(operation string, locals []LocalRun, traces []Trace, now time.Time) Comparison {
	c := Comparison{
		Schema:     "tei.eki-delegation-comparison/v0",
		Operation:  operation,
		MeasuredAt: now.UTC(),
	}
	local := LocalStats{Samples: len(locals), OutputStable: true}
	durations := make([]int64, 0, len(locals))
	digests := map[string]int{}
	for _, l := range locals {
		durations = append(durations, l.DurationMillis)
		local.ExitCodes = append(local.ExitCodes, l.ExitCode)
		if l.OutputSHA256 != "" {
			digests[l.OutputSHA256]++
		} else {
			local.OutputStable = false
		}
		if local.CacheState == "" {
			local.CacheState = l.CacheState
		}
		if local.ExecutableSHA256 == "" {
			local.ExecutableSHA256 = l.ExecutableSHA256
		}
	}
	local.MinMillis, local.Median, local.MaxMillis = minMedianMax(durations)
	if len(digests) > 1 {
		local.OutputStable = false
	}
	for digest := range digests {
		local.OutputSHA256 = digest
	}
	c.Local = local

	if len(traces) > 0 {
		c.EKI = summarizeTraces(traces)
	}

	c.Caveat = []string{
		"EKI did not execute this operation in this measurement unless attempts_observed is greater than 0: the round trip covers the input upload, admission, status polling and the result answer only.",
		"The local numbers are wall-clock times of one process on one host; the same command varied by more than an order of magnitude during this work.",
		"The two paths are not interchangeable: a local run produces the output here, while a delegation only produces it if a Worker is placed and the result is accepted.",
	}
	return c
}

func summarizeTraces(traces []Trace) EKIRoundTrip {
	rt := EKIRoundTrip{Samples: len(traces), Outcomes: map[string]int{}}
	var elapsedMillis, envelopeMillis, waitedMillis []int64
	var requests []int
	for _, t := range traces {
		waited := t.Observation.WaitedMillis
		if !t.Observation.StartedAt.IsZero() && !t.Observation.FinishedAt.IsZero() {
			elapsed := t.Observation.FinishedAt.Sub(t.Observation.StartedAt).Milliseconds()
			elapsedMillis = append(elapsedMillis, elapsed)
			envelopeMillis = append(envelopeMillis, maxInt64(0, elapsed-waited))
		}
		waitedMillis = append(waitedMillis, waited)
		requests = append(requests, t.Observation.Requests)
		rt.Outcomes[string(t.Receipt.Outcome)]++
		if t.Links.AttemptCount > rt.Attempts {
			rt.Attempts = t.Links.AttemptCount
		}
	}
	rt.MinMillis, rt.Median, rt.MaxMillis = minMedianMax(elapsedMillis)
	rt.EnvelopeMinMillis, rt.EnvelopeMedianMillis, rt.EnvelopeMaxMillis = minMedianMax(envelopeMillis)
	_, rt.MedianWaitMillis, _ = minMedianMax(waitedMillis)
	_, medianRequests, _ := minMedianMaxInt(requests)
	rt.MedianRequests = int(medianRequests)
	return rt
}

func minMedianMax(values []int64) (min, median, max int64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return ordered[0], ordered[len(ordered)/2], ordered[len(ordered)-1]
}

func minMedianMaxInt(values []int) (min, median, max int) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	ordered := append([]int(nil), values...)
	sort.Ints(ordered)
	return ordered[0], ordered[len(ordered)/2], ordered[len(ordered)-1]
}

// MedianLabel is a short human-readable summary, used by the CLI's stderr line.
func (c Comparison) MedianLabel() string {
	return fmt.Sprintf("local median %dms (%d samples); EKI round trip median %dms, envelope median %dms (%d samples, %d requests, attempts=%d)",
		c.Local.Median, c.Local.Samples, c.EKI.Median, c.EKI.EnvelopeMedianMillis, c.EKI.Samples, c.EKI.MedianRequests, c.EKI.Attempts)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
