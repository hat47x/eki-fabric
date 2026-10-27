package workerplane_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	server "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

const (
	tokenA = "http-test-token-worker-a-0123456789"
	tokenB = "http-test-token-worker-b-0123456789"
)

type fakeClaims struct {
	calls     int
	principal workerplane.Principal
	err       error
}

func (f *fakeClaims) SubmitClaim(_ context.Context, p workerplane.Principal, _ resultclaim.Claim) (workerplane.ClaimAck, error) {
	f.calls++
	f.principal = p
	if f.err != nil {
		return workerplane.ClaimAck{}, f.err
	}
	return workerplane.ClaimAck{Decision: "new", AttemptPhase: "verifying", VerificationRequired: true}, nil
}

func httpClaimBody(t *testing.T, worker string) []byte {
	t.Helper()
	c, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: "tsk_1", TaskIRDigest: contract.SHA256([]byte("ir")), AttemptID: "atm_1", WorkerIdentity: worker,
		LeaseID: "les_1", FenceEpoch: 1, WorkOrderDecision: "wo", ExecutorDigest: contract.SHA256([]byte("ex")),
		Nonce: []byte("0123456789abcdef"), Status: resultclaim.StatusCompleted, OutputDigest: contract.SHA256([]byte("o")),
		StartedAt: time.Unix(100, 0), EndedAt: time.Unix(101, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := testprofile.EncodeClaim(c)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newHandler(t *testing.T, api workerplane.ClaimClient, max int64) http.Handler {
	t.Helper()
	h, err := server.NewHTTPHandler(server.HTTPConfig{
		Claims: api, MaxBodyBytes: max, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sessions: []server.SessionEntry{
			{WorkerIdentity: "worker-a", TrustDomain: "example.org", SessionTokenSHA256: server.HashSessionToken(tokenA)},
			{WorkerIdentity: "worker-b", SessionTokenSHA256: server.HashSessionToken(tokenB)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, auth, contentType string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, testprofile.Path, bytes.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPHandlerTakesThePrincipalFromTheTable(t *testing.T) {
	api := &fakeClaims{}
	h := newHandler(t, api, 0)
	rec := do(h, http.MethodPost, "Bearer "+tokenA, testprofile.ContentType, httpClaimBody(t, "worker-a"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if api.principal != (workerplane.Principal{WorkerIdentity: "worker-a", TrustDomain: "example.org"}) {
		t.Fatalf("principal = %+v", api.principal)
	}
	ack, err := testprofile.DecodeAck(rec.Body.Bytes())
	if err != nil || ack.Decision != "new" || !ack.VerificationRequired {
		t.Fatalf("ack = %+v, %v", ack, err)
	}
}

func TestHTTPHandlerRejectsBeforeTouchingTheClaimService(t *testing.T) {
	good := httpClaimBody(t, "worker-a")
	cases := []struct {
		name   string
		method string
		auth   string
		ctype  string
		body   []byte
		want   int
	}{
		{"no credential", http.MethodPost, "", testprofile.ContentType, good, 401},
		{"unknown token", http.MethodPost, "Bearer unknown-token-0123456789abcdef", testprofile.ContentType, good, 401},
		{"short token", http.MethodPost, "Bearer short", testprofile.ContentType, good, 401},
		{"basic scheme", http.MethodPost, "Basic " + tokenA, testprofile.ContentType, good, 401},
		{"wrong method", http.MethodGet, "Bearer " + tokenA, "", nil, 405},
		{"wrong content type", http.MethodPost, "Bearer " + tokenA, "text/plain", good, 415},
		{"malformed body", http.MethodPost, "Bearer " + tokenA, testprofile.ContentType, []byte("{"), 400},
		{"oversize body", http.MethodPost, "Bearer " + tokenA, testprofile.ContentType, bytes.Repeat([]byte(" "), 2048), 413},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeClaims{}
			rec := do(newHandler(t, api, 1024), tc.method, tc.auth, tc.ctype, tc.body)
			if rec.Code != tc.want || api.calls != 0 {
				t.Fatalf("status = %d (want %d), service calls = %d", rec.Code, tc.want, api.calls)
			}
		})
	}
}

func TestHTTPHandlerErrorsNeverLeakInternals(t *testing.T) {
	secret := "SECRET-attempt-atm_1-lease-les_1 /var/db/state.sqlite"
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not owner", errors.Join(claimplane.ErrNotClaimOwner, errors.New(secret)), 403},
		{"other failure", errors.New(secret), 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeClaims{err: tc.err}
			rec := do(newHandler(t, api, 0), http.MethodPost, "Bearer "+tokenB, testprofile.ContentType, httpClaimBody(t, "worker-a"))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if strings.Contains(rec.Body.String(), "SECRET") || strings.Contains(rec.Body.String(), "atm_1") {
				t.Fatalf("response leaks internals: %s", rec.Body)
			}
			if api.principal.WorkerIdentity != "worker-b" {
				t.Fatalf("principal came from %q, want the table entry worker-b", api.principal.WorkerIdentity)
			}
		})
	}
}

func TestNewHTTPHandlerRejectsBadTables(t *testing.T) {
	api := &fakeClaims{}
	hash := server.HashSessionToken(tokenA)
	for name, sessions := range map[string][]server.SessionEntry{
		"empty":         nil,
		"no identity":   {{SessionTokenSHA256: hash}},
		"bad hash":      {{WorkerIdentity: "w", SessionTokenSHA256: "abc"}},
		"duplicate":     {{WorkerIdentity: "w", SessionTokenSHA256: hash}, {WorkerIdentity: "x", SessionTokenSHA256: hash}},
		"upper-hex bad": {{WorkerIdentity: "w", SessionTokenSHA256: strings.Repeat("g", 64)}},
	} {
		if _, err := server.NewHTTPHandler(server.HTTPConfig{Claims: api, Sessions: sessions}); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if _, err := server.NewHTTPHandler(server.HTTPConfig{Sessions: []server.SessionEntry{{WorkerIdentity: "w", SessionTokenSHA256: hash}}}); err == nil {
		t.Fatal("missing claim service accepted")
	}
}
