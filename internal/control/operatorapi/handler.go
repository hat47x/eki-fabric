// Package operatorapi is the Operator entry point (ADR-0038).
//
// It exists as its own package because its audience is different from both the
// Client API and the Worker listener: an Operator approves who the Control Plane
// trusts, and the authentication that proves an Operator (a certificate from the
// Operator CA) must not be reachable through the other two.
package operatorapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

// Path is where an Operator approves a Trust Bundle version.
const Path = "/v1/operator/trust-bundles"

// MaxBodyBytes bounds a request body. The body is a small JSON document.
const MaxBodyBytes = 8 * 1024

// Approver records an approval. The implementation turns it into the state the
// Control Plane admits versions from (ADR-0036 §3).
type Approver interface {
	Approve(approval trustbundle.Approval) error
}

// Authenticator turns a request into the Operator identity the certificate names.
// Nil means this process has no Operator CA configured, and then the route does
// not exist at all: there is no "unauthenticated because unconfigured" path.
type Authenticator func(*http.Request) (operatorcert.Identity, error)

// Access is one line of the Operator audit (ADR-0038 §2). It is recorded for
// refusals as well: the refusals are what an investigation needs.
type Access struct {
	At        time.Time `json:"at"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Operation string    `json:"operation"`
	Target    string    `json:"target"`
	Outcome   string    `json:"outcome"`
}

// The outcomes an audit line can carry.
const (
	OutcomeApproved = "approved"
	OutcomeRefused  = "refused"
)

// Config is what a Handler needs.
type Config struct {
	// Authenticate is required for the route to exist.
	Authenticate Authenticator
	// Approver records the approval. Required with Authenticate.
	Approver Approver
	// Book is the ledger the approvals land in. Nil hides the read path.
	Book *trustbundle.ApprovalBook
	// Admitter admits a signed version on behalf of an Administrator.
	Admitter Admitter
	// Audit receives one entry per request. Nil logs instead.
	Audit func(Access)
	// AuditErr is a sink that can refuse (ADR-0040 §5). When set, an approval or
	// an admission is refused if the line cannot be written.
	AuditErr func(Access) error
	// Logger receives one line per request. Nil discards.
	Logger *slog.Logger
	// Now is the clock. Nil means the system clock.
	Now func() time.Time
}

// Handler serves the Operator routes.
type Handler struct {
	authenticate Authenticator
	approver     Approver
	// book is where the approvals are collected; nil hides the read path.
	book *trustbundle.ApprovalBook
	// admitter records a signed version; nil hides the Administrator route.
	admitter Admitter
	audit    func(Access)
	// auditErr is a sink that can refuse (ADR-0040 §5). It takes precedence over
	// audit: a sink that cannot fail cannot tell the handler to stop.
	auditErr func(Access) error
	log      *slog.Logger
	now      func() time.Time
}

// New returns a Handler. Without both an authenticator and an approver it returns
// a handler that answers 404 to everything, which is the same answer a process
// that never had an Operator route would give.
func New(cfg Config) *Handler {
	h := &Handler{authenticate: cfg.Authenticate, approver: cfg.Approver, book: cfg.Book, admitter: cfg.Admitter, audit: cfg.Audit, auditErr: cfg.AuditErr, log: cfg.Logger, now: cfg.Now}
	if h.log == nil {
		h.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.authenticate == nil || h.approver == nil {
		h.authenticate = nil
		h.approver = nil
	}
	return h
}

// Enabled reports whether the Operator route exists in this process.
func (h *Handler) Enabled() bool { return h != nil && h.authenticate != nil && h.approver != nil }

// ServeHTTP answers the Operator routes.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.Enabled() {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "This operation is not available in this deployment")
		return
	}
	cleaned := path.Clean(r.URL.Path)
	if cleaned == PendingPath && r.Method == http.MethodGet {
		identity, err := h.authenticate(r)
		if err != nil {
			_ = h.record(identity, "", "", OutcomeRefused)
			writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required")
			return
		}
		if identity.Role != operatorcert.RoleOperator {
			_ = h.record(identity, "", "", OutcomeRefused)
			writeError(w, http.StatusForbidden, "OPERATOR_ROLE_REQUIRED", "This credential cannot read trust bundle approvals")
			return
		}
		h.pending(w, r, identity)
		return
	}
	if cleaned == AdmitPath && r.Method == http.MethodPost {
		identity, err := h.authenticate(r)
		if err != nil {
			_ = h.record(identity, "", "", OutcomeRefused)
			writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required")
			return
		}
		if identity.Role != operatorcert.RoleAdministrator {
			_ = h.record(identity, "", "", OutcomeRefused)
			writeError(w, http.StatusForbidden, "ADMINISTRATOR_ROLE_REQUIRED", "This credential cannot admit trust bundles")
			return
		}
		h.admit(w, r, identity)
		return
	}
	if cleaned != Path || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "This operation is not available in this deployment")
		return
	}
	identity, err := h.authenticate(r)
	if err != nil {
		_ = h.record(identity, "", "", OutcomeRefused)
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required")
		return
	}
	if identity.Role != operatorcert.RoleOperator {
		_ = h.record(identity, "", "", OutcomeRefused)
		writeError(w, http.StatusForbidden, "OPERATOR_ROLE_REQUIRED", "This credential cannot approve trust bundles")
		return
	}
	approval, err := decodeApproval(r, identity)
	if err != nil {
		_ = h.record(identity, "", "", OutcomeRefused)
		writeError(w, http.StatusBadRequest, "INVALID_APPROVAL", err.Error())
		return
	}
	if h.auditErr != nil {
		if err := h.record(identity, string(approval.Operation), approval.Issuer.String(), OutcomeApproved); err != nil {
			writeError(w, http.StatusServiceUnavailable, "AUDIT_UNAVAILABLE", "The audit record could not be written, so the approval was not taken")
			return
		}
	}
	if err := h.approver.Approve(approval); err != nil {
		h.record(identity, string(approval.Operation), approval.Issuer.String(), OutcomeRefused)
		writeError(w, http.StatusConflict, "APPROVAL_REFUSED", err.Error())
		return
	}
	if h.auditErr == nil {
		_ = h.record(identity, string(approval.Operation), approval.Issuer.String(), OutcomeApproved)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"schema":   "eki.trust-bundle-approval/v1",
		"version":  approval.Version,
		"operator": identity.Name,
	})
}

// decodeApproval reads the body and checks it against the certificate. The body
// must name the same Operator the certificate does: the record is what an
// investigation reads later, so a body that disagrees would put a name in the
// log that nobody proved.
func decodeApproval(r *http.Request, identity operatorcert.Identity) (trustbundle.Approval, error) {
	var body struct {
		Version       uint64 `json:"version"`
		Operation     string `json:"operation"`
		Issuer        string `json:"issuer"`
		ApprovedBy    string `json:"approved_by"`
		Justification string `json:"justification"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return trustbundle.Approval{}, fmt.Errorf("the body is not an approval: %v", err)
	}
	if body.ApprovedBy != identity.Name {
		return trustbundle.Approval{}, fmt.Errorf("approved_by must name the operator the certificate names")
	}
	issuer, err := contract.ParseSHA256(body.Issuer)
	if err != nil {
		return trustbundle.Approval{}, fmt.Errorf("issuer must be the SHA-256 fingerprint of the authority")
	}
	approval := trustbundle.Approval{
		Version:       body.Version,
		Operation:     trustbundle.Operation(body.Operation),
		Issuer:        issuer,
		ApprovedBy:    identity.Name,
		ApprovedAt:    time.Now().UTC(),
		Justification: body.Justification,
	}
	if approval.Version == 0 || approval.Justification == "" {
		return trustbundle.Approval{}, fmt.Errorf("version and justification are required")
	}
	if approval.Operation != trustbundle.OperationAdd && approval.Operation != trustbundle.OperationRemove {
		return trustbundle.Approval{}, fmt.Errorf("operation must be %q or %q", trustbundle.OperationAdd, trustbundle.OperationRemove)
	}
	return approval, nil
}

// record writes one audit line. When the sink can refuse, the refusal is returned:
// an Operation that changes who the platform trusts must not happen unless it was
// written down (ADR-0040 §5). Retrieval keeps going on a sink failure because the
// owner-only rule does not depend on the record; an authority change is different.
func (h *Handler) record(identity operatorcert.Identity, operation, target, outcome string) error {
	entry := Access{At: h.now().UTC(), Name: identity.Name, Role: string(identity.Role), Operation: operation, Target: target, Outcome: outcome}
	if h.auditErr != nil {
		if err := h.auditErr(entry); err != nil {
			h.log.Error("operator access was not recorded", "name", entry.Name, "operation", entry.Operation, "error", err.Error())
			return err
		}
		return nil
	}
	if h.audit != nil {
		h.audit(entry)
		return nil
	}
	h.log.Info("operator access", "name", entry.Name, "role", entry.Role, "operation", entry.Operation, "target", entry.Target, "outcome", entry.Outcome)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, title string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "title": title}})
}
