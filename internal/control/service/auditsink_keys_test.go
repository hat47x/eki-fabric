package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/clientapi"
)

// The record's keys are a contract with the deployment's store, not a copy of the
// Go field names (ADR-0040 §7). A store written in another language reads these.
func TestTheRecordUsesLowercaseKeys(t *testing.T) {
	var seen map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Schema string         `json:"schema"`
			Record map[string]any `json:"record"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body: %v", err)
		}
		seen = body.Record
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sink := newAuditSink(server.URL)
	record := clientapi.ArtifactAccess{At: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), Principal: "alice", Artifact: "sha256:00", Outcome: "served"}
	if err := sink.Send(context.Background(), artifactAccessSchema, record); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	for _, key := range []string{"at", "principal", "artifact", "outcome"} {
		if _, ok := seen[key]; !ok {
			t.Fatalf("record has no %q key: %v", key, seen)
		}
	}
}
