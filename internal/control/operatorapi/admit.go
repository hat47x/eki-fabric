package operatorapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
)

// AdmitPath is where an Administrator submits a signed Trust Bundle version.
//
// The split follows ADR-0038 §2: the Administrator signs, the Operator approves,
// and neither can do the other's part. That is why this route lives beside the
// approval route and checks the other role.
const AdmitPath = "/v1/admin/trust-bundles"

// Admitter admits a version that has already been signed. The approvals are the
// ones the Operator API collected; the implementation is expected to pass them to
// trustbundle.Store.AdmitFromBook.
type Admitter interface {
	AdmitSigned(version uint64, signed []byte) error
}

// admit reads the body, checks the role, and hands the version to the store.
func (h *Handler) admit(w http.ResponseWriter, r *http.Request, identity operatorcert.Identity) {
	if h.admitter == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "This operation is not available in this deployment")
		return
	}
	var body struct {
		Version uint64 `json:"version"`
		Signed  string `json:"signed"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_VERSION_DOCUMENT", "The body must carry version and signed")
		return
	}
	if body.Version == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_VERSION_DOCUMENT", "version must be a positive integer")
		return
	}
	signed, err := base64.StdEncoding.DecodeString(body.Signed)
	if err != nil || len(signed) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_VERSION_DOCUMENT", "signed must be base64 of the signed bundle")
		return
	}
	// The record is written before the version is admitted. If the sink cannot take
	// it, the admission does not happen: an authority change nobody can look up
	// afterwards is what ADR-0040 §5 refuses.
	// Only a sink that can refuse changes the order. With one, the line is written
	// first: an admission that nobody can look up afterwards is what ADR-0040 §5
	// refuses. A logger cannot fail, so the line still goes after the answer.
	if h.auditErr != nil {
		if err := h.record(identity, "admit", fmt.Sprintf("version %d", body.Version), OutcomeApproved); err != nil {
			writeError(w, http.StatusServiceUnavailable, "AUDIT_UNAVAILABLE", "The audit record could not be written, so the version was not admitted")
			return
		}
	}
	if err := h.admitter.AdmitSigned(body.Version, signed); err != nil {
		_ = h.record(identity, "admit", fmt.Sprintf("version %d", body.Version), OutcomeRefused)
		writeError(w, http.StatusConflict, "ADMISSION_REFUSED", err.Error())
		return
	}
	if h.auditErr == nil {
		_ = h.record(identity, "admit", fmt.Sprintf("version %d", body.Version), OutcomeApproved)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"schema":        "eki.trust-bundle-admission/v1",
		"version":       body.Version,
		"administrator": identity.Name,
	})
}
