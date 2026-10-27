package claimwire

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

const experimentalContentType = "application/x-eki-claim-experiment"

// These are test-only transport fixtures, not Connect, production credentials,
// or an adopted Worker protocol. The session table is supplied by the test.
type experimentalHTTPClient struct {
	codec   *codec
	http    *http.Client
	url     string
	session string
}

func (c *experimentalHTTPClient) SubmitClaim(ctx context.Context, p workerplane.Principal, claim resultclaim.Claim) (workerplane.ClaimAck, error) {
	if !strings.HasPrefix(c.url, "https://") {
		return workerplane.ClaimAck{}, errInvalid
	}
	wire, err := c.codec.encodeClaim(p, claim)
	if err != nil {
		return workerplane.ClaimAck{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/claim", bytes.NewReader(wire))
	if err != nil {
		return workerplane.ClaimAck{}, err
	}
	req.Header.Set("Content-Type", experimentalContentType)
	req.Header.Set("X-Experiment-Session", c.session)
	response, err := c.http.Do(req)
	if err != nil {
		return workerplane.ClaimAck{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.TLS == nil || response.Header.Get("Content-Type") != experimentalContentType {
		return workerplane.ClaimAck{}, fmt.Errorf("experimental HTTP response: status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMessageBytes+1))
	if err != nil {
		return workerplane.ClaimAck{}, err
	}
	return c.codec.decodeReceipt(body, claim)
}

type experimentalHTTPServer struct {
	codec     *codec
	api       workerplane.ClaimClient
	sessions  map[string]workerplane.Principal
	fault     string
	calls     atomic.Int32
	newClaims atomic.Int32
	decision  atomic.Value
	cancelled chan struct{}
}

func (s *experimentalHTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil {
		http.Error(w, "TLS required", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/health" && r.Method == http.MethodGet {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path != "/claim" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Content-Type") != experimentalContentType {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	p, found := s.sessions[r.Header.Get("X-Experiment-Session")]
	if !found {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMessageBytes))
	if err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	claim, err := s.codec.decodeClaim(p, body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if s.fault == "http-503" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if s.fault == "timeout" {
		<-r.Context().Done()
		close(s.cancelled)
		return
	}
	s.calls.Add(1)
	ack, err := s.api.SubmitClaim(r.Context(), p, claim)
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if ack.Decision == "new" {
		s.newClaims.Add(1)
	}
	s.decision.Store(ack.Decision)
	if s.fault == "drop-response" {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	if s.fault == "wrong-receipt" {
		// Change only the request binding; keep a structurally valid receipt.
		cfg := copyClaimConfig(claim)
		cfg.OutputBytes++
		claim, err = resultclaim.NewClaim(cfg)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	wire, err := s.codec.encodeReceipt(ack, claim)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if s.fault == "malformed-receipt" {
		wire = []byte{0xff}
	}
	w.Header().Set("Content-Type", experimentalContentType)
	_, _ = w.Write(wire)
}

func startTLSFixture(t *testing.T, c *codec, api workerplane.ClaimClient, fault string) (*httptest.Server, *experimentalHTTPServer) {
	t.Helper()
	handler := &experimentalHTTPServer{
		codec: c, api: api, fault: fault, cancelled: make(chan struct{}),
		sessions: map[string]workerplane.Principal{
			"fixture-a": {WorkerIdentity: "worker-a", TrustDomain: "example.org"},
			"fixture-b": {WorkerIdentity: "worker-b", TrustDomain: "example.org"},
		},
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS() // HTTP/1.1; the lost-response test deliberately hijacks it.
	t.Cleanup(srv.Close)
	return srv, handler
}

func tlsClient(c *codec, srv *httptest.Server, session string) *experimentalHTTPClient {
	client := *srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &experimentalHTTPClient{codec: c, http: &client, url: srv.URL, session: session}
}

func copyClaimConfig(c resultclaim.Claim) resultclaim.ClaimConfig {
	return resultclaim.ClaimConfig{
		TaskID: c.TaskID(), TaskIRDigest: c.TaskIRDigest(), AttemptID: c.AttemptID(), WorkerIdentity: c.WorkerIdentity(),
		LeaseID: c.LeaseID(), FenceEpoch: c.FenceEpoch(), WorkOrderDecision: c.WorkOrderDecision(), ExecutorDigest: c.ExecutorDigest(),
		Nonce: c.Nonce(), Status: c.Status(), ExitCode: c.ExitCode(), OutputBytes: c.OutputBytes(), OutputDigest: c.OutputDigest(),
		StartedAt: c.StartedAt(), EndedAt: c.EndedAt(),
	}
}
