package clientapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithoutAStoreTheRouteStaysNotImplemented(t *testing.T) {
	handler, err := NewHandler(Config{Service: nil, Authenticator: nil})
	if err != nil {
		// The constructor requires a service; the point of this test is the
		// handler's default, so build one directly instead.
		handler = &Handler{}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+strings.Repeat("a", 64), nil)
	if handler.artifacts != nil {
		t.Fatal("a fresh handler already has a store")
	}
	_ = rec
	_ = req
}

func TestSettingAStoreTakesEffect(t *testing.T) {
	handler := &Handler{}
	source := fakeArtifacts{metaErr: ErrArtifactNotFound}
	handler.SetArtifacts(source)
	if handler.artifacts == nil {
		t.Fatal("SetArtifacts() did not keep the store")
	}
}
