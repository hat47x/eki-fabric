package transportfixture

import (
	"encoding/json"
	"os"
	"testing"
)

// This test only checks that the predeclared oracle can be consumed by a future
// adapter. It does NOT run Local IPC, Connect, a Worker, or a lease authority.
func TestFixtureIsWellFormed(t *testing.T) {
	raw, err := os.ReadFile("fixture.json")
	if err != nil {
		t.Fatal(err)
	}

	var fixture struct {
		FixtureVersion string `json:"fixture_version"`
		CaseID         string `json:"case_id"`
		DataContract   struct {
			Kind     string `json:"kind"`
			SchemaID string `json:"schema_id"`
			TrackID  string `json:"track_id"`
		} `json:"data_contract"`
		DeliveryContract struct {
			Ordering     string `json:"ordering"`
			StalePolicy  string `json:"stale_policy"`
			PartialResult string `json:"partial_result"`
		} `json:"delivery_contract"`
		TransportProfiles []struct {
			Name   string `json:"name"`
			Role   string `json:"role"`
			Status string `json:"implementation_status"`
		} `json:"transport_profiles"`
		Events []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			Generation string `json:"generation"`
			Sequence   *int   `json:"sequence"`
			Epoch      string `json:"epoch"`
		} `json:"events"`
		Expected map[string]string `json:"independent_expected"`
		Evidence struct {
			LocalIPC string `json:"local_ipc"`
			Connect  string `json:"connect_data_adapter"`
			E2E      string `json:"end_to_end"`
		} `json:"execution_evidence"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.FixtureVersion != "D0-1" || fixture.CaseID == "" {
		t.Fatal("missing fixture revision or identity")
	}
	if fixture.DataContract.Kind == "" || fixture.DataContract.SchemaID == "" || fixture.DataContract.TrackID == "" {
		t.Fatal("data contract is incomplete")
	}
	if fixture.DeliveryContract.Ordering == "" || fixture.DeliveryContract.StalePolicy == "" || fixture.DeliveryContract.PartialResult == "" {
		t.Fatal("delivery contract is incomplete")
	}
	if len(fixture.TransportProfiles) != 2 {
		t.Fatalf("expected two candidate profiles, got %d", len(fixture.TransportProfiles))
	}
	profiles := make(map[string]bool)
	for _, profile := range fixture.TransportProfiles {
		if profile.Name == "" || profiles[profile.Name] || profile.Role != "data-delivery" || profile.Status != "NOT_RUN" {
			t.Fatalf("invalid or prematurely qualified profile: %+v", profile)
		}
		profiles[profile.Name] = true
	}
	if !profiles["local-ipc-candidate"] || !profiles["connect-data-adapter-candidate"] {
		t.Fatal("fixture does not name both candidate transport adapters")
	}
	if len(fixture.Events) == 0 || len(fixture.Expected) != len(fixture.Events) {
		t.Fatal("the independent oracle must cover every declared event exactly once")
	}
	seen := make(map[string]bool)
	for _, event := range fixture.Events {
		if event.ID == "" || seen[event.ID] || event.Kind == "" || event.Epoch == "" {
			t.Fatalf("invalid event identity: %+v", event)
		}
		seen[event.ID] = true
		if fixture.Expected[event.ID] == "" {
			t.Fatalf("missing independently fixed expected disposition for %s", event.ID)
		}
		if event.Kind == "observation" && (event.Generation == "" || event.Sequence == nil) {
			t.Fatalf("observation lacks generation or sequence: %+v", event)
		}
	}
	if fixture.Evidence.LocalIPC != "NOT_RUN" || fixture.Evidence.Connect != "NOT_RUN" || fixture.Evidence.E2E != "NOT_RUN" {
		t.Fatal("a static fixture must not claim a real transport/E2E PASS")
	}
}
