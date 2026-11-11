package teideleg

// A fake EKI Client API for the Spike's Mock tests. It implements the answers
// the external contract declares and nothing else: no Lease, no Attempt
// identity, no Worker. The real EKI experiments are recorded separately in
// RESULTS_2026-11-11.md; nothing here is evidence about the real deployment.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeUpload struct {
	expectedDigest string
	maxBytes       int64
}

type fakeTask struct {
	id           string
	idempotency  string
	requestRaw   []byte
	status       string
	terminalCode string
	attemptCount int
	result       *TaskResult
	resultErr    *Problem
	cancelled    bool
	submittedAt  time.Time
}

type fakeEKI struct {
	t        *testing.T
	mu       sync.Mutex
	approved map[string]bool
	bodies   map[string][]byte
	meta     map[string]ArtifactDescriptor
	uploads  map[string]fakeUpload
	tasks    map[string]*fakeTask
	byKey    map[string]string
	nextID   int
	clock    func() time.Time

	// autoResult makes the fake act like a Task that finished on its own the
	// first time its status is read. Tests use it to script a lifecycle
	// without sleeping.
	autoStatus   string
	autoAttempts int
	autoResult   *TaskResult
	autoProblem  *Problem
	autoDone     map[string]bool
}

// putArtifact stores a body and returns the digest EKI would name it by.
func (f *fakeEKI) putArtifact(body []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	f.bodies[digest] = body
	return digest
}

// autoComplete scripts the fake to finish the Task on its first status read.
func (f *fakeEKI) autoComplete(attempts int, result *TaskResult, problem *Problem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoStatus = "completed"
	f.autoAttempts = attempts
	f.autoResult = result
	f.autoProblem = problem
}

func (f *fakeEKI) autoHold(status string, attempts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoStatus = status
	f.autoAttempts = attempts
}

func newFakeEKI(t *testing.T) *fakeEKI {
	t.Helper()
	return &fakeEKI{
		t:        t,
		approved: map[string]bool{"tei.validate": true},
		bodies:   map[string][]byte{},
		meta:     map[string]ArtifactDescriptor{},
		uploads:  map[string]fakeUpload{},
		tasks:    map[string]*fakeTask{},
		byKey:    map[string]string{},
		clock:    func() time.Time { return time.Now().UTC() },
	}
}

func (f *fakeEKI) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(f.serve))
}

func (f *fakeEKI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/artifacts/ingest-intents":
		f.ingest(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/artifact-uploads/"):
		f.upload(w, r, strings.TrimPrefix(r.URL.Path, "/v1/artifact-uploads/"))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/artifacts/"):
		f.artifact(w, r, strings.TrimPrefix(r.URL.Path, "/v1/artifacts/"))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/tasks":
		f.submit(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
		rest := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
		if strings.HasSuffix(rest, "/result") {
			f.result(w, strings.TrimSuffix(rest, "/result"))
			return
		}
		f.status(w, rest)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
		f.cancel(w, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/tasks/"), "/cancel"))
	default:
		writeProblem(w, http.StatusNotFound, "NOT_FOUND", "no such route")
	}
}

func (f *fakeEKI) ingest(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Idempotency-Key") == "" {
		writeProblem(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required")
		return
	}
	var req IngestIntentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if req.Schema != "eki.artifact-ingest-intent-request/v1" {
		writeProblem(w, http.StatusBadRequest, "INVALID_REQUEST", "unexpected schema")
		return
	}
	expected := ""
	if req.ExpectedDigest != nil {
		expected = req.ExpectedDigest.Value
	}
	f.nextID++
	uploadID := "up-" + strings.Repeat("0", 1) + itoa(f.nextID)
	f.uploads[uploadID] = fakeUpload{expectedDigest: expected, maxBytes: req.DeclaredSizeBytes}
	writeJSON(w, http.StatusCreated, IngestIntentResponse{
		Schema:   "eki.artifact-ingest-intent-response/v1",
		IngestID: "ing-" + itoa(f.nextID),
		Upload: UploadSession{
			UploadID:  uploadID,
			Method:    http.MethodPut,
			Href:      "/v1/artifact-uploads/" + uploadID,
			ExpiresAt: f.clock().Add(15 * time.Minute).Format(time.RFC3339),
			MaxBytes:  req.DeclaredSizeBytes,
		},
	})
}

func (f *fakeEKI) upload(w http.ResponseWriter, r *http.Request, uploadID string) {
	session, ok := f.uploads[uploadID]
	if !ok {
		writeProblem(w, http.StatusNotFound, "UPLOAD_NOT_FOUND", "upload session not found")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if int64(len(raw)) > session.maxBytes && session.maxBytes > 0 {
		delete(f.uploads, uploadID)
		writeProblem(w, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE", "body exceeds the reserved maximum")
		return
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if session.expectedDigest != "" && session.expectedDigest != digest {
		delete(f.uploads, uploadID)
		writeProblem(w, http.StatusUnprocessableEntity, "DIGEST_MISMATCH", "body digest does not match expected_digest")
		return
	}
	delete(f.uploads, uploadID)
	f.bodies[digest] = raw
	f.meta[digest] = ArtifactDescriptor{
		Schema:     "eki.artifact-descriptor/v1",
		ArtifactID: digest,
		Digest:     &Digest{Algorithm: "sha256", Value: digest},
		SizeBytes:  int64(len(raw)),
		MediaType:  "application/gzip",
		DataClass:  "internal",
		Facets: ArtifactFacetState{
			ContentIdentity: "verified", SafeProcessing: "not-required",
			Provenance: "recorded", Availability: "available",
		},
	}
	writeJSON(w, http.StatusAccepted, UploadAccepted{
		Schema:   "eki.artifact-upload-accepted/v1",
		IngestID: "ing-1",
		Artifact: f.meta[digest],
	})
}

func (f *fakeEKI) artifact(w http.ResponseWriter, r *http.Request, id string) {
	body, ok := f.bodies[id]
	if !ok {
		writeProblem(w, http.StatusNotFound, "ARTIFACT_NOT_FOUND", "artifact not found")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Digest", "sha-256="+id)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (f *fakeEKI) submit(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeProblem(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if id, ok := f.byKey[key]; ok {
		task := f.tasks[id]
		if string(task.requestRaw) != string(raw) {
			writeProblem(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "the key was used for a different request")
			return
		}
		writeJSON(w, http.StatusAccepted, TaskAccepted{
			Schema: "eki.task-accepted/v1", TaskID: task.id, Status: "queued",
			RequestFingerprint: fingerprint(raw), ClientRequestID: clientIDOf(raw),
			Replayed: true, SubmittedAt: task.submittedAt.Format(time.RFC3339),
		})
		return
	}
	var req TaskRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if !f.approved[req.Executor.ID] {
		writeProblem(w, http.StatusUnprocessableEntity, "EXECUTOR_NOT_APPROVED", "executor is not approved")
		return
	}
	for _, in := range req.Inputs {
		if _, ok := f.bodies[in.ArtifactID]; !ok {
			writeProblem(w, http.StatusUnprocessableEntity, "ARTIFACT_NOT_AVAILABLE", "input artifact is not available")
			return
		}
	}
	f.nextID++
	id := "task-" + itoa(f.nextID)
	task := &fakeTask{
		id: id, idempotency: key, requestRaw: raw, status: "queued",
		submittedAt: f.clock(),
	}
	f.tasks[id] = task
	f.byKey[key] = id
	writeJSON(w, http.StatusAccepted, TaskAccepted{
		Schema: "eki.task-accepted/v1", TaskID: id, Status: "queued",
		RequestFingerprint: fingerprint(raw), ClientRequestID: clientIDOf(raw),
		Replayed: false, SubmittedAt: task.submittedAt.Format(time.RFC3339),
	})
}

func (f *fakeEKI) status(w http.ResponseWriter, taskID string) {
	task, ok := f.tasks[taskID]
	if !ok {
		writeProblem(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	if f.autoStatus != "" && !f.autoDone[taskID] {
		if f.autoDone == nil {
			f.autoDone = map[string]bool{}
		}
		f.autoDone[taskID] = true
		task.status = f.autoStatus
		if f.autoAttempts > 0 {
			task.attemptCount = f.autoAttempts
		}
		if f.autoResult != nil {
			task.result = f.autoResult
		}
		if f.autoProblem != nil {
			task.resultErr = f.autoProblem
		}
	}
	st := TaskPublicStatus{
		Schema: "eki.task-public-status/v1", TaskID: task.id, Status: task.status,
		SubmittedAt:  task.submittedAt.Format(time.RFC3339),
		UpdatedAt:    task.submittedAt.Add(time.Second).Format(time.RFC3339),
		TerminalCode: task.terminalCode,
		AttemptCount: task.attemptCount,
	}
	writeJSON(w, http.StatusOK, st)
}

func (f *fakeEKI) result(w http.ResponseWriter, taskID string) {
	task, ok := f.tasks[taskID]
	if !ok {
		writeProblem(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	if task.resultErr != nil {
		writeJSON(w, http.StatusConflict, *task.resultErr)
		return
	}
	if task.result == nil {
		writeJSON(w, http.StatusConflict, Problem{
			Type: "about:blank", Title: "The task has not produced an accepted result yet",
			Status: http.StatusConflict, Code: "RESULT_NOT_READY", Retryable: true, TaskID: task.id,
		})
		return
	}
	writeJSON(w, http.StatusOK, *task.result)
}

func (f *fakeEKI) cancel(w http.ResponseWriter, taskID string) {
	task, ok := f.tasks[taskID]
	if !ok {
		writeProblem(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	task.cancelled = true
	task.status = "cancelled"
	task.terminalCode = "CANCELLED"
	task.result = nil
	task.resultErr = &Problem{
		Type: "about:blank", Title: "The task finished without an accepted result",
		Status: http.StatusConflict, Code: "RESULT_NOT_AVAILABLE", TaskID: task.id,
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"schema": "eki.task-cancel-accepted/v1", "task_id": task.id, "status": "cancellation-requested",
	})
}

// scenario helpers: the test drives the fake the way EKI's own lifecycle would.

func (f *fakeEKI) setStatus(taskID, status string, attemptCount int, terminalCode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	task := f.tasks[taskID]
	task.status = status
	task.attemptCount = attemptCount
	task.terminalCode = terminalCode
}

func (f *fakeEKI) setResult(taskID string, result *TaskResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[taskID].result = result
}

func (f *fakeEKI) setResultProblem(taskID string, problem *Problem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[taskID].resultErr = problem
}

func fingerprint(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func clientIDOf(raw []byte) string {
	var probe struct {
		ClientRequestID string `json:"client_request_id"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.ClientRequestID
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, code, title string) {
	writeJSON(w, status, Problem{Type: "about:blank", Title: title, Status: status, Code: code})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
