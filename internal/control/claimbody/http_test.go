package claimbody

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

type recordingReceive struct {
	gotAttempt ids.AttemptID
	gotWorker  string
	gotBody    []byte
	gotMedia   string
	err        error
}

func (r *recordingReceive) receive(ctx context.Context, attempt ids.AttemptID, worker string, body []byte, media string) error {
	r.gotAttempt, r.gotWorker, r.gotBody, r.gotMedia = attempt, worker, body, media
	return r.err
}

func TestAWellFormedBodyReachesTheReceiverWithTheTransportWorker(t *testing.T) {
	got := &recordingReceive{}
	h, err := NewHTTPReceiver(got.receive)
	if err != nil {
		t.Fatal(err)
	}
	attempt, _ := ids.NewAttemptID()
	recorder := httptest.NewRecorder()
	envelope := `{"attempt_id":"` + attempt.String() + `","media_type":"text/plain"}`
	request := httptest.NewRequest("POST", testprofile.BodyPath, strings.NewReader(envelope+"\nraw bytes here"))
	request.Header.Set("Content-Type", testprofile.ContentType)
	request = request.WithContext(WithWorker(request.Context(), "worker-a"))
	h.ServeHTTP(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("status %d; want 200", recorder.Code)
	}
	if got.gotAttempt != attempt || got.gotWorker != "worker-a" {
		t.Fatalf("receiver got attempt %q worker %q", got.gotAttempt, got.gotWorker)
	}
	if string(got.gotBody) != "raw bytes here" {
		t.Fatalf("body %q was not forwarded whole", got.gotBody)
	}
	if got.gotMedia != "text/plain" {
		t.Fatalf("media type %q was not forwarded", got.gotMedia)
	}
}

func TestAWorkerIdentityComesOnlyFromTheTransport(t *testing.T) {
	got := &recordingReceive{}
	h, _ := NewHTTPReceiver(got.receive)
	attempt, _ := ids.NewAttemptID()
	// The envelope tries to name a Worker; there is no such field.
	envelope := `{"attempt_id":"` + attempt.String() + `","worker_identity":"worker-evil"}`
	request := httptest.NewRequest("POST", testprofile.BodyPath, strings.NewReader(envelope+"\nbody"))
	request.Header.Set("Content-Type", testprofile.ContentType)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status %d; want 401 without a transport identity", recorder.Code)
	}
	if got.gotWorker != "" {
		t.Fatalf("the receiver ran with a worker identity %q", got.gotWorker)
	}
}

func TestARefusedBodyIsACoarseCodeNotAReason(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"worker mismatch":    {ErrWorkerMismatch, http.StatusForbidden},
		"no completed claim": {ErrNoCompletedClaim, http.StatusConflict},
		"digest mismatch":    {ErrDigestMismatch, http.StatusUnprocessableEntity},
		"too large":          {ErrTooLarge, http.StatusRequestEntityTooLarge},
		"unknown attempt":    {ErrUnknownAttempt, http.StatusNotFound},
		"unspecified":        {errors.New("deep internal"), http.StatusInternalServerError},
	} {
		got := &recordingReceive{err: tc.err}
		h, _ := NewHTTPReceiver(got.receive)
		attempt, _ := ids.NewAttemptID()
		request := httptest.NewRequest("POST", testprofile.BodyPath, strings.NewReader(`{"attempt_id":"`+attempt.String()+`"}`+"\nbody"))
		request.Header.Set("Content-Type", testprofile.ContentType)
		request = request.WithContext(WithWorker(request.Context(), "worker-a"))
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		if recorder.Code != tc.want {
			t.Fatalf("%s: status %d; want %d", name, recorder.Code, tc.want)
		}
		if body := recorder.Body.String(); strings.Contains(body, "deep") {
			t.Fatalf("%s: the reason leaked: %q", name, body)
		}
	}
}

func TestAMissingOrMalformedEnvelopeIsNotRouted(t *testing.T) {
	got := &recordingReceive{}
	h, _ := NewHTTPReceiver(got.receive)
	for name, body := range map[string]string{
		"not json":   "not json at all\nbody",
		"no attempt": `{"media_type":"text/plain"}` + "\nbody",
		"no newline": `{"attempt_id":"atm_1"}`,
		"empty":      "",
	} {
		request := httptest.NewRequest("POST", testprofile.BodyPath, strings.NewReader(body))
		request.Header.Set("Content-Type", testprofile.ContentType)
		request = request.WithContext(WithWorker(request.Context(), "worker-a"))
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d; want 400", name, recorder.Code)
		}
	}
	if got.gotWorker != "" {
		t.Fatal("the receiver ran on a malformed envelope")
	}
}

func TestTheReceiverAnswersNothingButPOSTAndTheProfileType(t *testing.T) {
	h, _ := NewHTTPReceiver((&recordingReceive{}).receive)

	get := httptest.NewRequest("GET", testprofile.BodyPath, nil)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: status %d; want 405", recorder.Code)
	}

	post := httptest.NewRequest("POST", testprofile.BodyPath, strings.NewReader("{}\n"))
	post.Header.Set("Content-Type", "text/csv")
	recorder = httptest.NewRecorder()
	h.ServeHTTP(recorder, post)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong type: status %d; want 415", recorder.Code)
	}
}

func TestABodyWithoutANewlineIsAMalformedEnvelope(t *testing.T) {
	got := &recordingReceive{}
	h, _ := NewHTTPReceiver(got.receive)
	request := httptest.NewRequest("POST", testprofile.BodyPath, strings.NewReader(`{"attempt_id":"atm_1"}`))
	request.Header.Set("Content-Type", testprofile.ContentType)
	request = request.WithContext(WithWorker(request.Context(), "worker-a"))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d; want 400", recorder.Code)
	}
}

func TestAnEmptyBodyStillReachesTheReceiver(t *testing.T) {
	got := &recordingReceive{}
	h, _ := NewHTTPReceiver(got.receive)
	attempt, _ := ids.NewAttemptID()
	request := httptest.NewRequest("POST", testprofile.BodyPath, strings.NewReader(`{"attempt_id":"`+attempt.String()+`"}`+"\n"))
	request.Header.Set("Content-Type", testprofile.ContentType)
	request = request.WithContext(WithWorker(request.Context(), "worker-a"))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("status %d; want 200 (an empty body is a body)", recorder.Code)
	}
	if len(got.gotBody) != 0 {
		t.Fatalf("body %q was not empty", got.gotBody)
	}
}
