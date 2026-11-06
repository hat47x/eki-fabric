package workerplane_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	server "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	spi "github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// fakeHeldClaims records the claims the endpoint passes on.
type fakeHeldClaims struct {
	mu    sync.Mutex
	held  map[string]workerregistry.HeldArtifacts
	fail  error
	calls int
}

func (f *fakeHeldClaims) SetHeldArtifacts(_ context.Context, identity ids.WorkerIdentity, held workerregistry.HeldArtifacts, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail != nil {
		return f.fail
	}
	f.held[string(identity)] = held
	return nil
}

func withClaims(f *fakeHeldClaims) func(*server.MTLSConfig) {
	return func(cfg *server.MTLSConfig) { cfg.HeldArtifacts = f }
}

// The claim is recorded for the Worker named by the certificate, and the body
// cannot name another one (#717, ADR-0029 §5.1).
func TestHeldArtifactsAreRecordedForTheCertificateWorker(t *testing.T) {
	claims := &fakeHeldClaims{held: map[string]workerregistry.HeldArtifacts{}}
	f := newMTLSFixture(t, withClaims(claims))
	w := f.enroll(t, nil)

	digest := contract.SHA256([]byte("artifact-a"))
	body, _ := json.Marshal(map[string]any{"digests": []string{digest.Hex()}})
	resp, text := f.post(t, f.client(&w.cert), spi.HeldArtifactsPath, "application/json", "", body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("held-artifacts = %d %s", resp.StatusCode, text)
	}
	if !claims.held[string(w.id)].Holds(digest) {
		t.Fatalf("recorded = %v, want the digest for the certificate's Worker", claims.held)
	}
}

// A claim that is not a list of digests is refused before anything is stored.
func TestABrokenHeldArtifactsClaimIsRefused(t *testing.T) {
	claims := &fakeHeldClaims{held: map[string]workerregistry.HeldArtifacts{}}
	f := newMTLSFixture(t, withClaims(claims))
	w := f.enroll(t, nil)

	for _, body := range []string{`{"digests":["not-hex"]}`, `{"digests":["aa"]}`, `{"digests":[],"extra":1}`, `{}`} {
		resp, _ := f.post(t, f.client(&w.cert), spi.HeldArtifactsPath, "application/json", "", []byte(body))
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("body %s: status = %d, want 400 or 204", body, resp.StatusCode)
		}
	}
	if got := claims.calls; got > 1 {
		t.Fatalf("the endpoint stored %d claims from malformed bodies", got)
	}
}

// Without a store the route stays closed, the way the other optional routes do.
func TestTheHeldArtifactsRouteIsClosedWithoutAStore(t *testing.T) {
	f := newMTLSFixture(t)
	w := f.enroll(t, nil)
	resp, _ := f.post(t, f.client(&w.cert), spi.HeldArtifactsPath, "application/json", "", []byte(`{"digests":[]}`))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no store is configured", resp.StatusCode)
	}
}
