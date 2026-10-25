package claimclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

const httpToken = "claimclient-http-token-0123456789"

func httpTestClaim(t *testing.T) resultclaim.Claim {
	t.Helper()
	c, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: "tsk_1", TaskIRDigest: contract.SHA256([]byte("ir")), AttemptID: "atm_1", WorkerIdentity: "worker-a",
		LeaseID: "les_1", FenceEpoch: 1, WorkOrderDecision: "wo", ExecutorDigest: contract.SHA256([]byte("ex")),
		Nonce: []byte("0123456789abcdef"), Status: resultclaim.StatusReclaimed,
		StartedAt: time.Unix(100, 0), EndedAt: time.Unix(101, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewHTTPRequiresTLSExceptOnLoopback(t *testing.T) {
	for url, ok := range map[string]bool{
		"https://example.org:9443":  true,
		"https://127.0.0.1:9443":    true,
		"http://127.0.0.1:9443":     true,
		"http://localhost:9443":     true,
		"http://example.org:9443":   false,
		"http://10.0.0.5:9443":      false,
		"https://example.org/path":  false,
		"https://user@example.org":  false,
		"ftp://127.0.0.1":           false,
		"":                          false,
		"https://example.org?x=y":   false,
		"https://example.org/#frag": false,
	} {
		_, err := NewHTTP(HTTPConfig{ControlURL: url, Token: httpToken})
		if (err == nil) != ok {
			t.Errorf("%q: err = %v, want ok=%v", url, err, ok)
		}
	}
	if _, err := NewHTTP(HTTPConfig{ControlURL: "https://example.org", Token: "short"}); err == nil {
		t.Error("short token accepted")
	}
}

func TestHTTPClientSendsTheBearerTokenAndDecodesTheAck(t *testing.T) {
	var gotAuth, gotType string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		if r.URL.Path != testprofile.Path || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		out, _ := testprofile.EncodeAck(workerplane.ClaimAck{Decision: "new", AttemptPhase: "closed"})
		_, _ = w.Write(out)
	}))
	defer srv.Close()
	c, err := NewHTTP(HTTPConfig{ControlURL: srv.URL, Token: httpToken})
	if err != nil {
		t.Fatal(err)
	}
	// The test server's certificate is not in the system pool: the client must
	// refuse it rather than skip verification.
	if _, err := c.SubmitClaim(context.Background(), workerplane.Principal{}, httpTestClaim(t)); err == nil {
		t.Fatal("an unverified server certificate was accepted")
	}
	pool := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	c, err = NewHTTP(HTTPConfig{ControlURL: srv.URL, Token: httpToken, RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.SubmitClaim(context.Background(), workerplane.Principal{}, httpTestClaim(t))
	if err != nil || ack.Decision != "new" || ack.AttemptPhase != "closed" {
		t.Fatalf("ack = %+v, %v", ack, err)
	}
	if gotAuth != "Bearer "+httpToken || gotType != testprofile.ContentType {
		t.Fatalf("headers = %q, %q", gotAuth, gotType)
	}
}

func TestHTTPClientTreatsEveryNon200AsUndelivered(t *testing.T) {
	for _, status := range []int{301, 401, 403, 409, 500, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status == 301 {
				w.Header().Set("Location", "http://127.0.0.1:1/elsewhere")
			}
			w.WriteHeader(status)
		}))
		c, err := NewHTTP(HTTPConfig{ControlURL: srv.URL, Token: httpToken})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.SubmitClaim(context.Background(), workerplane.Principal{}, httpTestClaim(t))
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "HTTP") {
			t.Fatalf("status %d: err = %v", status, err)
		}
	}
}

func TestHTTPClientRejectsAMalformedAcknowledgement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) }))
	defer srv.Close()
	c, err := NewHTTP(HTTPConfig{ControlURL: srv.URL, Token: httpToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SubmitClaim(context.Background(), workerplane.Principal{}, httpTestClaim(t)); err == nil {
		t.Fatal("garbage acknowledgement accepted")
	}
}
