package placementcandidatecomparison

import (
	"fmt"
)

// Candidate is one placement candidate across the 3 paths.
type Candidate struct {
	Path         string
	ProviderID   string
	Cost         Cost
	LatencyNanos int64
	TrustLevel   string
	Authority    string
	DataTransfer bool
	EffectMode   string
	Note         string
}

// Cost is a currency-agnostic cost estimate.
type Cost struct {
	Currency string
	Unit     string
	Amount   float64
}

// Comparison holds the candidates for one pure computation.
type Comparison struct {
	FunctionID    string
	InputSHA256   string
	OutputSHA256  string
	WallTimeNanos int64
	Candidates    []Candidate
	SelectedPath  string
	SelectedID    string
}

// Compare ranks candidates by cost, then latency, then trust level.
func Compare(c Comparison) (Candidate, error) {
	if len(c.Candidates) == 0 {
		return Candidate{}, fmt.Errorf("no candidates")
	}
	best := c.Candidates[0]
	for _, cand := range c.Candidates[1:] {
		if cand.Cost.Amount < best.Cost.Amount {
			best = cand
		} else if cand.Cost.Amount == best.Cost.Amount && cand.LatencyNanos < best.LatencyNanos {
			best = cand
		} else if cand.Cost.Amount == best.Cost.Amount && cand.LatencyNanos == best.LatencyNanos && trustRank(cand.TrustLevel) > trustRank(best.TrustLevel) {
			best = cand
		}
	}
	return best, nil
}

func trustRank(level string) int {
	switch level {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}
