package clientapi

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/admission"
)

// The line that describes a request has to name the request the caller does, or an
// error report cannot be matched to the line that explains it (#288).
func TestTheRequestLogCarriesTheClientRequestID(t *testing.T) {
	var logged bytes.Buffer
	e := newEnv(t, func(cfg *Config, _ *admission.Policy) {
		cfg.Logger = slog.New(slog.NewTextHandler(&logged, nil))
	})

	body := strings.TrimSuffix(strings.TrimSpace(validRequest), "}") + `, "client_request_id": "order-4711"}`
	if resp := e.submit(aliceToken, "key-log-001", body); resp.status != 202 {
		t.Fatalf("submit = %d %s", resp.status, resp.body)
	}
	if !strings.Contains(logged.String(), "client_request_id=order-4711") {
		t.Fatalf("the request log does not name the caller's request id:\n%s", logged.String())
	}
}

// A request without one still logs an empty value rather than a made-up name.
func TestTheRequestLogWithoutAClientRequestID(t *testing.T) {
	var logged bytes.Buffer
	e := newEnv(t, func(cfg *Config, _ *admission.Policy) {
		cfg.Logger = slog.New(slog.NewTextHandler(&logged, nil))
	})
	if resp := e.submit(aliceToken, "key-log-002", validRequest); resp.status != 202 {
		t.Fatalf("submit = %d %s", resp.status, resp.body)
	}
	if !strings.Contains(logged.String(), "client_request_id=") {
		t.Fatalf("the request log has no client_request_id field:\n%s", logged.String())
	}
}
