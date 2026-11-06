package workerplane_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	server "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	spi "github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type fixedArtifacts struct {
	body []byte
	err  error
}

func (f fixedArtifacts) OpenArtifact(context.Context, contract.Digest) (io.ReadCloser, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return io.NopCloser(strings.NewReader(string(f.body))), int64(len(f.body)), nil
}

func withArtifacts(fixed fixedArtifacts) func(*server.MTLSConfig) {
	return func(cfg *server.MTLSConfig) { cfg.Artifacts = fixed }
}

// A Worker that holds the Attempt gets the bytes of its declared input.
func TestADeclaredInputIsServedToTheWorker(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	f := newMTLSFixture(t, withArtifacts(fixedArtifacts{body: []byte("input")}))
	w := f.enroll(t, nil)
	f.assign.byWorker[string(w.id)] = []spi.Assignment{{
		AttemptID: "atm_1", LeaseID: "lse_1", LeaseState: spi.LeaseStateCurrent,
		InputDigests: []contract.Digest{digest},
	}}

	resp, body := getURL(t, f, f.client(&w.cert), "/test-profile/v1/artifacts/"+digest.Hex())
	if resp.StatusCode != http.StatusOK || body != "input" {
		t.Fatalf("artifact = %d %q, want 200 with the body", resp.StatusCode, body)
	}
}

// A digest the Attempt does not declare is refused, even for an authenticated
// Worker (ADR-0029 §5.10).
func TestAnUndeclaredInputIsRefused(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	f := newMTLSFixture(t, withArtifacts(fixedArtifacts{body: []byte("input")}))
	w := f.enroll(t, nil)

	resp, _ := getURL(t, f, f.client(&w.cert), "/test-profile/v1/artifacts/"+digest.Hex())
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an input no Attempt declares", resp.StatusCode)
	}
}

// A deployment that serves no bodies says so, rather than pretending the artifact
// is missing.
func TestADeploymentWithoutBodiesAnswersNotImplemented(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	f := newMTLSFixture(t)
	w := f.enroll(t, nil)
	f.assign.byWorker[string(w.id)] = []spi.Assignment{{
		AttemptID: "atm_1", LeaseID: "lse_1", LeaseState: spi.LeaseStateCurrent,
		InputDigests: []contract.Digest{digest},
	}}

	resp, _ := getURL(t, f, f.client(&w.cert), "/test-profile/v1/artifacts/"+digest.Hex())
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 when no bodies are served", resp.StatusCode)
	}
}

// A missing body is 404, not 403: the Worker is allowed to ask, the deployment
// cannot produce the bytes.
func TestAMissingBodyIsNotFound(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	f := newMTLSFixture(t, withArtifacts(fixedArtifacts{err: server.ErrArtifactNotFound}))
	w := f.enroll(t, nil)
	f.assign.byWorker[string(w.id)] = []spi.Assignment{{
		AttemptID: "atm_1", LeaseID: "lse_1", LeaseState: spi.LeaseStateCurrent,
		InputDigests: []contract.Digest{digest},
	}}

	resp, _ := getURL(t, f, f.client(&w.cert), "/test-profile/v1/artifacts/"+digest.Hex())
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a digest with no body", resp.StatusCode)
	}
}

// getURL performs a GET against the fixture's server, so the artifact route is
// tested the way a Worker calls it.
func getURL(t *testing.T, f *mtlsFixture, c *http.Client, path string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(f.srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(body)
}
