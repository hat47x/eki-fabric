package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hat47x/eki-fabric/internal/control/clientapi"
)

func TestTheSinkAcceptsA200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sink := newAuditSink(server.URL)
	if err := sink.Send(context.Background(), clientapi.ArtifactAccess{Principal: "alice"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestTheSinkReportsANonTwoHundredAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	sink := newAuditSink(server.URL)
	if err := sink.Send(context.Background(), clientapi.ArtifactAccess{Principal: "alice"}); err == nil {
		t.Fatal("Send() hid a 500: a record nobody wrote must not look written")
	}
}

func TestTheSinkReportsARedirectAsAFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer server.Close()
	sink := newAuditSink(server.URL)
	if err := sink.Send(context.Background(), clientapi.ArtifactAccess{Principal: "alice"}); err == nil {
		t.Fatal("Send() followed a redirect: the record went somewhere the operator did not name")
	}
}
