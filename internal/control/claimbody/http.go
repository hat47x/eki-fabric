package claimbody

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

// HandlerMaxBodyBytes is the largest body the HTTP receiver will read. The
// WorkOrder's maximum is the binding; this cap is the transport's guard
// against a caller that never stops writing.
const HandlerMaxBodyBytes = testprofile.MaxKnownBodyBytes

// HTTPReceiver receives a claim's body over HTTP. It is mounted on the
// Worker's mTLS listener, next to the claim report; the transport produces
// the authenticated Principal and this receiver never trusts a field of the
// envelope beyond routing.
type HTTPReceiver struct {
	receive func(ctx context.Context, attemptID ids.AttemptID, worker string, body []byte, mediaType string) error
}

// NewHTTPReceiver wires an HTTP receiver to a Receive function. The function
// is expected to measure the body against the recorded claim and to keep it
// only when they agree.
func NewHTTPReceiver(receive func(ctx context.Context, attemptID ids.AttemptID, worker string, body []byte, mediaType string) error) (*HTTPReceiver, error) {
	if receive == nil {
		return nil, fmt.Errorf("%w: a receive function is required", ErrInvalidConfig)
	}
	return &HTTPReceiver{receive: receive}, nil
}

// ServeHTTP answers one claim-body request: a JSON envelope, then the raw
// body bytes to the end of the request. The answers are deliberately coarse:
// the reason for a refusal stays in the receiver's log, and the caller learns
// the code.
func (h *HTTPReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	if mt, _, _ := cutContentType(r); mt != testprofile.ContentType {
		http.Error(w, "unsupported_media_type", http.StatusUnsupportedMediaType)
		return
	}
	reader := bufio.NewReader(io.LimitReader(r.Body, HandlerMaxBodyBytes+1))
	// The envelope is the request's first line, newline-delimited; the rest
	// of the request is the raw body. A JSON decoder would buffer ahead and
	// eat the body's first bytes, so the line is read by hand.
	line, err := reader.ReadString('\n')
	if err != nil {
		http.Error(w, "invalid_envelope", http.StatusBadRequest)
		return
	}
	var envelope struct {
		AttemptID ids.AttemptID `json:"attempt_id"`
		MediaType string        `json:"media_type,omitempty"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		http.Error(w, "invalid_envelope", http.StatusBadRequest)
		return
	}
	if envelope.AttemptID == "" {
		http.Error(w, "attempt_required", http.StatusBadRequest)
		return
	}
	if _, err := ids.ParseAttemptID(envelope.AttemptID.String()); err != nil {
		http.Error(w, "attempt_invalid", http.StatusBadRequest)
		return
	}
	mediaType := envelope.MediaType
	if mediaType == "" {
		mediaType = testprofile.MediaTypeOctetStream
	}
	// The worker identity comes from the transport, not the envelope: the
	// caller is who the certificate says. The ServeHTTP signature does not
	// carry it, so the mount point passes it in.
	worker := workerFromContext(r.Context())
	if worker == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		http.Error(w, "body_read_failed", http.StatusBadRequest)
		return
	}
	if err := h.receive(r.Context(), envelope.AttemptID, worker, body, mediaType); err != nil {
		switch {
		case errors.Is(err, ErrWorkerMismatch):
			http.Error(w, "worker_mismatch", http.StatusForbidden)
		case errors.Is(err, ErrNoCompletedClaim):
			http.Error(w, "no_completed_claim", http.StatusConflict)
		case errors.Is(err, ErrDigestMismatch):
			http.Error(w, "digest_mismatch", http.StatusUnprocessableEntity)
		case errors.Is(err, ErrTooLarge):
			http.Error(w, "body_too_large", http.StatusRequestEntityTooLarge)
		case errors.Is(err, ErrUnknownAttempt):
			http.Error(w, "attempt_unknown", http.StatusNotFound)
		default:
			http.Error(w, "internal_error", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// workerContextKey carries the authenticated Worker identity from the mount
// point (which gets it from the certificate) into ServeHTTP.
type workerContextKey struct{}

// WithWorker returns a context that names the authenticated Worker.
func WithWorker(ctx context.Context, worker string) context.Context {
	return context.WithValue(ctx, workerContextKey{}, worker)
}

func workerFromContext(ctx context.Context) string {
	worker, _ := ctx.Value(workerContextKey{}).(string)
	return worker
}

func cutContentType(r *http.Request) (string, string, bool) {
	mt := r.Header.Get("Content-Type")
	for i := 0; i < len(mt); i++ {
		if mt[i] == ';' {
			return trimSpace(mt[:i]), trimSpace(mt[i+1:]), true
		}
	}
	return trimSpace(mt), "", false
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
