package clientapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/admission"
)

type fakeArtifacts struct {
	meta    ArtifactMeta
	metaErr error
	body    string
}

func (f fakeArtifacts) LoadMeta(string) (ArtifactMeta, error) { return f.meta, f.metaErr }

func (f fakeArtifacts) Open(string) (io.ReadCloser, int64, error) {
	return io.NopCloser(strings.NewReader(f.body)), int64(len(f.body)), nil
}

func artifactRequest(t *testing.T, source ArtifactSource, digest string) *httptest.ResponseRecorder {
	t.Helper()
	handler := &Handler{artifacts: source, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+digest, nil)
	handler.artifact(rec, req, Identity{Principal: admission.Principal{ID: "alice"}, Role: RoleClient}, digest)
	return rec
}

func TestTheOwnerReceivesTheBodyAndItsHeaders(t *testing.T) {
	source := fakeArtifacts{meta: ArtifactMeta{Owner: "alice", MediaType: "text/plain", Size: 5, ExpiresAt: time.Now().Add(time.Hour)}, body: "hello"}
	rec := artifactRequest(t, source, strings.Repeat("a", 64))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Length") != "5" || rec.Header().Get("X-EKI-Artifact-Size") != "5" {
		t.Fatalf("headers = %v", rec.Header())
	}
	if rec.Header().Get("Digest") == "" {
		t.Fatal("the Digest header is missing")
	}
}

func TestAnotherPrincipalGetsTheSameAnswerAsAMissingArtifact(t *testing.T) {
	source := fakeArtifacts{meta: ArtifactMeta{Owner: "bob", Size: 5, ExpiresAt: time.Now().Add(time.Hour)}, body: "hello"}
	rec := artifactRequest(t, source, strings.Repeat("a", 64))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAnExpiredArtifactIsRefusedWith410ForItsOwner(t *testing.T) {
	source := fakeArtifacts{meta: ArtifactMeta{Owner: "alice", Size: 5, ExpiresAt: time.Now().Add(-time.Hour)}, body: "hello"}
	rec := artifactRequest(t, source, strings.Repeat("a", 64))
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "artifact_expired") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestAMissingMetadataRecordIsA404(t *testing.T) {
	source := fakeArtifacts{metaErr: ErrArtifactNotFound}
	rec := artifactRequest(t, source, strings.Repeat("a", 64))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "artifact_not_found") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestAnUnreadableStoreIsAnsweredWith500AndAuditedAsUnreadable(t *testing.T) {
	var entries []ArtifactAccess
	source := fakeArtifacts{metaErr: errors.New("disk on fire")}
	handler := &Handler{artifacts: source, log: slog.New(slog.NewTextHandler(io.Discard, nil)), artifactAudit: func(a ArtifactAccess) { entries = append(entries, a) }}
	digest := strings.Repeat("a", 64)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+digest, nil)
	handler.artifact(rec, req, Identity{Principal: admission.Principal{ID: "alice"}, Role: RoleClient}, digest)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "artifact_unreadable") {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if len(entries) != 1 || entries[0].Outcome != ArtifactUnreadable {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestTheAuditRecordsServedAndRefused(t *testing.T) {
	var entries []ArtifactAccess
	source := fakeArtifacts{meta: ArtifactMeta{Owner: "alice", Size: 5, ExpiresAt: time.Now().Add(time.Hour)}, body: "hello"}
	handler := &Handler{artifacts: source, log: slog.New(slog.NewTextHandler(io.Discard, nil)), artifactAudit: func(a ArtifactAccess) { entries = append(entries, a) }}
	digest := strings.Repeat("a", 64)
	rec := httptest.NewRecorder()
	handler.artifact(rec, httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+digest, nil), Identity{Principal: admission.Principal{ID: "alice"}, Role: RoleClient}, digest)
	if len(entries) != 1 || entries[0].Outcome != ArtifactServed || entries[0].Principal != "alice" || entries[0].Artifact != digest {
		t.Fatalf("entries = %+v", entries)
	}
	entries = nil
	foreign := fakeArtifacts{meta: ArtifactMeta{Owner: "bob", Size: 5, ExpiresAt: time.Now().Add(time.Hour)}, body: "hello"}
	handler = &Handler{artifacts: foreign, log: slog.New(slog.NewTextHandler(io.Discard, nil)), artifactAudit: func(a ArtifactAccess) { entries = append(entries, a) }}
	handler.artifact(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+digest, nil), Identity{Principal: admission.Principal{ID: "alice"}, Role: RoleClient}, digest)
	if len(entries) != 1 || entries[0].Outcome != ArtifactNotFound {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestAnExpiredRetrievalIsAuditedAsExpired(t *testing.T) {
	var entries []ArtifactAccess
	source := fakeArtifacts{meta: ArtifactMeta{Owner: "alice", Size: 5, ExpiresAt: time.Now().Add(-time.Hour)}, body: "hello"}
	handler := &Handler{artifacts: source, log: slog.New(slog.NewTextHandler(io.Discard, nil)), artifactAudit: func(a ArtifactAccess) { entries = append(entries, a) }}
	digest := strings.Repeat("b", 64)
	handler.artifact(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+digest, nil), Identity{Principal: admission.Principal{ID: "alice"}, Role: RoleClient}, digest)
	if len(entries) != 1 || entries[0].Outcome != ArtifactExpired {
		t.Fatalf("entries = %+v", entries)
	}
}
