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
	if err := h.admitter.AdmitSigned(body.Version, signed); err != nil {
		h.record(identity, "admit", fmt.Sprintf("version %d", body.Version), OutcomeRefused)
		writeError(w, http.StatusConflict, "ADMISSION_REFUSED", err.Error())
		return
	}
	h.record(identity, "admit", fmt.Sprintf("version %d", body.Version), OutcomeApproved)
	writeJSON(w, http.StatusCreated, map[string]any{
		"schema":        "eki.trust-bundle-admission/v1",
		"version":       body.Version,
		"administrator": identity.Name,
	})
}
