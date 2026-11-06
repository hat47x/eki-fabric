package workerplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// heldArtifactsBody is the wire form of a claim. The digests are hex, the same
// spelling the rest of the API uses.
type heldArtifactsBody struct {
	Digests []string `json:"digests"`
}

// maxHeldArtifactsBody bounds the request: 64 digests of hex plus JSON overhead is
// well under 8 KiB, and the limit keeps a claim from carrying a whole cache
// listing through the Control Plane.
const maxHeldArtifactsBody = 8 << 10

// serveHeldArtifacts records what the authenticated Worker says it already holds
// (ADR-0029 §5.1).
//
// The Worker comes from the certificate, never from the body: a claim is about the
// Worker that made it, and letting the body name another Worker would let one
// Worker poison another's placement hint.
func (h *mtlsHandler) serveHeldArtifacts(w http.ResponseWriter, r *http.Request, principal workerenrollment.WorkerPrincipal) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHeldArtifactsBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			reply(w, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		reply(w, http.StatusBadRequest, "bad_request")
		return
	}
	var wire heldArtifactsBody
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		reply(w, http.StatusBadRequest, "invalid_claim")
		return
	}
	held := make(workerregistry.HeldArtifacts, 0, len(wire.Digests))
	for _, text := range wire.Digests {
		raw, err := hex.DecodeString(text)
		if err != nil || len(raw) != sha256.Size {
			reply(w, http.StatusBadRequest, "invalid_claim")
			return
		}
		var sum [sha256.Size]byte
		copy(sum[:], raw)
		held = append(held, contract.FromSHA256(sum))
	}
	if err := held.Validate(); err != nil {
		reply(w, http.StatusBadRequest, "invalid_claim")
		return
	}

	err = h.heldClaims.SetHeldArtifacts(r.Context(), principal.Identity, held, time.Now().UTC())
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		reply(w, http.StatusServiceUnavailable, "unavailable")
	case errors.Is(err, statestore.ErrNotFound):
		// The Worker cannot take work (unknown or no longer registered), so its
		// claim has nowhere to go. One answer covers both: saying which would tell
		// the caller something about a Worker other than itself.
		reply(w, http.StatusConflict, "claim_refused")
	default:
		h.log.Error("held artifacts recording failed", "worker", principal.Identity, "error", err.Error())
		reply(w, http.StatusServiceUnavailable, "unavailable")
	}
}
