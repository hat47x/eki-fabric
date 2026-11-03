package operatorapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
)

// VersionsPath is where an Operator reads the accepted Trust Bundle versions.
//
// It exists so the key handover (ADR-0038 §7) can be finished from outside: "may
// I remove the old key yet" is answered by looking at which key verified each
// version, and that answer must not require reading the state store by hand
// (ADR-0038 §9).
const VersionsPath = "/v1/operator/trust-bundles/versions"

// versionEntry is one row of the answer. KeyID and VerifiedKey sit side by side on
// purpose: one is what the signer called itself, the other is what happened.
type versionEntry struct {
	Version     uint64    `json:"version"`
	NotAfter    time.Time `json:"not_after"`
	RecordedAt  time.Time `json:"recorded_at"`
	KeyID       string    `json:"key_id,omitempty"`
	VerifiedKey string    `json:"verified_key,omitempty"`
}

const (
	defaultVersionLimit = 20
	maxVersionLimit     = 100
)

// versions lists the newest versions first.
func (h *Handler) versions(w http.ResponseWriter, r *http.Request, identity operatorcert.Identity) {
	if h.bundles == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "This operation is not available in this deployment")
		return
	}
	limit := defaultVersionLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxVersionLimit {
			writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be between 1 and "+strconv.Itoa(maxVersionLimit))
			return
		}
		limit = parsed
	}
	records, err := h.bundles.ListTrustBundles(r.Context(), limit)
	if err != nil {
		_ = h.record(identity, "", "", OutcomeRefused)
		writeError(w, http.StatusInternalServerError, "STORE_UNAVAILABLE", "The versions could not be read")
		return
	}
	entries := make([]versionEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, versionEntry{
			Version:     record.Version,
			NotAfter:    record.NotAfter.UTC(),
			RecordedAt:  record.RecordedAt.UTC(),
			KeyID:       record.KeyID,
			VerifiedKey: record.VerifiedKey,
		})
	}
	_ = h.record(identity, "", "", OutcomeApproved)
	writeJSON(w, http.StatusOK, map[string]any{
		"schema":   "eki.trust-bundle-versions/v1",
		"versions": entries,
	})
}
