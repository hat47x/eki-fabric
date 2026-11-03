package operatorapi

import (
	"net/http"
	"strconv"

	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
)

// PendingPath is where an Operator reads the approvals waiting for a version.
//
// It exists because an approval has no effect until a version is signed and
// admitted (#545): without a way to look, an Operator cannot tell "my approval was
// recorded" from "my approval was dropped", and the second looks exactly like the
// first from the outside.
const PendingPath = "/v1/operator/trust-bundles/approvals"

// pending lists the approvals the book holds for a version.
func (h *Handler) pending(w http.ResponseWriter, r *http.Request, identity operatorcert.Identity) {
	if h.book == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "This operation is not available in this deployment")
		return
	}
	version, err := strconv.ParseUint(r.URL.Query().Get("version"), 10, 64)
	if err != nil || version == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_VERSION", "version must be a positive integer")
		return
	}
	approvals := h.book.For(version)
	out := make([]map[string]any, 0, len(approvals))
	for _, a := range approvals {
		out = append(out, map[string]any{
			"operation":     string(a.Operation),
			"issuer":        a.Issuer.String(),
			"approved_by":   a.ApprovedBy,
			"approved_at":   a.ApprovedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"justification": a.Justification,
		})
	}
	h.record(identity, "", "", OutcomeApproved)
	writeJSON(w, http.StatusOK, map[string]any{
		"schema":    "eki.trust-bundle-approvals/v1",
		"version":   version,
		"approvals": out,
	})
}
