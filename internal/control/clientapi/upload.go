package clientapi

import (
	"crypto/sha256"
	"errors"
	"io"
	"net"
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

// isTimeoutError reports whether err is a read timeout from the HTTP server.
// It is used to distinguish a slow network from a corrupted body (#816).
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

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
		scope := session.principal + "\x00" + session.key
		delete(s.byID, uploadID)
		delete(s.byKey, scope)
		delete(s.byKeyReq, scope)
		s.perPrincipal[session.principal]--
		if s.perPrincipal[session.principal] <= 0 {
			delete(s.perPrincipal, session.principal)
		}
		return nil
	}
	return session
}

// finish removes the session once its bytes were kept: a session is one upload.
func (s *ingestSessions) finish(uploadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.byID[uploadID]
	if !ok {
		return
	}
	scope := session.principal + "\x00" + session.key
	delete(s.byID, uploadID)
	delete(s.byKey, scope)
	delete(s.byKeyReq, scope)
	s.perPrincipal[session.principal]--
	if s.perPrincipal[session.principal] <= 0 {
		delete(s.perPrincipal, session.principal)
	}
}

// upload answers PUT /v1/artifact-uploads/{upload_id}. The bytes are streamed
// to the sink, measured there, and recorded with the session's owner, media type
// and requested data class (#245, #816).
//
// The body is NOT buffered in memory: it is piped through an io.TeeReader into
// the sink. This means the handler's memory footprint is bounded by the HTTP
// buffer size, not the artifact size. The sink is responsible for enforcing
// maxBytes and computing the digest.
//
// A read timeout from the server is reported as 504 UPLOAD_TIMEOUT so the
// Client can distinguish "network too slow" from "body is corrupted" (#816).
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
	// the upload and keeps nothing. The body is streamed directly to the sink so
	// the handler does not buffer the whole artifact in memory (#816).
	hash := sha256.New()
	limitedBody := http.MaxBytesReader(w, r.Body, session.maxBytes)
	tee := io.TeeReader(limitedBody, hash)
	kept, size, perr := h.sink.Put(tee, "", session.maxBytes)
	if perr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(perr, &tooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, &admission.Error{Kind: admission.KindBadRequest, Code: "UPLOAD_TOO_LARGE", Title: "The body exceeds the declared size"})
			return
		}
		if isTimeoutError(perr) {
			// A read timeout is a transport failure, not a bad body. Separating
			// it from BODY_UNREADABLE lets the Client retry without changing
			// the artifact (#816).
			h.log.Error("upload body read timed out", "error", perr.Error())
			writeProblem(w, http.StatusGatewayTimeout, &admission.Error{Kind: admission.KindInternal, Code: "UPLOAD_TIMEOUT", Title: "The request body could not be read within the server timeout"})
			return
		}
		if errors.Is(perr, ErrDigestMismatch) {
			writeProblem(w, http.StatusUnprocessableEntity, &admission.Error{Kind: admission.KindUnprocessable, Code: "CONTENT_IDENTITY_FAILED", Title: "The body does not match its expected digest"})
			return
		}
		h.log.Error("upload could not be kept", "error", perr.Error())
		writeProblem(w, http.StatusInternalServerError, &admission.Error{Kind: admission.KindInternal, Code: "UPLOAD_FAILED", Title: "The body could not be kept"})
		return
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	measured := contract.FromSHA256(sum)
	if measured.Hex() != kept {
		h.log.Error("upload digest disagreement with sink", "measured", measured.Hex(), "kept", kept)
		writeProblem(w, http.StatusInternalServerError, &admission.Error{Kind: admission.KindInternal, Code: "UPLOAD_FAILED", Title: "The body digest disagrees with the store"})
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
		// A sink that can drop the body does so. A sink that cannot has already
		// kept bytes it will not describe; that is reported in the log rather
		// than hidden.
		if remover, ok := h.sink.(interface{ Remove(string) error }); ok {
			if rerr := remover.Remove(kept); rerr != nil {
				h.log.Error("upload body could not be removed after a metadata failure", "error", rerr.Error())
			}
		} else {
			h.log.Error("upload body kept without a record: the sink cannot remove it", "digest", kept)
		}
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
				// The bytes were measured by the store and bound to the digest
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
