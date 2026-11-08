package transportfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// An observation is a claim from the selected adapter, not proof that a
// worker ran, a grant was valid, or an external system committed an effect.
type transportTrace struct {
	CaseID          string             `json:"case_id"`
	FixtureSHA256   string             `json:"fixture_sha256"`
	Profile         string             `json:"transport_profile"`
	RunID           string             `json:"run_id"`
	AdapterRevision string             `json:"adapter_revision"`
	CaptureMethod   string             `json:"capture_method"`
	Observations    []traceObservation `json:"observations"`
}

type traceObservation struct {
	EventID     string `json:"event_id"`
	Disposition string `json:"observed_disposition"`
}

type transportOracle struct {
	CaseID   string `json:"case_id"`
	Profiles []struct {
		Name string `json:"name"`
	} `json:"transport_profiles"`
	Expected map[string]string `json:"independent_expected"`
}

// checkTrace compares adapter-reported observations to the frozen fixture.
// It deliberately never invokes referenceDispositions: both implementations
// must not share the same evaluation path or invent observed evidence.
func checkTrace(oracle transportOracle, fixtureRaw []byte, trace transportTrace) []string {
	problems := []string{}
	expectedHash := sha256.Sum256(fixtureRaw)
	if trace.CaseID != oracle.CaseID || trace.CaseID == "" {
		problems = append(problems, "case_id mismatch")
	}
	if len(trace.FixtureSHA256) != 64 || trace.FixtureSHA256 != hex.EncodeToString(expectedHash[:]) {
		problems = append(problems, "fixture_sha256 mismatch")
	}
	knownProfile := false
	for _, profile := range oracle.Profiles {
		if profile.Name != "" && profile.Name == trace.Profile {
			knownProfile = true
		}
	}
	if !knownProfile {
		problems = append(problems, "unknown transport_profile")
	}
	if strings.TrimSpace(trace.RunID) == "" || strings.TrimSpace(trace.AdapterRevision) == "" || strings.TrimSpace(trace.CaptureMethod) == "" {
		problems = append(problems, "missing trace provenance")
	}
	if len(trace.Observations) != len(oracle.Expected) {
		problems = append(problems, fmt.Sprintf("observed %d events; expected %d", len(trace.Observations), len(oracle.Expected)))
	}
	seen := map[string]bool{}
	for _, event := range trace.Observations {
		if seen[event.EventID] {
			problems = append(problems, "duplicate event_id: "+event.EventID)
		}
		seen[event.EventID] = true
		want, known := oracle.Expected[event.EventID]
		if !known {
			problems = append(problems, "unknown event_id: "+event.EventID)
		} else if event.Disposition != want {
			problems = append(problems, fmt.Sprintf("%s: observed %q, expected %q", event.EventID, event.Disposition, want))
		}
	}
	for id := range oracle.Expected {
		if !seen[id] {
			problems = append(problems, "missing event_id: "+id)
		}
	}
	sort.Strings(problems)
	return problems
}

func loadFrozenTransportOracle(t *testing.T) (transportOracle, []byte) {
	t.Helper()
	raw, err := os.ReadFile("fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle transportOracle
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.CaseID == "" || len(oracle.Profiles) == 0 || len(oracle.Expected) == 0 {
		t.Fatal("frozen fixture lacks the required oracle metadata")
	}
	return oracle, raw
}

func syntheticTraceForComparator(oracle transportOracle, raw []byte) transportTrace {
	digest := sha256.Sum256(raw)
	results := make([]traceObservation, 0, len(oracle.Expected))
	for id, disposition := range oracle.Expected {
		results = append(results, traceObservation{EventID: id, Disposition: disposition})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].EventID < results[j].EventID })
	return transportTrace{
		CaseID: oracle.CaseID, FixtureSHA256: hex.EncodeToString(digest[:]),
		Profile: oracle.Profiles[0].Name, RunID: "synthetic-comparator-test",
		AdapterRevision: "synthetic-only", CaptureMethod: "constructed-in-test-no-adapter",
		Observations: results,
	}
}

func TestTraceComparatorRejectsInvalidEvidence(t *testing.T) {
	oracle, raw := loadFrozenTransportOracle(t)
	basic := syntheticTraceForComparator(oracle, raw)
	if got := checkTrace(oracle, raw, basic); len(got) != 0 {
		t.Fatalf("comparator rejected its synthetic baseline: %v", got)
	}
	tests := []struct {
		name   string
		mutate func(*transportTrace)
	}{
		{"missing-event", func(x *transportTrace) { x.Observations = x.Observations[:len(x.Observations)-1] }},
		{"duplicate-event", func(x *transportTrace) { x.Observations = append(x.Observations, x.Observations[0]) }},
		{"wrong-disposition", func(x *transportTrace) { x.Observations[0].Disposition = "fabricated-accepted" }},
		{"unknown-event", func(x *transportTrace) { x.Observations[0].EventID = "E-UNDECLARED" }},
		{"wrong-fixture-digest", func(x *transportTrace) { x.FixtureSHA256 = strings.Repeat("0", 64) }},
		{"unknown-transport-profile", func(x *transportTrace) { x.Profile = "not-authorized-by-fixture" }},
		{"missing-capture-provenance", func(x *transportTrace) { x.CaptureMethod = "" }},
		{"wrong-case-id", func(x *transportTrace) { x.CaseID = "another-fixture" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := basic
			candidate.Observations = append([]traceObservation(nil), basic.Observations...)
			tc.mutate(&candidate)
			if got := checkTrace(oracle, raw, candidate); len(got) == 0 {
				t.Fatal("invalid adapter claim passed the comparator")
			}
		})
	}
}

// A real run must explicitly pass an observation file. Without one, skipping
// is deliberate: a passing unit test is not a passing adapter conformance.
func TestProvidedAdapterObservedTrace(t *testing.T) {
	evidencePath := os.Getenv("EKI_TRANSPORT_TRACE_FILE")
	if evidencePath == "" {
		t.Skip("NOT RUN: EKI_TRANSPORT_TRACE_FILE not set; no adapter observations provided")
	}
	raw, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	var trace transportTrace
	if err := json.Unmarshal(raw, &trace); err != nil {
		t.Fatal(err)
	}
	oracle, fixtureRaw := loadFrozenTransportOracle(t)
	if problems := checkTrace(oracle, fixtureRaw, trace); len(problems) != 0 {
		t.Fatalf("adapter-reported observations do not match frozen expected:\n%s", strings.Join(problems, "\n"))
	}
	// A matching self-reported file remains only a trace comparison. Actual
	// transport, authority and effect evidence require separate inspection.
	t.Logf("reported dispositions matched; inspect adapter run provenance separately: %s", evidencePath)
}
