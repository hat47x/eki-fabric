package clientapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/admission"
)

const validIntent = `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1024,"media_type":"text/plain","requested_data_class":"internal"}`

const validIntentWithExpectedDigest = `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1024,"media_type":"text/plain","requested_data_class":"internal","expected_digest":{"algorithm":"sha256","value":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}`

func postIngest(e *env, key, body string) response {
	e.t.Helper()
	headers := map[string]string{"Content-Type": jsonContentType}
	if key != "" {
		headers["Idempotency-Key"] = key
	}
	return e.do(http.MethodPost, "/v1/artifacts/ingest-intents", aliceToken, headers, body)
}

// A declaration this deployment can honour answers with a named session and the
// path the body goes to (spec/external/openapi.json).
func TestAnIngestDeclarationNamesAnUploadSession(t *testing.T) {
	e := newEnvWithBodyStore(t)
	res := postIngest(e, "key-ingest-001", validIntent)
	if res.status != http.StatusCreated {
		t.Fatalf("status = %d %s, want 201", res.status, res.body)
	}
	answer := decode(t, res)
	if answer["schema"] != "eki.artifact-ingest-intent-response/v1" {
		t.Fatalf("schema = %v", answer["schema"])
	}
	if answer["ingest_id"] == "" || answer["ingest_id"] == nil {
		t.Fatal("the answer names no ingest")
	}
	upload, ok := answer["upload"].(map[string]any)
	if !ok {
		t.Fatalf("upload = %v", answer["upload"])
	}
	if upload["method"] != http.MethodPut {
		t.Fatalf("method = %v, want PUT", upload["method"])
	}
	if upload["href"] != "/v1/artifact-uploads/"+upload["upload_id"].(string) {
		t.Fatalf("href = %v, is not the session's own path", upload["href"])
	}
	if upload["max_bytes"].(json.Number).String() != "1024" {
		t.Fatalf("max_bytes = %v, want the declared size", upload["max_bytes"])
	}
}

// The same Idempotency-Key from the same principal converges on the same session:
// a retry does not open a second place to write.
func TestTheSameKeyReturnsTheSameSession(t *testing.T) {
	e := newEnvWithBodyStore(t)
	first := decode(t, postIngest(e, "key-ingest-002", validIntent))
	second := decode(t, postIngest(e, "key-ingest-002", validIntent))
	a := first["upload"].(map[string]any)["upload_id"]
	b := second["upload"].(map[string]any)["upload_id"]
	if a != b {
		t.Fatalf("upload ids differ: %v / %v", a, b)
	}
}

// A declaration this deployment cannot honour is refused before any session exists.
func TestAnUnusableDeclarationIsRefused(t *testing.T) {
	e := newEnvWithBodyStore(t)
	for _, c := range []struct {
		name   string
		body   string
		status int
	}{
		{"wrong schema", `{"schema":"other","declared_size_bytes":1,"media_type":"text/plain","requested_data_class":"internal"}`, http.StatusBadRequest},
		{"negative size", `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":-1,"media_type":"text/plain","requested_data_class":"internal"}`, http.StatusBadRequest},
		{"over the cap", `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":100000000,"media_type":"text/plain","requested_data_class":"internal"}`, http.StatusRequestEntityTooLarge},
		{"unknown class", `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1,"media_type":"text/plain","requested_data_class":"secret"}`, http.StatusBadRequest},
		{"unknown field", `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1,"media_type":"text/plain","requested_data_class":"internal","extra":1}`, http.StatusBadRequest},
		{"empty media_type", `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1,"media_type":"","requested_data_class":"internal"}`, http.StatusBadRequest},
		{"missing media_type slash", `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1,"media_type":"textplain","requested_data_class":"internal"}`, http.StatusBadRequest},
		{"empty media_type type", `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1,"media_type":"/plain","requested_data_class":"internal"}`, http.StatusBadRequest},
		{"empty media_type subtype", `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1,"media_type":"text/","requested_data_class":"internal"}`, http.StatusBadRequest},
		{"not json", `hello`, http.StatusBadRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := postIngest(e, "key-ingest-003", c.body)
			if res.status != c.status {
				t.Fatalf("status = %d %s, want %d", res.status, res.body, c.status)
			}
		})
	}
	if len(e.handler.ingests.byID) != 0 {
		t.Fatalf("sessions = %d, want none created for refused declarations", len(e.handler.ingests.byID))
	}
}

// Without a body store the route answers 501: naming a session that could never
// receive bytes would be a promise this deployment cannot keep.
func TestIngestIsNotOfferedWithoutABodyStore(t *testing.T) {
	e := newEnv(t)
	res := postIngest(e, "key-ingest-004", validIntent)
	if res.status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", res.status)
	}
}

// The declaration needs an Idempotency-Key, like every other write.
func TestIngestNeedsAnIdempotencyKey(t *testing.T) {
	e := newEnvWithBodyStore(t)
	res := postIngest(e, "", validIntent)
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.status)
	}
}

// The spec example with expected_digest is accepted (#803).
func TestTheSpecExampleWithExpectedDigestIsAccepted(t *testing.T) {
	e := newEnvWithBodyStore(t)
	res := postIngest(e, "key-ingest-expected-001", validIntentWithExpectedDigest)
	if res.status != http.StatusCreated {
		t.Fatalf("status = %d %s, want 201", res.status, res.body)
	}
}

// An expected_digest with the wrong algorithm is refused.
func TestAnExpectedDigestWithWrongAlgorithmIsRefused(t *testing.T) {
	e := newEnvWithBodyStore(t)
	body := `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1024,"media_type":"text/plain","requested_data_class":"internal","expected_digest":{"algorithm":"sha1","value":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}`
	res := postIngest(e, "key-ingest-expected-002", body)
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d %s, want 400", res.status, res.body)
	}
}

// An expected_digest with a non-hex value is refused.
func TestAnExpectedDigestWithNonHexValueIsRefused(t *testing.T) {
	e := newEnvWithBodyStore(t)
	body := `{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":1024,"media_type":"text/plain","requested_data_class":"internal","expected_digest":{"algorithm":"sha256","value":"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"}}`
	res := postIngest(e, "key-ingest-expected-003", body)
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d %s, want 400", res.status, res.body)
	}
}

// newEnvWithBodyStore is a deployment that keeps bodies. The ingest route is
// offered only where there is somewhere for the bytes to go (#245).
func newEnvWithBodyStore(t *testing.T) *env {
	t.Helper()
	return newEnv(t, func(c *Config, _ *admission.Policy) {
		c.Artifacts = fakeArtifacts{}
	})
}
