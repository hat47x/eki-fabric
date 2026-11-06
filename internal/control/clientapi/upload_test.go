package clientapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// memSink keeps bodies and records in memory, so the upload route can be tested
// without a directory.
type memSink struct {
	bodies   map[string][]byte
	records  map[string]UploadRecord
	mismatch bool
	failMeta bool
}

func newMemSink() *memSink {
	return &memSink{bodies: map[string][]byte{}, records: map[string]UploadRecord{}}
}

func (m *memSink) Put(r io.Reader, claimedHex string, maxBytes int64) (string, int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	if m.mismatch {
		return "", 0, ErrDigestMismatch
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return "", 0, errors.New("too large")
	}
	if got := contract.FromSHA256(sha256.Sum256(data)); got.Hex() != claimedHex {
		return "", 0, ErrDigestMismatch
	}
	m.bodies[claimedHex] = data
	return claimedHex, int64(len(data)), nil
}

func (m *memSink) PutMeta(digestHex string, record UploadRecord) error {
	if m.failMeta {
		return errors.New("no record")
	}
	m.records[digestHex] = record
	return nil
}

func (m *memSink) Remove(digestHex string) error {
	delete(m.bodies, digestHex)
	return nil
}

// newEnvWithSink is a deployment that both serves and keeps bodies.
func newEnvWithSink(t *testing.T) (*env, *memSink) {
	t.Helper()
	sink := newMemSink()
	e := newEnv(t, func(c *Config, _ *admission.Policy) {
		c.Artifacts = fakeArtifacts{}
		c.Sink = sink
	})
	return e, sink
}

func putUpload(e *env, token, uploadID, body string) response {
	e.t.Helper()
	return e.do(http.MethodPut, "/v1/artifact-uploads/"+uploadID, token,
		map[string]string{"Content-Type": "application/octet-stream"}, body)
}

// The bytes a Client uploads are kept under their own digest and described with
// the session's owner, media type and class (#245).
func TestAnUploadIsKeptUnderItsDigest(t *testing.T) {
	e, sink := newEnvWithSink(t)
	answer := decode(t, postIngest(e, "key-upload-001", validIntent))
	uploadID := answer["upload"].(map[string]any)["upload_id"].(string)
	body := "hello ingest"
	res := putUpload(e, aliceToken, uploadID, body)
	if res.status != http.StatusAccepted {
		t.Fatalf("status = %d %s, want 202", res.status, res.body)
	}
	got := decode(t, res)
	descriptor := got["artifact"].(map[string]any)
	digest := contract.FromSHA256(sha256.Sum256([]byte(body))).Hex()
	if descriptor["digest"] != digest {
		t.Fatalf("digest = %v, want the body's own digest %s", descriptor["digest"], digest)
	}
	if sink.bodies[digest] == nil {
		t.Fatal("the body was not kept")
	}
	record := sink.records[digest]
	if record.Owner != "alice" {
		t.Fatalf("owner = %s", record.Owner)
	}
	if record.MediaType != "text/plain" || record.DataClass != "internal" {
		t.Fatalf("record = %+v, want the session's declarations", record)
	}
	if record.Size != int64(len(body)) {
		t.Fatalf("size = %d, want %d", record.Size, len(body))
	}
	facets := descriptor["facets"].(map[string]any)
	if facets["content_identity"] != "verified" {
		t.Fatalf("content_identity = %v, want verified", facets["content_identity"])
	}
	if facets["safe_processing"] != "not-required" {
		t.Fatalf("safe_processing = %v: this deployment does not interpret bodies", facets["safe_processing"])
	}
}

// A session is one upload: a second PUT finds nothing.
func TestASessionIsConsumedByOneUpload(t *testing.T) {
	e, _ := newEnvWithSink(t)
	answer := decode(t, postIngest(e, "key-upload-002", validIntent))
	uploadID := answer["upload"].(map[string]any)["upload_id"].(string)
	if res := putUpload(e, aliceToken, uploadID, "first"); res.status != http.StatusAccepted {
		t.Fatalf("first status = %d", res.status)
	}
	if res := putUpload(e, aliceToken, uploadID, "second"); res.status != http.StatusNotFound {
		t.Fatalf("second status = %d, want 404", res.status)
	}
}

// Another principal's session is not disclosed: unknown and not-yours are the
// same answer.
func TestAnotherPrincipalsSessionIsNotFound(t *testing.T) {
	e, _ := newEnvWithSink(t)
	answer := decode(t, postIngest(e, "key-upload-003", validIntent))
	uploadID := answer["upload"].(map[string]any)["upload_id"].(string)
	if res := putUpload(e, bobToken, uploadID, "hello"); res.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.status)
	}
}

// A body past the declared size is refused and nothing is kept.
func TestAnUploadPastTheDeclaredSizeIsRefused(t *testing.T) {
	e, sink := newEnvWithSink(t)
	answer := decode(t, postIngest(e, "key-upload-004",
		`{"schema":"eki.artifact-ingest-intent-request/v1","declared_size_bytes":4,"media_type":"text/plain","requested_data_class":"internal"}`))
	uploadID := answer["upload"].(map[string]any)["upload_id"].(string)
	res := putUpload(e, aliceToken, uploadID, "far too many bytes")
	if res.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", res.status)
	}
	if len(sink.bodies) != 0 {
		t.Fatalf("bodies = %d, want none kept", len(sink.bodies))
	}
}

// A body whose digest does not match is refused; the store's own re-measurement
// is what decides.
func TestAFailedIdentityIsAnUnprocessableBody(t *testing.T) {
	e, sink := newEnvWithSink(t)
	sink.mismatch = true
	answer := decode(t, postIngest(e, "key-upload-005", validIntent))
	uploadID := answer["upload"].(map[string]any)["upload_id"].(string)
	res := putUpload(e, aliceToken, uploadID, "hello")
	if res.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", res.status)
	}
}

// Without a sink the route answers 501, like every other unwired operation.
func TestUploadIsNotOfferedWithoutASink(t *testing.T) {
	e := newEnvWithBodyStore(t)
	e.handler.sink = nil
	res := putUpload(e, aliceToken, "ing_whatever", "hello")
	if res.status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", res.status)
	}
}

// The 202 the upload route answers is declared under the same operation.
func TestTheUploadAnswerIsDeclaredInTheSpec(t *testing.T) {
	operation := specOperation(t, http.MethodPut, "/v1/artifact-uploads/{upload_id}")
	responses := operation["responses"].(map[string]any)
	created, ok := responses["202"].(map[string]any)
	if !ok {
		t.Fatal("the upload operation declares no 202")
	}
	content := created["content"].(map[string]any)["application/json"].(map[string]any)
	if got := content["schema"].(map[string]any)["$ref"]; got != "#/components/schemas/ArtifactUploadAccepted" {
		t.Fatalf("202 schema = %v", got)
	}
	e, _ := newEnvWithSink(t)
	answer := decode(t, postIngest(e, "key-upload-006", validIntent))
	uploadID := answer["upload"].(map[string]any)["upload_id"].(string)
	if res := putUpload(e, aliceToken, uploadID, "hello"); res.status != http.StatusAccepted {
		t.Fatalf("status = %d, want the declared 202", res.status)
	}
}

var _ = json.Marshal
var _ = bytes.NewReader
var _ = time.Now
