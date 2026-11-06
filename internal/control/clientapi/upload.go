package clientapi

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ArtifactRetention is how long an uploaded body may be served. It is provisional
// until the retention policy of ADR-0037 is wired to ingest (#245).
const ArtifactRetention = 24 * time.Hour

// ErrDigestMismatch means the bytes this handler measured are not the bytes the
// sink kept. The upload is refused and nothing is kept.
var ErrDigestMismatch = errors.New("clientapi: artifact body does not match its measured digest")

// ArtifactSink is where an uploaded body is kept. It is separate from
// ArtifactSource so a deployment reads and writes through the same store without
// either side depending on the other (#245).
type ArtifactSink interface {
	// Put keeps the body under the digest it claims and returns that name and
	// the size it wrote.
	Put(r io.Reader, claimedHex string, maxBytes int64) (string, int64, error)
	// PutMeta records what a retrieval needs to decide whether to serve it.
	PutMeta(digestHex string, record UploadRecord) error
}

// UploadRecord is what is kept beside the body.
type UploadRecord struct {
	Owner     string
	MediaType string
	DataClass string
	Size      int64
	ExpiresAt time.Time
}

type artifactUploadAccepted struct {
	Schema   string             `json:"schema"`
	IngestID string             `json:"ingest_id"`
	Artifact artifactDescriptor `json:"artifact"`
}

type artifactDescriptor struct {
	Schema     string         `json:"schema"`
	ArtifactID string         `json:"artifact_id"`
	Digest     string         `json:"digest"`
	SizeBytes  int64          `json:"size_bytes"`
	MediaType  string         `json:"media_type"`
	DataClass  string         `json:"data_class"`
	Facets     artifactFacets `json:"facets"`
}

type artifactFacets struct {
	ContentIdentity string `json:"content_identity"`
	SafeProcessing  string `json:"safe_processing"`
	Provenance      string `json:"provenance"`
	Availability    string `json:"availability"`
}

// take returns the session for an upload, or nil when this principal may not use
// it. An unknown session and another principal's session are the same answer:
// whether a session exists is not disclosed.
func (s *ingestSessions) take(principal, uploadID string) *ingestSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.byID[uploadID]
	if !ok || session.principal != principal {
		return nil
	}
	if s.now().UTC().After(session.expiresAt) {
		delete(s.byID, uploadID)
		return nil
	}
	return session
}

// finish removes the session once its bytes were kept: a session is one upload.
func (s *ingestSessions) finish(uploadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, uploadID)
}

// upload answers PUT /v1/artifact-uploads/{upload_id}. The bytes are measured
// here, kept under their own digest, and recorded with the session's owner,
// media type and requested data class (#245).
func (h *Handler) upload(w http.ResponseWriter, r *http.Request, id Identity, uploadID string) {
	if h.artifacts == nil || h.sink == nil {
		writeProblem(w, http.StatusNotImplemented, &admission.Error{Kind: admission.KindInternal, Code: "NOT_IMPLEMENTED", Title: "This operation is not available in this deployment"})
		return
	}
	session := h.ingests.take(id.Principal.ID, uploadID)
	if session == nil {
		writeProblem(w, http.StatusNotFound, &admission.Error{Kind: admission.KindNotFound, Code: "UPLOAD_NOT_FOUND", Title: "No such upload session"})
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && ct != "application/octet-stream" {
		writeProblem(w, http.StatusBadRequest, &admission.Error{Kind: admission.KindBadRequest, Code: "UNSUPPORTED_MEDIA_TYPE", Title: "The body must be application/octet-stream"})
		return
	}
	// The declared size is the bound the Client was given. Exceeding it refuses
	// the upload and keeps nothing.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, session.maxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, &admission.Error{Kind: admission.KindBadRequest, Code: "UPLOAD_TOO_LARGE", Title: "The body exceeds the declared size"})
			return
		}
		writeProblem(w, http.StatusBadRequest, &admission.Error{Kind: admission.KindBadRequest, Code: "BODY_UNREADABLE", Title: "The request body could not be read"})
		return
	}
	digest := contract.FromSHA256(sha256.Sum256(body))
	kept, size, perr := h.sink.Put(bytes.NewReader(body), digest.Hex(), session.maxBytes)
	if perr != nil {
		if errors.Is(perr, ErrDigestMismatch) {
			writeProblem(w, http.StatusUnprocessableEntity, &admission.Error{Kind: admission.KindInternal, Code: "CONTENT_IDENTITY_FAILED", Title: "The body does not match its measured digest"})
			return
		}
		h.log.Error("upload could not be kept", "error", perr.Error())
		writeProblem(w, http.StatusInternalServerError, &admission.Error{Kind: admission.KindInternal, Code: "UPLOAD_FAILED", Title: "The body could not be kept"})
		return
	}
	if size != int64(len(body)) {
		writeProblem(w, http.StatusInternalServerError, &admission.Error{Kind: admission.KindInternal, Code: "UPLOAD_FAILED", Title: "The stored size disagrees with what was received"})
		return
	}
	record := UploadRecord{
		Owner: id.Principal.ID, MediaType: session.mediaType, DataClass: session.dataClass,
		Size: size, ExpiresAt: time.Now().UTC().Add(ArtifactRetention),
	}
	if merr := h.sink.PutMeta(kept, record); merr != nil {
		// A body without its record has no owner and no retention: it could be
		// served to nobody and retired by nobody. Removing it keeps the store
		// honest rather than leaving a body this deployment cannot describe.
		// A sink that can drop the body does so: a body with no record is\n\t\t// served to nobody. A sink that cannot has already kept bytes it will\n\t\t// not describe, and that is reported rather than hidden.\n\t\tif remover, ok := h.sink.(interface{ Remove(string) error }); ok {\n\t\t\t_ = remover.Remove(kept)\n\t\t}
		h.log.Error("upload metadata not recorded", "error", merr.Error())
		writeProblem(w, http.StatusInternalServerError, &admission.Error{Kind: admission.KindInternal, Code: "UPLOAD_FAILED", Title: "The body could not be recorded"})
		return
	}
	h.ingests.finish(uploadID)
	writeJSON(w, http.StatusAccepted, artifactUploadAccepted{
		Schema: "eki.artifact-upload-accepted/v1", IngestID: session.ingestID,
		Artifact: artifactDescriptor{
			Schema: "eki.artifact-descriptor/v1", ArtifactID: kept, Digest: kept,
			SizeBytes: size, MediaType: session.mediaType, DataClass: session.dataClass,
			Facets: artifactFacets{
				// The bytes were measured here and re-measured by the store
				// before they were kept: identity is settled.
				ContentIdentity: "verified",
				// This deployment does not interpret bodies, so there is
				// nothing to pass. "passed" would be a claim nothing checked.
				SafeProcessing: "not-required",
				Provenance:     "recorded",
				Availability:   "available",
			},
		},
	})
}
