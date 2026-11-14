package resourceobservationreadonly_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/spikes/resource-observation-readonly"
)

func TestHTTPHandlerReadOnly(t *testing.T) {
	now := time.Now()
	store := resourceobservationreadonly.NewSnapshotStore(func() time.Time { return now })
	store.Set(resourceobservationreadonly.Observation{
		Schema:       resourceobservationreadonly.Schema,
		ProviderID:   "P1",
		ObservedAt:   now,
		Staleness:    time.Minute,
		Capability:   resourceobservationreadonly.Capability{CPUMillis: 1000},
		State:        resourceobservationreadonly.ProviderState{AvailableSlots: 1},
	})
	handler := resourceobservationreadonly.NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/v1/resources/P1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/resources/P1 = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/resources", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v1/resources = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHTTPHandlerEmpty(t *testing.T) {
	store := resourceobservationreadonly.NewSnapshotStore(func() time.Time { return time.Now() })
	handler := resourceobservationreadonly.NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/v1/resources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/resources = %d, want %d", rec.Code, http.StatusOK)
	}
	var list []resourceobservationreadonly.Observation
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("len(list) = %d, want 0", len(list))
	}
}
