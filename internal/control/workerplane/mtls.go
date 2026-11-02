package workerplane

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
	"github.com/hat47x/eki-fabric/internal/control/claimbody"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workercert"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

// This file is the Worker listener of ADR-0027: Worker authentication by a
// client certificate (mTLS), the enrollment route, and the renewal route. It
// is the IDV-1 adapter for Workers: it turns a verified certificate into the
// Principal and nothing else.
//
// The listener's tls.Config asks for a client certificate without verifying
// it (tls.RequestClientCert). Verification happens here, on every request, for
// two reasons: the enrollment route needs no certificate, and a connection that
// outlives its certificate or its registry entry must not keep working.

const (
	// MaxEnrollBodyBytes bounds an enroll or renew body.
	MaxEnrollBodyBytes = enrollwire.MaxCSRBytes
)

// ErrInvalidMTLSConfig is returned for an incomplete MTLSConfig.
var ErrInvalidMTLSConfig = errors.New("invalid Worker mTLS configuration")

// MTLSConfig configures NewMTLSHandler.
type MTLSConfig struct {
	Claims workerplane.ClaimClient
	// BodyReceiver receives the body a Worker sends after its claim report
	// (ADR-0034 §2.2). Nil means the route does not exist (404).
	BodyReceiver BodyReceiver
	// Assignments serves the assignment route of the test profile. Nil means
	// the route does not exist (404).
	Assignments workerplane.AssignmentSource
	// Heartbeats records the running-Lease reports the assignments requests
	// carry (ADR-0030 §2.2). Nil means reports are accepted and dropped.
	Heartbeats workerplane.HeartbeatRecorder
	// Registry authorizes every request and runs enrollment and renewal. It
	// must have a certificate issuer.
	Registry *workerenrollment.Service
	// WorkerCA is the only trust anchor for client certificates. Its name
	// constraint limits it to TrustDomain.
	WorkerCA *x509.Certificate
	// TrustDomain is the trust domain this listener serves. A certificate for
	// any other domain, including a sub-domain, is refused.
	TrustDomain string
	// Clock is the time against which certificates are verified. Nil means
	// the system clock.
	Clock        func() time.Time
	MaxBodyBytes int64
	Logger       *slog.Logger
}

type mtlsHandler struct {
	claimEndpoint
	bodies      BodyReceiver
	assignments workerplane.AssignmentSource
	// heartbeats records the running-Lease reports the assignments request
	// carries (ADR-0030 §2.2). Nil means the reports are accepted and
	// dropped, which keeps the route usable for a Control Plane that does
	// not track liveness yet.
	heartbeats  workerplane.HeartbeatRecorder
	registry    *workerenrollment.Service
	roots       *x509.CertPool
	caPEM       string
	trustDomain string
	now         func() time.Time
}

// NewMTLSHandler builds the handler of the Worker listener in mTLS mode. It
// serves the claim route (client certificate required), the renewal route
// (client certificate required), and the enrollment route (no client
// certificate; an enrollment secret in the Authorization header).
func NewMTLSHandler(cfg MTLSConfig) (http.Handler, error) {
	switch {
	case cfg.Claims == nil:
		return nil, fmt.Errorf("%w: claim service is required", ErrInvalidMTLSConfig)
	case cfg.Registry == nil:
		return nil, fmt.Errorf("%w: registry is required", ErrInvalidMTLSConfig)
	case cfg.WorkerCA == nil:
		return nil, fmt.Errorf("%w: Worker CA certificate is required", ErrInvalidMTLSConfig)
	case cfg.MaxBodyBytes < 0:
		return nil, fmt.Errorf("%w: negative body limit", ErrInvalidMTLSConfig)
	}
	if err := workerregistry.ValidateTrustDomain(cfg.TrustDomain); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMTLSConfig, err)
	}
	h := &mtlsHandler{
		claimEndpoint: claimEndpoint{claims: cfg.Claims, maxBody: cfg.MaxBodyBytes, log: cfg.Logger},
		bodies:        cfg.BodyReceiver,
		assignments:   cfg.Assignments,
		heartbeats:    cfg.Heartbeats,
		registry:      cfg.Registry,
		roots:         x509.NewCertPool(),
		caPEM:         string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cfg.WorkerCA.Raw})),
		trustDomain:   cfg.TrustDomain,
		now:           cfg.Clock,
	}
	h.roots.AddCert(cfg.WorkerCA)
	if h.maxBody == 0 {
		h.maxBody = DefaultMaxBodyBytes
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	if h.now == nil {
		h.now = time.Now
	}
	return h, nil
}

func (h *mtlsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case testprofile.Path, enrollwire.RenewPath, enrollwire.EnrollPath:
	case testprofile.AssignmentsPath:
		if h.assignments == nil {
			reply(w, http.StatusNotFound, "not_found")
			return
		}
	case testprofile.BodyPath:
		if h.bodies == nil {
			reply(w, http.StatusNotFound, "not_found")
			return
		}
	default:
		reply(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		reply(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if r.URL.Path == enrollwire.EnrollPath {
		h.enroll(w, r)
		return
	}
	// Authenticate before reading the body, as the static-table handler does.
	principal, leaf, status := h.authenticate(r)
	if status != 0 {
		h.refuse(w, status)
		return
	}
	if r.URL.Path == enrollwire.RenewPath {
		h.renew(w, r, principal, leaf)
		return
	}
	caller := workerplane.Principal{WorkerIdentity: string(principal.Identity), TrustDomain: principal.TrustDomain}
	if r.URL.Path == testprofile.AssignmentsPath {
		h.serveAssignments(w, r, caller)
		return
	}
	if r.URL.Path == testprofile.BodyPath {
		h.serveBody(w, r, caller)
		return
	}
	h.serveClaim(w, r, caller)
}

// serveBody hands an authenticated claim-body request to the receiver. The
// body is one envelope line, a newline, then the raw bytes; the receiver
// measures them against the claim it already recorded (ADR-0034 §2.2). The
// receiver's refusals are coarse; the reason stays in its log.
func (h *mtlsHandler) serveBody(w http.ResponseWriter, r *http.Request, caller workerplane.Principal) {
	if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != testprofile.ContentType {
		reply(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	if r.ContentLength > claimbody.HandlerMaxBodyBytes {
		reply(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, claimbody.HandlerMaxBodyBytes+1))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			reply(w, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		reply(w, http.StatusBadRequest, "bad_request")
		return
	}
	line, rest, ok := strings.Cut(string(raw), "\n")
	if !ok {
		reply(w, http.StatusBadRequest, "invalid_envelope")
		return
	}
	var envelope struct {
		AttemptID ids.AttemptID `json:"attempt_id"`
		MediaType string        `json:"media_type,omitempty"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		reply(w, http.StatusBadRequest, "invalid_envelope")
		return
	}
	mediaType := envelope.MediaType
	if mediaType == "" {
		mediaType = testprofile.MediaTypeOctetStream
	}
	if err := h.bodies.ReceiveBody(r.Context(), caller.WorkerIdentity, envelope.AttemptID, []byte(rest), mediaType); err != nil {
		status, code := classifyBodyError(err)
		if status == http.StatusInternalServerError {
			h.log.Error("worker claim body failed", "worker", caller.WorkerIdentity, "error", err.Error())
		}
		reply(w, status, code)
		return
	}
	reply(w, http.StatusOK, "stored")
}

func classifyBodyError(err error) (int, string) {
	switch {
	case errors.Is(err, claimbody.ErrWorkerMismatch):
		return http.StatusForbidden, "worker_mismatch"
	case errors.Is(err, claimbody.ErrNoCompletedClaim):
		return http.StatusConflict, "no_completed_claim"
	case errors.Is(err, claimbody.ErrDigestMismatch):
		return http.StatusUnprocessableEntity, "digest_mismatch"
	case errors.Is(err, claimbody.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, "body_too_large"
	case errors.Is(err, claimbody.ErrUnknownAttempt):
		return http.StatusNotFound, "attempt_unknown"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

// authenticate verifies the client certificate of the request and asks the
// registry whether the Worker may act now. The identity comes from the
// certificate's name only; the registry is consulted on every request. The
// returned status is 0 on success. Every refusal is one line in the log with
// a reason that names the failed check and no secret (#300): under load the
// 401s were indistinguishable, and the reason is the difference between a
// test-flake report and a product bug.
func (h *mtlsHandler) authenticate(r *http.Request) (workerenrollment.WorkerPrincipal, *x509.Certificate, int) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return h.refuseAuth(r, "no_client_certificate", http.StatusUnauthorized)
	}
	leaf := r.TLS.PeerCertificates[0]
	// The leaf must be signed directly by the Worker CA. Certificates the peer
	// sends along are not used as intermediates.
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:       h.roots,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		CurrentTime: h.now(),
	}); err != nil {
		return h.refuseAuth(r, "certificate_not_verified", http.StatusUnauthorized, "verify_error", err.Error())
	}
	if leaf.IsCA || len(leaf.URIs) != 1 || len(leaf.DNSNames)+len(leaf.IPAddresses)+len(leaf.EmailAddresses) != 0 {
		return h.refuseAuth(r, "certificate_shape", http.StatusUnauthorized, "is_ca", leaf.IsCA, "uris", len(leaf.URIs))
	}
	trustDomain, identity, err := workercert.ParseURI(leaf.URIs[0])
	// The CA's name constraint also admits sub-domains of the trust domain, so
	// the domain is compared exactly.
	if err != nil || trustDomain != h.trustDomain {
		return h.refuseAuth(r, "identity_uri", http.StatusUnauthorized, "parse_error", err != nil, "trust_domain_match", err == nil && trustDomain == h.trustDomain)
	}
	principal := workerenrollment.WorkerPrincipal{Identity: identity, TrustDomain: trustDomain}
	worker, err := h.registry.Authorize(r.Context(), principal)
	if err != nil {
		if errors.Is(err, workerenrollment.ErrWorkerNotUsable) {
			return h.refuseAuth(r, "worker_not_usable", http.StatusUnauthorized, "worker", identity)
		}
		// The registry could not be read. Fail closed.
		h.log.Error("worker registry lookup failed", "error", err.Error())
		return workerenrollment.WorkerPrincipal{}, nil, http.StatusServiceUnavailable
	}
	// Only the certificate the registry currently records for this Worker is
	// accepted (ADR-0027 section 2.4 item 15, #284). Otherwise a stolen key
	// keeps working, and keeps renewing itself, after the Worker has renewed
	// to a new key. A Worker without a recorded serial cannot authenticate:
	// there is no current certificate to compare against.
	if worker.Certificate.Serial == "" || worker.Certificate.Serial != leaf.SerialNumber.Text(16) {
		return h.refuseAuth(r, "stale_certificate", http.StatusUnauthorized, "worker", identity)
	}
	return principal, leaf, 0
}

// refuseAuth logs one refused authentication with the reason that names the
// failed check, then returns the caller's refusal values. The reason kinds
// are a closed set (no request bytes, no certificate fields beyond counts and
// flags, no secrets); a log line must never become the way a certificate is
// quoted back to its sender.
func (h *mtlsHandler) refuseAuth(r *http.Request, reason string, status int, logKeysAndValues ...any) (workerenrollment.WorkerPrincipal, *x509.Certificate, int) {
	values := append([]any{"reason", reason, "remote", r.RemoteAddr}, logKeysAndValues...)
	h.log.Warn("worker authentication refused", values...)
	return workerenrollment.WorkerPrincipal{}, nil, status
}

// refuse answers a request that failed authentication and closes the
// connection after the answer, so a refused Worker does not keep a session.
//
// The close is asked for with "Connection: close" and left to net/http, which
// first discards the unread request body. Closing the socket from the handler
// would reset the connection while the client is still sending, and the
// client could lose the 401 it should see.
func (h *mtlsHandler) refuse(w http.ResponseWriter, status int) {
	code := "unauthorized"
	if status == http.StatusServiceUnavailable {
		code = "unavailable"
	}
	w.Header().Set("Connection", "close")
	reply(w, status, code)
}

func (h *mtlsHandler) enroll(w http.ResponseWriter, r *http.Request) {
	// A secret never travels in the URL. Any query at all is refused, before
	// anything about the credential is looked at, and the query is not logged.
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		reply(w, http.StatusBadRequest, "query_not_allowed")
		return
	}
	scheme, presented, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		h.enrollRefused(w, r, workerregistry.ReasonMalformed)
		return
	}
	if _, err := workerregistry.HashPresentedSecret(presented); err != nil {
		reason, _ := workerregistry.RefusalReason(err)
		h.enrollRefused(w, r, reason)
		return
	}
	pub, ok := h.readCSR(w, r)
	if !ok {
		return
	}
	enrolled, err := h.registry.Enroll(r.Context(), presented, h.trustDomain, pub)
	if err != nil {
		if errors.Is(err, workerregistry.ErrCredentialRefused) {
			reason, _ := workerregistry.RefusalReason(err)
			h.enrollRefused(w, r, reason)
			return
		}
		h.log.Error("worker enrollment failed", "error", err.Error())
		reply(w, http.StatusInternalServerError, "internal_error")
		return
	}
	h.log.Info("worker enrolled", "worker", enrolled.Worker.Identity, "trust_domain", enrolled.Worker.TrustDomain,
		"credential", enrolled.Worker.CredentialID, "serial", enrolled.Certificate.Serial)
	if enrolled.Certificate.Truncated {
		h.log.Warn("worker certificate validity was shortened by the CA expiry",
			"worker", enrolled.Worker.Identity, "serial", enrolled.Certificate.Serial,
			"not_after", enrolled.Certificate.NotAfter.UTC().Format(time.RFC3339))
	}
	h.writeCertificate(w, enrolled.Worker.Identity, enrolled.Worker.TrustDomain, enrolled.Certificate)
}

// enrollRefused is the one answer to every unusable secret: no status, body or
// header depends on the reason, so the answer is no oracle for whether a
// guessed secret exists, was spent, expired, or belongs to another domain. The
// reason goes to the audit log; the secret does not.
func (h *mtlsHandler) enrollRefused(w http.ResponseWriter, r *http.Request, reason workerregistry.Reason) {
	h.log.Warn("worker enrollment refused", "reason", string(reason), "remote", r.RemoteAddr)
	reply(w, http.StatusUnauthorized, enrollwire.RefusedCode)
}

func (h *mtlsHandler) renew(w http.ResponseWriter, r *http.Request, principal workerenrollment.WorkerPrincipal, leaf *x509.Certificate) {
	pub, ok := h.readCSR(w, r)
	if !ok {
		return
	}
	// Each renewal uses a new key (ADR-0027 section 2.4 item 15).
	if current, ok := leaf.PublicKey.(*ecdsa.PublicKey); ok && current.Equal(pub) {
		reply(w, http.StatusBadRequest, "key_reuse")
		return
	}
	cert, err := h.registry.Renew(r.Context(), principal, pub)
	if err != nil {
		if errors.Is(err, workerenrollment.ErrWorkerNotUsable) {
			h.refuse(w, http.StatusUnauthorized)
			return
		}
		h.log.Error("worker certificate renewal failed", "worker", principal.Identity, "error", err.Error())
		reply(w, http.StatusInternalServerError, "internal_error")
		return
	}
	h.log.Info("worker certificate renewed", "worker", principal.Identity, "serial", cert.Serial)
	if cert.Truncated {
		h.log.Warn("worker certificate validity was shortened by the CA expiry",
			"worker", principal.Identity, "serial", cert.Serial,
			"not_after", cert.NotAfter.UTC().Format(time.RFC3339))
	}
	h.writeCertificate(w, principal.Identity, principal.TrustDomain, cert)
}

// readCSR reads and validates the request body. It reports the failure to the
// caller and returns false.
func (h *mtlsHandler) readCSR(w http.ResponseWriter, r *http.Request) (*ecdsa.PublicKey, bool) {
	if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != enrollwire.CSRContentType {
		reply(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxEnrollBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			reply(w, http.StatusRequestEntityTooLarge, "request_too_large")
			return nil, false
		}
		reply(w, http.StatusBadRequest, "bad_request")
		return nil, false
	}
	pub, err := workerca.ParseCSR(body)
	if err != nil {
		reply(w, http.StatusBadRequest, "invalid_csr")
		return nil, false
	}
	return pub, true
}

func (h *mtlsHandler) writeCertificate(w http.ResponseWriter, identity ids.WorkerIdentity, trustDomain string, cert workerenrollment.IssuedCertificate) {
	out, err := json.Marshal(enrollwire.Response{
		WorkerIdentity: string(identity),
		TrustDomain:    trustDomain,
		CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.DER})),
		CAChainPEM:     h.caPEM,
		NotBefore:      cert.NotBefore,
		NotAfter:       cert.NotAfter,
	})
	if err != nil {
		reply(w, http.StatusInternalServerError, "internal_error")
		return
	}
	w.Header().Set("Content-Type", enrollwire.JSONContentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}
