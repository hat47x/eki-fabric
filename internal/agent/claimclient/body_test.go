package claimclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/control/claimbody"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// A stand-in for the receiver that records what arrived.
type stubReceiver struct {
	gotWorker string
	gotBody   []byte
	gotMedia  string
	err       error
}

func (s *stubReceiver) ReceiveBody(ctx context.Context, attemptID ids.AttemptID, worker string, body []byte, media string) error {
	s.gotWorker, s.gotBody, s.gotMedia = worker, body, media
	return s.err
}

func bodyServer(t *testing.T, worker string, stub *stubReceiver) *httptest.Server {
	t.Helper()
	handler, err := claimbody.NewHTTPReceiver(stub.ReceiveBody)
	if err != nil {
		t.Fatal(err)
	}
	mount := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(claimbody.WithWorker(r.Context(), worker)))
	})
	srv := httptest.NewServer(mount)
	t.Cleanup(srv.Close)
	return srv
}

func TestTheBodyReachesTheReceiverAsEnvelopeThenBytes(t *testing.T) {
	stub := &stubReceiver{}
	srv := bodyServer(t, "worker-a", stub)
	hc := &HTTPClient{base: strings.TrimSuffix(srv.URL, "/"), http: srv.Client()}
	attempt, _ := ids.NewAttemptID()

	if err := hc.SendBody(context.Background(), attempt, []byte("the output"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if stub.gotWorker != "worker-a" {
		t.Fatalf("worker %q was not the transport's", stub.gotWorker)
	}
	if string(stub.gotBody) != "the output" {
		t.Fatalf("body %q was not forwarded whole", stub.gotBody)
	}
	if stub.gotMedia != "text/plain" {
		t.Fatalf("media type %q was not forwarded", stub.gotMedia)
	}
}

func TestTheBodyWithoutAWorkerIdentityIsRefusedBeforeTheReceiver(t *testing.T) {
	stub := &stubReceiver{}
	srv := bodyServer(t, "", stub)
	hc := &HTTPClient{base: srv.URL, http: srv.Client()}
	attempt, _ := ids.NewAttemptID()

	if err := hc.SendBody(context.Background(), attempt, []byte("out"), ""); err == nil {
		t.Fatal("the send succeeded without an identity")
	}
	if stub.gotWorker != "" || stub.gotBody != nil {
		t.Fatal("the receiver ran on an unauthenticated request")
	}
}
