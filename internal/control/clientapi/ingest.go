package clientapi

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/admission"
)

// MaxIngestBytes is the largest body one ingest may declare. A Client that
// declares more is refused at the declaration, before it sends anything
// (#245, spec/external/openapi.json /v1/artifacts/ingest-intents).
const MaxIngestBytes int64 = 64 << 20

// IngestSessionLifetime bounds how long an upload session stays usable. A session
// that never expires would be a standing permission to write.
const IngestSessionLifetime = 15 * time.Minute

// ErrInvalidIngest is the refusal of a declaration this deployment cannot honour.
var ErrInvalidIngest = errors.New("clientapi: invalid ingest intent")

const ingestIntentSchema = "eki.artifact-ingest-intent-request/v1"

// ingestRequest is the declaration a Client makes before sending a body.
type ingestRequest struct {
	Schema             string `json:"schema"`
	DeclaredSizeBytes  int64  `json:"declared_size_bytes"`
	MediaType          string `json:"media_type"`
	RequestedDataClass string `json:"requested_data_class"`
	// ExpectedDigest is optional. When present, the uploaded body must match
	// this digest (#803).
	ExpectedDigest *expectedDigest `json:"expected_digest"`
}

type expectedDigest struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// ingestResponse is the answer to a declaration: a named session to upload into.
type ingestResponse struct {
	Schema   string        `json:"schema"`
	IngestID string        `json:"ingest_id"`
	Upload   uploadSession `json:"upload"`
}

type uploadSession struct {
	UploadID  string    `json:"upload_id"`
	Method    string    `json:"method"`
	Href      string    `json:"href"`
	ExpiresAt time.Time `json:"expires_at"`
	MaxBytes  int64     `json:"max_bytes"`
}

const ingestResponseSchema = "eki.artifact-ingest-intent-response/v1"

// ingestSession is one open upload. It lives in memory on purpose: the bytes are
// not written until the upload arrives, so a session that is lost in a restart
// costs the Client a new declaration and nothing else (#245).
type ingestSession struct {
	principal string
	ingestID  string
	uploadID  string
	mediaType string
	dataClass string
	maxBytes  int64
	expiresAt time.Time
	// answer is kept so a replayed Idempotency-Key returns the same session.
	answer ingestResponse
}

// ingestSessions holds the open sessions and the idempotency records.
type ingestSessions struct {
	mu      sync.Mutex
	byKey   map[string]ingestResponse
	byKeyReq map[string]ingestRequest
	byID    map[string]*ingestSession
	now     func() time.Time
}

func newIngestSessions(now func() time.Time) *ingestSessions {
	if now == nil {
		now = time.Now
	}
	return &ingestSessions{byKey: map[string]ingestResponse{}, byKeyReq: map[string]ingestRequest{}, byID: map[string]*ingestSession{}, now: now}
}

// validate checks a declaration before any session is created.
func (s *ingestSessions) validate(body []byte) (ingestRequest, error) {
	var req ingestRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return ingestRequest{}, fmt.Errorf("%w: %v", ErrInvalidIngest, err)
	}
	switch {
	case req.Schema != ingestIntentSchema:
		return ingestRequest{}, fmt.Errorf("%w: schema must be %s", ErrInvalidIngest, ingestIntentSchema)
	case req.DeclaredSizeBytes < 0:
		return ingestRequest{}, fmt.Errorf("%w: declared_size_bytes must not be negative", ErrInvalidIngest)
	case req.DeclaredSizeBytes > MaxIngestBytes:
		return req, fmt.Errorf("%w: declared_size_bytes exceeds %d", ErrInvalidIngest, MaxIngestBytes)
	case req.MediaType == "" || len(req.MediaType) > 255:
		return ingestRequest{}, fmt.Errorf("%w: media_type must be 1..255 bytes", ErrInvalidIngest)
	case !validMediaType(req.MediaType):
		return ingestRequest{}, fmt.Errorf("%w: media_type must be type/subtype", ErrInvalidIngest)
	case req.RequestedDataClass == "" || !admission.ValidDataClass(req.RequestedDataClass):
		return ingestRequest{}, fmt.Errorf("%w: requested_data_class is not in the vocabulary", ErrInvalidIngest)
	case req.ExpectedDigest != nil:
		if req.ExpectedDigest.Algorithm != "sha256" {
			return ingestRequest{}, fmt.Errorf("%w: expected_digest.algorithm must be sha256", ErrInvalidIngest)
		}
		if len(req.ExpectedDigest.Value) != 64 {
			return ingestRequest{}, fmt.Errorf("%w: expected_digest.value must be 64 hex characters", ErrInvalidIngest)
		}
		if _, err := hex.DecodeString(req.ExpectedDigest.Value); err != nil {
			return ingestRequest{}, fmt.Errorf("%w: expected_digest.value must be hex", ErrInvalidIngest)
		}
	}
	return req, nil
}

// validMediaType reports whether the value looks like a MIME media type. The
// declaration is a hint, not a security boundary: EKI never inspects the bytes,
// so the check is only to catch obvious typos (RFC 6838 type/subtype).
func validMediaType(mt string) bool {
	parts := strings.SplitN(mt, "/", 2)
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

// open returns the session for this declaration, creating one unless the same
// Idempotency-Key was already used by this principal with different content.
func (s *ingestSessions) open(principal, key string, req ingestRequest) (ingestResponse, bool, error) {
	scope := principal + "\x00" + key
	now := s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	if answer, ok := s.byKey[scope]; ok {
		if old, hasOld := s.byKeyReq[scope]; hasOld && !sameIngestRequest(old, req) {
			return ingestResponse{}, false, &admission.Error{Kind: admission.KindConflict, Code: "IDEMPOTENCY_KEY_CONFLICT", Title: "The Idempotency-Key was already used with different content"}
		}
		return answer, true, nil
	}
	s.expire(now)
	ingestID, err := newOpaqueID()
	if err != nil {
		return ingestResponse{}, false, err
	}
	uploadID, err := newOpaqueID()
	if err != nil {
		return ingestResponse{}, false, err
	}
	answer := ingestResponse{
		Schema: ingestResponseSchema, IngestID: ingestID,
		Upload: uploadSession{
			UploadID: uploadID, Method: http.MethodPut,
			// The Client cannot choose where the bytes go: the path is the
			// session's own name (#245).
			Href:      "/v1/artifact-uploads/" + uploadID,
			ExpiresAt: now.Add(IngestSessionLifetime),
			MaxBytes:  req.DeclaredSizeBytes,
		},
	}
	s.byKey[scope] = answer
	s.byKeyReq[scope] = req
	s.byID[uploadID] = &ingestSession{
		principal: principal, ingestID: ingestID, uploadID: uploadID,
		mediaType: req.MediaType, dataClass: req.RequestedDataClass,
		maxBytes: req.DeclaredSizeBytes, expiresAt: answer.Upload.ExpiresAt, answer: answer,
	}
	return answer, false, nil
}

// sameIngestRequest reports whether two declarations are the same for idempotency.
// The comparison is case-insensitive for media_type because HTTP media types are
// case-insensitive (RFC 6838 §4.2).
func sameIngestRequest(a, b ingestRequest) bool {
	if a.DeclaredSizeBytes != b.DeclaredSizeBytes {
		return false
	}
	if !strings.EqualFold(a.MediaType, b.MediaType) {
		return false
	}
	if a.RequestedDataClass != b.RequestedDataClass {
		return false
	}
	if (a.ExpectedDigest == nil) != (b.ExpectedDigest == nil) {
		return false
	}
	if a.ExpectedDigest != nil && b.ExpectedDigest != nil {
		if a.ExpectedDigest.Algorithm != b.ExpectedDigest.Algorithm {
			return false
		}
		if a.ExpectedDigest.Value != b.ExpectedDigest.Value {
			return false
		}
	}
	return true
}

// expire drops sessions whose window passed. It is called under the lock.
func (s *ingestSessions) expire(now time.Time) {
	for id, session := range s.byID {
		if now.After(session.expiresAt) {
			delete(s.byID, id)
		}
	}
}

// newOpaqueID returns an identifier that carries nothing about the deployment.
func newOpaqueID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "ing_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// ingest handles the declaration. The body is uploaded in a later step, so this
// answer names the session and where it goes (#245).
func (h *Handler) ingest(w http.ResponseWriter, r *http.Request, id Identity) {
	if h.artifacts == nil {
		// A deployment that keeps no bodies cannot honour an upload: naming a
		// session the process would never serve is a promise it cannot keep.
		writeProblem(w, http.StatusNotImplemented, &admission.Error{Kind: admission.KindInternal, Code: "NOT_IMPLEMENTED", Title: "This operation is not available in this deployment"})
		return
	}
	key, kerr := idempotencyKey(r)
	if kerr != nil {
		writeProblem(w, statusFor(kerr), kerr)
		return
	}
	if !isJSON(r.Header.Get("Content-Type")) {
		writeProblem(w, http.StatusBadRequest, &admission.Error{Kind: admission.KindBadRequest, Code: "UNSUPPORTED_MEDIA_TYPE", Title: "The request body must be application/json"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, http.StatusBadRequest, &admission.Error{Kind: admission.KindBadRequest, Code: "REQUEST_TOO_LARGE", Title: "The request exceeds the size limit"})
			return
		}
		writeProblem(w, http.StatusBadRequest, &admission.Error{Kind: admission.KindBadRequest, Code: "BODY_UNREADABLE", Title: "The request body could not be read"})
		return
	}
	req, verr := h.ingests.validate(body)
	if verr != nil {
		// A declaration above the cap is its own answer: the Client learns the
		// limit, not just "bad request".
		status := http.StatusBadRequest
		if req.DeclaredSizeBytes > MaxIngestBytes {
			status = http.StatusRequestEntityTooLarge
		}
		writeProblem(w, status, &admission.Error{
			Kind: admission.KindBadRequest, Code: "INVALID_INGEST_INTENT",
			Title: "The ingest declaration is not usable", Detail: verr.Error(),
		})
		return
	}
	answer, replayed, oerr := h.ingests.open(id.Principal.ID, key, req)
	if oerr != nil {
		var ae *admission.Error
		if errors.As(oerr, &ae) && ae.Kind == admission.KindConflict {
			writeProblem(w, statusFor(ae), ae)
			return
		}
		writeProblem(w, http.StatusInternalServerError, &admission.Error{Kind: admission.KindInternal, Code: "INGEST_UNAVAILABLE", Title: "The ingest session could not be created"})
		return
	}
	_ = replayed
	writeJSON(w, http.StatusCreated, answer)
}
