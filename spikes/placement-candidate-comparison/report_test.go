package placementcandidatecomparison_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/spikes/placement-candidate-comparison"
)

func TestNewReportBuildsBestPath(t *testing.T) {
	c := placementcandidatecomparison.Comparison{
		FunctionID:   "pure-compute-v1",
		InputSHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		OutputSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		WallTimeNanos: 100000000,
		Candidates: []placementcandidatecomparison.Candidate{
			{Path: "T0 Direct", ProviderID: "local", Cost: placementcandidatecomparison.Cost{Amount: 0.01}, LatencyNanos: 50000000, TrustLevel: "high"},
			{Path: "T1 EKI Native", ProviderID: "eki", Cost: placementcandidatecomparison.Cost{Amount: 0.02}, LatencyNanos: 120000000, TrustLevel: "high"},
		},
	}

	report, err := placementcandidatecomparison.NewReport(c)
	if err != nil {
		t.Fatalf("NewReport: %v", err)
	}
	if report.Best.Path != "T0 Direct" {
		t.Errorf("Best.Path = %q, want %q", report.Best.Path, "T0 Direct")
	}
	if report.Schema != "tei.eki-comparison-report/v0" {
		t.Errorf("Schema = %q, want %q", report.Schema, "tei.eki-comparison-report/v0")
	}
	if report.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is zero")
	}

	encoded, err := report.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded placementcandidatecomparison.Report
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.Best.Path != report.Best.Path {
		t.Error("Report is not stable across JSON round-trip")
	}

	summary := report.Summary()
	if !strings.Contains(summary, "T0 Direct") {
		t.Errorf("Summary = %q, missing path", summary)
	}
}
