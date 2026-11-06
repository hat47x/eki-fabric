package workerplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	spi "github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// maxArtifactBody bounds what this route will serve. A larger artifact is refused
// rather than streamed: the bound keeps a single fetch from filling the endpoint's
// memory, and a Task that needs more is a deployment question, not a request one.
const maxArtifactBody = 64 << 20

// artifactPathPrefix is the route prefix. The digest is the whole address: a
// request names the content it wants, so what it receives can be checked against
// the name (ADR-0029 §5.10).
const artifactPathPrefix = "/test-profile/v1/artifacts/"

// serveArtifact serves one input to the authenticated Worker, if the Attempt it
// holds declares that digest as an input and the deployment can produce the bytes.
func (h *mtlsHandler) serveArtifact(w http.ResponseWriter, r *http.Request, principal workerenrollment.WorkerPrincipal) {
	digest, err := contract.ParseSHA256(strings.TrimPrefix(r.URL.Path, artifactPathPrefix))
	if err != nil {
		reply(w, http.StatusBadRequest, "invalid_digest")
		return
	}
	assignments, err := h.assignments.ListAssignments(r.Context(), spi.Principal{
		WorkerIdentity: string(principal.Identity),
		TrustDomain:    principal.TrustDomain,
	})
	if err != nil {
		h.log.Error("artifact fetch: list assignments failed", "worker", principal.Identity, "error", err.Error())
		reply(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if !ArtifactFetchAllowed(assignments, digest) {
		// The Worker does not hold an Attempt that declares this input. Saying
		// which of "not assigned" and "revoked" it is would tell the Worker
		// something about its own placement it can already read from its listing.
		reply(w, http.StatusForbidden, "not_assigned")
		return
	}
	if h.artifacts == nil {
		reply(w, http.StatusNotImplemented, "artifacts_not_served")
		return
	}
	body, size, err := h.artifacts.OpenArtifact(r.Context(), digest)
	switch {
	case errors.Is(err, ErrArtifactNotFound):
		reply(w, http.StatusNotFound, "artifact_not_found")
		return
	case err != nil:
		h.log.Error("artifact fetch failed", "worker", principal.Identity, "error", err.Error())
		reply(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	defer func() { _ = body.Close() }()
	if size > maxArtifactBody {
		reply(w, http.StatusRequestEntityTooLarge, "artifact_too_large")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if _, err := io.Copy(w, io.LimitReader(body, maxArtifactBody+1)); err != nil {
		// The bytes are already going out; the status is spent. The Worker sees a
		// short body and its own digest check fails, which is the safe outcome.
		h.log.Error("artifact fetch: copy failed", "worker", principal.Identity, "error", err.Error())
	}
}

// ErrArtifactNotFound is what an ArtifactSource returns when this deployment has no
// body for the digest.
var ErrArtifactNotFound = errors.New("artifact body not found")

// ArtifactSource produces the bytes of one artifact by digest. A deployment that
// does not serve them leaves it nil, and the route answers 501 rather than 404: the
// difference between "not served here" and "not held" matters to the Worker.
type ArtifactSource interface {
	OpenArtifact(ctx context.Context, digest contract.Digest) (io.ReadCloser, int64, error)
}

// serveArtifactRoute authenticates the request and serves the body. It is separate
// from the POST routes so the method check cannot be inherited from them.
func (h *mtlsHandler) serveArtifactRoute(w http.ResponseWriter, r *http.Request) {
	if h.assignments == nil {
		reply(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		reply(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	principal, _, status := h.authenticate(r)
	if status != 0 {
		h.refuse(w, status)
		return
	}
	h.serveArtifact(w, r, principal)
}
