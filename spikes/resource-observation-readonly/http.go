package resourceobservationreadonly

import (
	"encoding/json"
	"net/http"
)

// NewHandler returns an HTTP handler that serves read-only observations.
// Writes are rejected with 405.
func NewHandler(store *SnapshotStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/resources", func(w http.ResponseWriter, r *http.Request) {
		list := make([]Observation, 0)
		for _, obs := range store.entries {
			if store.IsFresh(obs) {
				list = append(list, obs)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("GET /v1/resources/{provider}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("provider")
		obs, ok := store.Get(id)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !store.IsFresh(obs) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(obs)
	})
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		mux.HandleFunc(method+" /v1/resources", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
		})
		mux.HandleFunc(method+" /v1/resources/{provider}", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
		})
	}
	return mux
}
