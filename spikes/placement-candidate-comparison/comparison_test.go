package placementcandidatecomparison_test

import (
	"testing"

	"github.com/hat47x/eki-fabric/spikes/placement-candidate-comparison"
)

func TestCompareRanksByCostLatencyTrust(t *testing.T) {
	c := placementcandidatecomparison.Comparison{
		FunctionID:    "pure-compute-v1",
		InputSHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		OutputSHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		WallTimeNanos: 100000000,
		Candidates: []placementcandidatecomparison.Candidate{
			{
				Path:         "T0 Direct",
				ProviderID:   "local",
				Cost:         placementcandidatecomparison.Cost{Currency: "USD", Unit: "request", Amount: 0.01},
				LatencyNanos: 50000000,
				TrustLevel:   "high",
				Authority:    "os-scheduler",
			},
			{
				Path:         "T1 EKI Native",
				ProviderID:   "eki",
				Cost:         placementcandidatecomparison.Cost{Currency: "USD", Unit: "request", Amount: 0.02},
				LatencyNanos: 120000000,
				TrustLevel:   "high",
				Authority:    "eki-scheduler",
			},
			{
				Path:         "T2 Managed Provider",
				ProviderID:   "managed",
				Cost:         placementcandidatecomparison.Cost{Currency: "USD", Unit: "request", Amount: 0.02},
				LatencyNanos: 100000000,
				TrustLevel:   "medium",
				Authority:    "provider-scheduler",
			},
		},
	}

	best, err := placementcandidatecomparison.Compare(c)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if best.Path != "T0 Direct" {
		t.Errorf("best.Path = %q, want %q", best.Path, "T0 Direct")
	}
	if best.ProviderID != "local" {
		t.Errorf("best.ProviderID = %q, want %q", best.ProviderID, "local")
	}
}

func TestCompareRejectsEmptyCandidates(t *testing.T) {
	c := placementcandidatecomparison.Comparison{}
	_, err := placementcandidatecomparison.Compare(c)
	if err == nil {
		t.Fatal("expected error for empty candidates, got nil")
	}
}

func TestCompareBreaksTiesByTrustLevel(t *testing.T) {
	c := placementcandidatecomparison.Comparison{
		FunctionID:    "pure-compute-v1",
		InputSHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		OutputSHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		WallTimeNanos: 100000000,
		Candidates: []placementcandidatecomparison.Candidate{
			{
				Path:         "T1 EKI Native",
				ProviderID:   "eki",
				Cost:         placementcandidatecomparison.Cost{Currency: "USD", Unit: "request", Amount: 0.01},
				LatencyNanos: 100000000,
				TrustLevel:   "low",
				Authority:    "eki-scheduler",
			},
			{
				Path:         "T2 Managed Provider",
				ProviderID:   "managed",
				Cost:         placementcandidatecomparison.Cost{Currency: "USD", Unit: "request", Amount: 0.01},
				LatencyNanos: 100000000,
				TrustLevel:   "high",
				Authority:    "provider-scheduler",
			},
		},
	}

	best, err := placementcandidatecomparison.Compare(c)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if best.Path != "T2 Managed Provider" {
		t.Errorf("best.Path = %q, want %q", best.Path, "T2 Managed Provider")
	}
}
