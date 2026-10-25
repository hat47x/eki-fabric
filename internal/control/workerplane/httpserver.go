package workerplane

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

// This file is the HTTP transport of the process-level TEST PROFILE. It is not
// the Worker Protocol (#198), and the session table below is not an
// enrollment or authentication decision: a Worker is not enrolled by being in
// a static table. See testprofile for the body format.

const (
	// DefaultMaxBodyBytes bounds a request body. A claim is a few hundred bytes.
	DefaultMaxBodyBytes = 64 * 1024
	// MinSessionTokenBytes is the shortest bearer token accepted. Generated
	// tokens are longer; this only refuses obviously guessable ones.
	MinSessionTokenBytes = 20
	maxSessionTokenBytes = 512
)

// SessionEntry maps the SHA-256 of a session token to the principal it
// authenticates as. Only the hash is configured, so a leaked configuration
// does not leak a usable token.
type SessionEntry struct {
	WorkerIdentity     string
	TrustDomain        string
	SessionTokenSHA256 string // lowercase hex
}

// HashSessionToken returns the lowercase hex SHA-256 of a token.
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type session struct {
	principal workerplane.Principal
	hash      [sha256.Size]byte
}

var ErrInvalidSessionConfig = errors.New("invalid Worker session table")

// HTTPConfig configures the test-profile HTTP handler.
type HTTPConfig struct {
	Claims       workerplane.ClaimClient
	Sessions     []SessionEntry
	MaxBodyBytes int64
	Logger       *slog.Logger
}

type httpHandler struct {
	claims   workerplane.ClaimClient
	sessions []session
	maxBody  int64
	log      *slog.Logger
}

// NewHTTPHandler builds the handler for testprofile.Path. The handler
// authenticates the bearer token against the table and passes the principal
// from the table, never anything from the body, to the claim service.
func NewHTTPHandler(cfg HTTPConfig) (http.Handler, error) {
	if cfg.Claims == nil {
		return nil, fmt.Errorf("%w: claim service is required", ErrInvalidConfig)
	}
	if len(cfg.Sessions) == 0 {
		return nil, fmt.Errorf("%w: at least one session is required", ErrInvalidSessionConfig)
	}
	if cfg.MaxBodyBytes < 0 {
		return nil, fmt.Errorf("%w: negative body limit", ErrInvalidConfig)
	}
	h := &httpHandler{claims: cfg.Claims, maxBody: cfg.MaxBodyBytes, log: cfg.Logger}
	if h.maxBody == 0 {
		h.maxBody = DefaultMaxBodyBytes
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	seen := map[[sha256.Size]byte]struct{}{}
	for i, e := range cfg.Sessions {
		if !validName(e.WorkerIdentity) {
			return nil, fmt.Errorf("%w: entry %d: worker_identity must be 1..256 bytes of UTF-8", ErrInvalidSessionConfig, i)
		}
		if e.TrustDomain != "" && !validName(e.TrustDomain) {
			return nil, fmt.Errorf("%w: entry %d: trust_domain must be 1..256 bytes of UTF-8", ErrInvalidSessionConfig, i)
		}
		raw, err := hex.DecodeString(e.SessionTokenSHA256)
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("%w: entry %d: session_token_sha256 must be 64 hex characters", ErrInvalidSessionConfig, i)
		}
		var sum [sha256.Size]byte
		copy(sum[:], raw)
		if _, dup := seen[sum]; dup {
			return nil, fmt.Errorf("%w: entry %d: duplicate token", ErrInvalidSessionConfig, i)
		}
		seen[sum] = struct{}{}
		h.sessions = append(h.sessions, session{
			principal: workerplane.Principal{WorkerIdentity: e.WorkerIdentity, TrustDomain: e.TrustDomain},
			hash:      sum,
		})
	}
	return h, nil
}

func validName(v string) bool { return len(v) > 0 && len(v) <= 256 && utf8.ValidString(v) }

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != testprofile.Path {
		reply(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		reply(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	// Authenticate before reading the body, so an unauthenticated caller
	// cannot make the server read or parse anything.
	principal, ok := h.authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		reply(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != testprofile.ContentType {
		reply(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			reply(w, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		reply(w, http.StatusBadRequest, "bad_request")
		return
	}
	claim, err := testprofile.DecodeClaim(body)
	if err != nil {
		reply(w, http.StatusBadRequest, "invalid_claim")
		return
	}
	ack, err := h.claims.SubmitClaim(r.Context(), principal, claim)
	if err != nil {
		status, code := classify(err)
		if status == http.StatusInternalServerError {
			// Details stay in the log; the caller learns only the code.
			h.log.Error("worker claim failed", "worker", principal.WorkerIdentity, "error", err.Error())
		}
		reply(w, status, code)
		return
	}
	out, err := testprofile.EncodeAck(ack)
	if err != nil {
		reply(w, http.StatusInternalServerError, "internal_error")
		return
	}
	w.Header().Set("Content-Type", testprofile.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}

func (h *httpHandler) authenticate(r *http.Request) (workerplane.Principal, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") ||
		len(token) < MinSessionTokenBytes || len(token) > maxSessionTokenBytes {
		return workerplane.Principal{}, false
	}
	sum := sha256.Sum256([]byte(token))
	// Compare against every entry without stopping at the first match, so the
	// time taken does not reveal which entry matched.
	var match *session
	for i := range h.sessions {
		if subtle.ConstantTimeCompare(sum[:], h.sessions[i].hash[:]) == 1 {
			match = &h.sessions[i]
		}
	}
	if match == nil {
		return workerplane.Principal{}, false
	}
	return match.principal, true
}

// classify maps an error to a status and a fixed code. The code never carries
// the error text: store and binding errors name Attempts, Leases and Workers.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, claimplane.ErrNotClaimOwner):
		return http.StatusForbidden, "not_claim_owner"
	case errors.Is(err, resultclaim.ErrInvalidClaim):
		return http.StatusBadRequest, "invalid_claim"
	case errors.Is(err, statestore.ErrNotFound):
		return http.StatusNotFound, "attempt_not_found"
	case errors.Is(err, resultclaim.ErrConflict), errors.Is(err, statestore.ErrConflict),
		errors.Is(err, claimaccept.ErrBindingMismatch), errors.Is(err, claimaccept.ErrWrongPhase):
		return http.StatusConflict, "claim_not_applied"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable, "unavailable"
	}
	return http.StatusInternalServerError, "internal_error"
}

func reply(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", testprofile.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, code)
}
