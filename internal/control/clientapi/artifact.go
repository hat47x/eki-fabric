package clientapi

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	claimbody "github.com/hat47x/eki-fabric/internal/control/claimbody"
)

// ErrArtifactNotFound means the store holds nothing under that name.
var ErrArtifactNotFound = errors.New("clientapi: no such artifact")

// ArtifactMeta is what the Control Plane recorded about a stored body. It is the
// subset the retrieval path needs (#494).
type ArtifactMeta struct {
	Owner     string
	MediaType string
	// DataClass is what the Worker declared (ADR-0034 §9).
	DataClass string
	// Withdrawn is set when the Task that produced the body ended before its
	// retention passed (ADR-0037 §9). The body is kept for an investigation but
	// is not served.
	Withdrawn bool
	Size      int64
	ExpiresAt time.Time
}

// ArtifactSource is the storage the retrieval path reads from. Keeping it an
// interface here means the handler does not depend on the reference store.
type ArtifactSource interface {
	LoadMeta(digestHex string) (ArtifactMeta, error)
	Open(digestHex string) (io.ReadCloser, int64, error)
}

// artifact answers GET /v1/artifacts/{artifact_id}. The identifier is the
// SHA-256 of the body: it is the one name the record and the store already share,
// so a lookup does not need a second index (#494).
func (h *Handler) artifact(w http.ResponseWriter, r *http.Request, id Identity, digest string) {
	meta, err := h.artifacts.LoadMeta(digest)
	if err != nil {
		if errors.Is(err, ErrArtifactNotFound) {
			h.writeArtifactFailure(w, claimbody.OutcomeNotFound, digest)
			return
		}
		h.log.Error("artifact metadata not read", "error", err.Error())
		h.writeArtifactFailure(w, claimbody.OutcomeNotFound, digest)
		return
	}
	outcome := claimbody.Decide(string(id.Principal.ID), meta.Owner, true, !time.Now().UTC().Before(meta.ExpiresAt), meta.Withdrawn)
	if outcome != claimbody.OutcomeDeliver {
		h.writeArtifactFailure(w, outcome, digest)
		return
	}
	w.Header().Set("Content-Type", mediaTypeOrDefault(meta.MediaType))
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	w.Header().Set("Digest", "sha-256="+base64Digest(digest))
	w.Header().Set("X-EKI-Artifact-Size", strconv.FormatInt(meta.Size, 10))
	w.WriteHeader(http.StatusOK)
	if _, err := claimbody.Stream(outcome, h.artifacts, digest, meta.Size, w); err != nil {
		// The status line is already sent, so the honest signal is a broken
		// connection: the requester sees a short body rather than a clean end of
		// a body that is not the one recorded.
		h.log.Error("artifact stream failed", "error", err.Error())
		if hijacker, ok := w.(http.Hijacker); ok {
			if conn, _, herr := hijacker.Hijack(); herr == nil {
				conn.Close()
			}
		}
		return
	}
	h.audit(string(id.Principal.ID), digest, ArtifactServed)
}

// writeArtifactFailure answers with the status and code the contract fixes. The
// body carries nothing about the artifact or the Task it came from (#494).
func (h *Handler) writeArtifactFailure(w http.ResponseWriter, outcome claimbody.Outcome, digest string) {
	outcomeName := ArtifactNotFound
	if outcome == claimbody.OutcomeExpired {
		outcomeName = ArtifactExpired
	}
	h.audit("", digest, outcomeName)
	writeJSON(w, outcome.Status(), map[string]any{
		"error": map[string]string{"code": outcome.Code()},
	})
}

func mediaTypeOrDefault(mediaType string) string {
	if strings.TrimSpace(mediaType) == "" {
		return "application/octet-stream"
	}
	return mediaType
}

// base64Digest renders a hex digest as the RFC 3230 Digest header value.
func base64Digest(digestHex string) string {
	raw, err := hex.DecodeString(digestHex)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(raw)
}
