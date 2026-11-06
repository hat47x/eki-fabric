package assignment

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// The fetcher verifies what it received: the route names content by digest, so a
// body that does not hash to the name is a failure, not a Task's input
// (ADR-0029 §5.10).
func TestTheFetcherVerifiesTheDigest(t *testing.T) {
	body := []byte("input-bytes")
	digest := contract.SHA256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-profile/v1/artifacts/"+digest.Hex() {
			t.Errorf("path = %s, want the digest route", r.URL.Path)
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	var saved []byte
	fetcher := HTTPFetcher{BaseURL: server.URL, Save: func(_ contract.Digest, got []byte) error {
		saved = got
		return nil
	}}
	if err := fetcher.Fetch(context.Background(), digest); err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(body) {
		t.Fatalf("saved = %q, want the served bytes", saved)
	}
}

// Bytes that do not match the name are refused, and nothing is saved.
func TestAMismatchedBodyIsRefused(t *testing.T) {
	digest := contract.SHA256([]byte("expected"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("something else"))
	}))
	defer server.Close()

	saved := false
	fetcher := HTTPFetcher{BaseURL: server.URL, Save: func(contract.Digest, []byte) error {
		saved = true
		return nil
	}}
	err := fetcher.Fetch(context.Background(), digest)
	if !errors.Is(err, ErrInputDigestMismatch) {
		t.Fatalf("Fetch() error = %v, want %v", err, ErrInputDigestMismatch)
	}
	if saved {
		t.Fatal("a mismatched body was saved")
	}
}

// A refusal from the route is a failed fetch, not an empty input.
func TestARefusedFetchFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	err := HTTPFetcher{BaseURL: server.URL}.Fetch(context.Background(), contract.SHA256([]byte("a")))
	if err == nil {
		t.Fatal("Fetch() accepted a 403")
	}
	if strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("error = %v, want a transport-level failure", err)
	}
}

// An oversized body is refused before it is read into memory.
func TestAnOversizedBodyIsRefused(t *testing.T) {
	body := []byte(strings.Repeat("x", 64))
	digest := contract.SHA256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	err := HTTPFetcher{BaseURL: server.URL, MaxBytes: 8}.Fetch(context.Background(), digest)
	if err == nil {
		t.Fatal("Fetch() accepted a body over the limit")
	}
}

// A fetcher without a base URL fails closed instead of calling some default host.
func TestAFetcherWithoutABaseURLFails(t *testing.T) {
	if err := (HTTPFetcher{}).Fetch(context.Background(), contract.SHA256([]byte("a"))); err == nil {
		t.Fatal("Fetch() ran without a base URL")
	}
}
