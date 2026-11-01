// Package clientapi serves the P0 External Client Plane (spec/external/openapi.json)
// over HTTPS and JSON.
//
// It is the last boundary before Core: it authenticates, bounds the request,
// calls admission, and projects the result. It never returns Worker identity,
// Attempts, Leases, or policy revisions, because admission never gives them to
// it.
package clientapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/admission"
)

// DefaultMaxBodyBytes bounds a request body. A valid TaskRequest is a few
// kilobytes; this is generous for parameters and still small enough that a
// flood of maximum-size requests is survivable.
const DefaultMaxBodyBytes = 256 * 1024

const (
	problemContentType = "application/problem+json"
	jsonContentType    = "application/json"
)

// Config wires the Handler.
type Config struct {
	Service       *admission.Service
	Authenticator Authenticator
	// MaxBodyBytes bounds request bodies. Zero means DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// Logger receives one line per request. It never receives tokens or bodies.
	// Nil discards.
	Logger *slog.Logger
	// Ready is asked by /readyz. Nil means /readyz answers 503 (fail closed).
	// The probe is answered without authentication; the function must not
	// put state into the answer — the endpoint reduces it to ok/503 itself.
	Ready func() bool
}

type Handler struct {
	svc     *admission.Service
	auth    Authenticator
	maxBody int64
	log     *slog.Logger

	// ready reports whether the process is serving for real: the state store
	// is open, the migrations ran, and the configured listeners are up. nil
	// means the process carries no readiness information and /readyz refuses
	// (fail closed).
	ready func() bool
}

var ErrInvalidConfig = errors.New("invalid client API configuration")

func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Service == nil {
		return nil, fmt.Errorf("%w: a service is required", ErrInvalidConfig)
	}
	if cfg.Authenticator == nil {
		return nil, fmt.Errorf("%w: an authenticator is required; the API is never served anonymously", ErrInvalidConfig)
	}
	if cfg.MaxBodyBytes < 0 {
		return nil, fmt.Errorf("%w: negative body limit", ErrInvalidConfig)
	}
	h := &Handler{svc: cfg.Service, auth: cfg.Authenticator, maxBody: cfg.MaxBodyBytes, log: cfg.Logger, ready: cfg.Ready}
	if h.maxBody == 0 {
		h.maxBody = DefaultMaxBodyBytes
	}
	if h.log == nil {
		h.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return h, nil
}

type route int

const (
	routeNone route = iota
	routeSubmitTask
	routeGetTask
	routeCancelTask
	routeGetResult
	routeNotImplemented
)

type matched struct {
	route  route
	taskID string
	allow  string
}

func match(path string) matched {
	switch {
	case path == "/v1/tasks":
		return matched{route: routeSubmitTask, allow: http.MethodPost}
	case path == "/v1/artifacts/ingest-intents":
		return matched{route: routeNotImplemented, allow: http.MethodPost}
	}
	if rest, ok := strings.CutPrefix(path, "/v1/tasks/"); ok && rest != "" {
		id, tail, hasTail := strings.Cut(rest, "/")
		switch {
		case !hasTail:
			return matched{route: routeGetTask, taskID: id, allow: http.MethodGet}
		case tail == "cancel":
			return matched{route: routeCancelTask, taskID: id, allow: http.MethodPost}
		case tail == "result":
			return matched{route: routeGetResult, taskID: id, allow: http.MethodGet}
		}
		return matched{}
	}
	if rest, ok := strings.CutPrefix(path, "/v1/artifacts/"); ok && rest != "" && !strings.Contains(rest, "/") {
		return matched{route: routeNotImplemented, allow: http.MethodGet}
	}
	if rest, ok := strings.CutPrefix(path, "/v1/artifact-uploads/"); ok && rest != "" && !strings.Contains(rest, "/") {
		return matched{route: routeNotImplemented, allow: http.MethodPut}
	}
	return matched{}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The readiness probes are answered before authentication on purpose
	// (#289): the process that waits for the Control Plane to come up (an
	// integration harness, an operator's start script) holds no Bearer token,
	// and the answers carry no state — a fixed body per probe, no identifiers,
	// no versions, no counts.
	switch r.URL.Path {
	case "/healthz":
		setSecurityHeaders(w)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed\n", http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
		return
	case "/readyz":
		setSecurityHeaders(w)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed\n", http.StatusMethodNotAllowed)
			return
		}
		if h.ready == nil || !h.ready() {
			http.Error(w, "not ready\n", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	principal := "-"

	defer func() {
		if p := recover(); p != nil {
			h.log.Error("panic serving request", "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprint(p))
			if rec.status == http.StatusOK {
				writeProblem(rec, http.StatusInternalServerError, &admission.Error{
					Kind: admission.KindInternal, Code: "INTERNAL_ERROR",
					Title: "The service could not complete the request", Retryable: true,
				})
			}
		}
		h.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"principal", principal, "duration_ms", time.Since(start).Milliseconds())
	}()

	setSecurityHeaders(rec)

	m := match(r.URL.Path)
	if m.route == routeNone {
		writeProblem(rec, http.StatusNotFound, &admission.Error{Kind: admission.KindNotFound, Code: "NOT_FOUND", Title: "No such resource"})
		return
	}

	identity, err := h.auth.Authenticate(r)
	if err != nil {
		rec.Header().Set("WWW-Authenticate", `Bearer realm="eki"`)
		writeProblem(rec, http.StatusUnauthorized, &admission.Error{Kind: admission.KindForbidden, Code: "AUTHENTICATION_REQUIRED", Title: "Authentication is required"})
		return
	}
	principal = identity.Principal.ID
	if identity.Role != RoleClient {
		writeProblem(rec, http.StatusForbidden, &admission.Error{Kind: admission.KindForbidden, Code: "CLIENT_ROLE_REQUIRED", Title: "This credential cannot use the client API"})
		return
	}
	if r.Method != m.allow {
		rec.Header().Set("Allow", m.allow)
		writeProblem(rec, http.StatusMethodNotAllowed, &admission.Error{Kind: admission.KindBadRequest, Code: "METHOD_NOT_ALLOWED", Title: "Method not allowed"})
		return
	}

	switch m.route {
	case routeSubmitTask:
		h.submit(rec, r, identity)
	case routeGetTask:
		h.status(rec, r, identity, m.taskID)
	case routeCancelTask:
		h.cancel(rec, r, identity, m.taskID)
	case routeGetResult:
		if !h.svc.ResultsEnabled() {
			writeProblem(rec, http.StatusNotImplemented, &admission.Error{Kind: admission.KindInternal, Code: "NOT_IMPLEMENTED", Title: "This operation is not available in this deployment"})
			return
		}
		h.result(rec, r, identity, m.taskID)
	default:
		writeProblem(rec, http.StatusNotImplemented, &admission.Error{Kind: admission.KindInternal, Code: "NOT_IMPLEMENTED", Title: "This operation is not available in this deployment"})
	}
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func idempotencyKey(r *http.Request) (string, *admission.Error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", &admission.Error{Kind: admission.KindBadRequest, Code: "IDEMPOTENCY_KEY_REQUIRED", Title: "Exactly one Idempotency-Key header is required"}
	}
	if !admission.ValidIdempotencyKey(values[0]) {
		return "", admission.InvalidIdempotencyKey()
	}
	return values[0], nil
}

type taskAccepted struct {
	Schema      string `json:"schema"`
	TaskID      string `json:"task_id"`
	Status      string `json:"status"`
	SubmittedAt string `json:"submitted_at"`
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request, id Identity) {
	key, kerr := idempotencyKey(r)
	if kerr != nil {
		writeProblem(w, statusFor(kerr), kerr)
		return
	}
	if !isJSON(r.Header.Get("Content-Type")) {
		// 400 rather than 415: P0 declares no 415 for this operation.
		writeProblem(w, http.StatusBadRequest, &admission.Error{Kind: admission.KindBadRequest, Code: "UNSUPPORTED_MEDIA_TYPE", Title: "The request body must be application/json"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, &admission.Error{Kind: admission.KindBadRequest, Code: "REQUEST_TOO_LARGE", Title: "The request exceeds the size limit"})
			return
		}
		writeProblem(w, http.StatusBadRequest, &admission.Error{Kind: admission.KindBadRequest, Code: "BODY_UNREADABLE", Title: "The request body could not be read"})
		return
	}

	accepted, err := h.svc.Submit(r.Context(), id.Principal, key, body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, taskAccepted{
		Schema:      "eki.task-accepted/v1",
		TaskID:      accepted.TaskID.String(),
		Status:      "queued",
		SubmittedAt: accepted.SubmittedAt.UTC().Format(time.RFC3339Nano),
	})
}

type taskPublicStatus struct {
	Schema        string `json:"schema"`
	TaskID        string `json:"task_id"`
	Status        string `json:"status"`
	SubmittedAt   string `json:"submitted_at"`
	UpdatedAt     string `json:"updated_at"`
	TerminalCode  string `json:"terminal_code,omitempty"`
	PublicMessage string `json:"public_message,omitempty"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request, id Identity, taskID string) {
	status, err := h.svc.Status(r.Context(), id.Principal, taskID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskPublicStatus{
		Schema:        "eki.task-public-status/v1",
		TaskID:        status.TaskID.String(),
		Status:        status.Status,
		SubmittedAt:   status.SubmittedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     status.UpdatedAt.UTC().Format(time.RFC3339Nano),
		TerminalCode:  status.TerminalCode,
		PublicMessage: status.PublicMessage,
	})
}

// taskResult is eki.task-result/v1. Artifacts is always present (the schema
// requires it) and is empty: the AcceptedResult records no artifact size, media
// type or data class, so no ArtifactDescriptor can be built from it truthfully.
// OutputDigest is the digest the durable AcceptedResult recorded.
type taskResult struct {
	Schema       string                 `json:"schema"`
	TaskID       string                 `json:"task_id"`
	Artifacts    []struct{}             `json:"artifacts"`
	Assurance    publicAssuranceSummary `json:"assurance"`
	OutputDigest publicDigest           `json:"output_digest"`
}

// publicDigest is the Digest schema of the OpenAPI contract.
type publicDigest struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type publicAssuranceSummary struct {
	Profile string `json:"profile"`
	Status  string `json:"status"`
}

func (h *Handler) result(w http.ResponseWriter, r *http.Request, id Identity, taskID string) {
	res, err := h.svc.Result(r.Context(), id.Principal, taskID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskResult{
		Schema:       "eki.task-result/v1",
		TaskID:       res.TaskID.String(),
		Artifacts:    []struct{}{},
		Assurance:    publicAssuranceSummary{Profile: res.AssuranceProfile, Status: "satisfied"},
		OutputDigest: publicDigest{Algorithm: res.OutputDigest.Algorithm(), Value: res.OutputDigest.Hex()},
	})
}

type cancelAccepted struct {
	Schema string `json:"schema"`
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request, id Identity, taskID string) {
	if _, kerr := idempotencyKey(r); kerr != nil {
		writeProblem(w, statusFor(kerr), kerr)
		return
	}
	// Cancellation is naturally idempotent, so the key is required by the
	// contract but not stored: repeating it never changes the outcome.
	accepted, err := h.svc.Cancel(r.Context(), id.Principal, taskID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, cancelAccepted{
		Schema: "eki.task-cancel-accepted/v1",
		TaskID: accepted.TaskID.String(),
		Status: "cancellation-requested",
	})
}

// fail answers an error from admission. Anything that is not an
// *admission.Error is a bug or a cancelled request; the Client gets no detail.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *admission.Error
	if errors.As(err, &ae) {
		if ae.Retryable && ae.Kind == admission.KindTooManyRequests {
			w.Header().Set("Retry-After", "5")
		}
		writeProblem(w, statusFor(ae), ae)
		return
	}
	if r.Context().Err() != nil {
		// The Client went away or timed out. Nothing useful can be sent.
		writeProblem(w, http.StatusServiceUnavailable, &admission.Error{Kind: admission.KindInternal, Code: "REQUEST_CANCELLED", Title: "The request was cancelled", Retryable: true})
		return
	}
	h.log.Error("unexpected error from admission", "error", err.Error())
	writeProblem(w, http.StatusInternalServerError, &admission.Error{Kind: admission.KindInternal, Code: "INTERNAL_ERROR", Title: "The service could not complete the request", Retryable: true})
}

func statusFor(e *admission.Error) int {
	switch e.Kind {
	case admission.KindBadRequest:
		return http.StatusBadRequest
	case admission.KindForbidden:
		return http.StatusForbidden
	case admission.KindNotFound:
		return http.StatusNotFound
	case admission.KindConflict:
		return http.StatusConflict
	case admission.KindUnprocessable:
		return http.StatusUnprocessableEntity
	case admission.KindTooManyRequests:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

func isJSON(contentType string) bool {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != jsonContentType {
		return false
	}
	for name, value := range params {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

// problem is RFC 9457 Problem Details with the stable EKI extensions of the P0
// contract. Clients branch on status and code, never on detail.
type problem struct {
	Type                 string `json:"type"`
	Title                string `json:"title"`
	Status               int    `json:"status"`
	Detail               string `json:"detail,omitempty"`
	Code                 string `json:"code"`
	Retryable            *bool  `json:"retryable,omitempty"`
	PolicyReasonCategory string `json:"policy_reason_category,omitempty"`
	TaskID               string `json:"task_id,omitempty"`
}

func writeProblem(w http.ResponseWriter, status int, e *admission.Error) {
	p := problem{
		Type:                 "urn:eki:problem:" + strings.ToLower(strings.ReplaceAll(e.Code, "_", "-")),
		Title:                e.Title,
		Status:               status,
		Code:                 e.Code,
		PolicyReasonCategory: e.PolicyReasonCategory,
		TaskID:               e.TaskID,
	}
	// Internal failures carry no detail: it could describe our own state.
	if e.Kind != admission.KindInternal {
		p.Detail = truncateDetail(e.Detail)
	}
	if e.Retryable || e.Kind == admission.KindInternal {
		retryable := e.Retryable
		p.Retryable = &retryable
	}
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

func truncateDetail(s string) string {
	const max = 2048
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
