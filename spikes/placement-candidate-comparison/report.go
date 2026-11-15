package placementcandidatecomparison

import (
	"encoding/json"
	"fmt"
	"time"
)

// Report is the comparison output for one pure computation.
type Report struct {
	Schema        string
	FunctionID    string
	InputSHA256   string
	OutputSHA256  string
	WallTimeNanos int64
	Paths         []Candidate
	Best          Candidate
	GeneratedAt   time.Time
}

// NewReport builds a Report from a Comparison.
func NewReport(c Comparison) (Report, error) {
	best, err := Compare(c)
	if err != nil {
		return Report{}, err
	}
	return Report{
		Schema:        "tei.eki-comparison-report/v0",
		FunctionID:    c.FunctionID,
		InputSHA256:   c.InputSHA256,
		OutputSHA256:  c.OutputSHA256,
		WallTimeNanos: c.WallTimeNanos,
		Paths:         c.Candidates,
		Best:          best,
		GeneratedAt:   time.Now().UTC(),
	}, nil
}

// JSON returns the JSON representation of the report.
func (r Report) JSON() ([]byte, error) {
	return json.Marshal(r)
}

// Summary returns a human-readable summary.
func (r Report) Summary() string {
	return fmt.Sprintf("selected=%s provider=%s cost=%.4f latency=%dns trust=%s",
		r.Best.Path, r.Best.ProviderID, r.Best.Cost.Amount, r.Best.LatencyNanos, r.Best.TrustLevel)
}
