package clientapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

const (
	aliceToken   = "alice-token-0123456789abcdef"
	bobToken     = "bob-token-0123456789abcdefgh"
	adminToken   = "admin-token-0123456789abcdefg"
	validRequest = `{
  "schema": "eki.task-request/v1",
  "executor": {"id": "dev.example.transform", "version_constraint": "^1"},
  "inputs": [],
  "parameters": {"mode": "normalize"},
  "resources": {"cpu_millis": 1000, "memory_bytes": 268435456, "wall_time_seconds": 300, "output_bytes_max": 16777216, "network_bytes_max": 134217728},
  "reliability": {"retryable": true, "preemptible": true, "checkpointable": false, "deterministic": true, "max_attempts": 3, "effect_mode": "none"},
  "priority": "normal"
}`
)

type env struct {
	t       *testing.T
	handler *Handler
	store   *memory.Store
}

func newEnv(t *testing.T, mutate ...func(*Config, *admission.Policy)) *env {
	t.Helper()
	return newEnvWith(t, nil, mutate...)
}

// newEnvWith wires a result reader. A nil reader leaves the result operation
// unavailable, as in a deployment that has not enabled it.
func newEnvWith(t *testing.T, results statestore.ResultAcceptanceStore, mutate ...func(*Config, *admission.Policy)) *env {
	t.Helper()
	store := memory.New()
	policy := admission.Policy{
		ApprovedExecutors: []string{"dev.example.transform"},
		PerTask: admission.ResourceCeiling{
			CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeSeconds: 3600,
			OutputBytesMax: 1 << 30, NetworkBytesMax: 1 << 30, TemporaryDiskBytes: 1 << 30,
		},
		MaxAttempts:         5,
		MaxOutstandingTasks: 3,
	}
	auth, err := NewStaticTokens([]TokenEntry{
		{Principal: "alice", Role: RoleClient, TokenSHA256: HashToken(aliceToken)},
		{Principal: "bob", Role: RoleClient, TokenSHA256: HashToken(bobToken)},
		{Principal: "root", Role: RoleAdmin, TokenSHA256: HashToken(adminToken)},
	})
	if err != nil {
		t.Fatalf("NewStaticTokens() error = %v", err)
	}
	cfg := Config{Authenticator: auth, MaxBodyBytes: 8 * 1024}
	for _, m := range mutate {
		m(&cfg, &policy)
	}
	var reader statestore.ResultAcceptanceStore
	if results != nil {
		reader = results
	}
	svc, err := admission.New(admission.Config{Store: store, Results: reader, Policy: policy})
	if err != nil {
		t.Fatalf("admission.New() error = %v", err)
	}
	cfg.Service = svc
	h, err := NewHandler(cfg)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return &env{t: t, handler: h, store: store}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (e *env) do(method, path, token string, headers map[string]string, body string) response {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return response{status: res.StatusCode, header: res.Header, body: data}
}

func (e *env) submit(token, key, body string) response {
	e.t.Helper()
	return e.do(http.MethodPost, "/v1/tasks", token, map[string]string{
		"Content-Type": "application/json", "Idempotency-Key": key,
	}, body)
}

func (e *env) get(token, id string) response {
	e.t.Helper()
	return e.do(http.MethodGet, "/v1/tasks/"+id, token, nil, "")
}

func (e *env) cancel(token, id, key string) response {
	e.t.Helper()
	headers := map[string]string{}
	if key != "" {
		headers["Idempotency-Key"] = key
	}
	return e.do(http.MethodPost, "/v1/tasks/"+id+"/cancel", token, headers, "")
}

func decode(t *testing.T, r response) map[string]any {
	t.Helper()
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(r.body))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("response is not a JSON object: %v\n%s", err, r.body)
	}
	return out
}

func wantProblem(t *testing.T, r response, status int, code string) map[string]any {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d\n%s", r.status, status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != problemContentType {
		t.Fatalf("Content-Type = %q, want %q", ct, problemContentType)
	}
	p := decode(t, r)
	if p["code"] != code {
		t.Fatalf("code = %v, want %s\n%s", p["code"], code, r.body)
	}
	checkAgainstSpec(t, "Problem", p)
	return p
}

// --- the OpenAPI document is the contract ---

var specCache map[string]any

func specSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	if specCache == nil {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "external", "openapi.json"))
		if err != nil {
			t.Fatalf("read openapi.json: %v", err)
		}
		if err := json.Unmarshal(raw, &specCache); err != nil {
			t.Fatalf("parse openapi.json: %v", err)
		}
	}
	schemas := specCache["components"].(map[string]any)["schemas"].(map[string]any)
	s, ok := schemas[name].(map[string]any)
	if !ok {
		t.Fatalf("schema %q not found in openapi.json", name)
	}
	return s
}

// checkAgainstSpec verifies a response body against the object schema in the
// P0 document: required properties present, no undeclared properties, and
// const / enum / pattern / length constraints on strings. It covers exactly
// what the P0 response schemas use; it is not a general JSON Schema validator.
func checkAgainstSpec(t *testing.T, schemaName string, body map[string]any) {
	t.Helper()
	schema := specSchema(t, schemaName)
	props := schema["properties"].(map[string]any)

	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if _, present := body[r.(string)]; !present {
				t.Errorf("%s: required property %q is missing", schemaName, r)
			}
		}
	}
	for name, value := range body {
		def, declared := props[name].(map[string]any)
		if !declared {
			if schema["additionalProperties"] == false {
				t.Errorf("%s: undeclared property %q", schemaName, name)
			}
			continue
		}
		s, isString := value.(string)
		if isString {
			if c, ok := def["const"].(string); ok && s != c {
				t.Errorf("%s.%s = %q, want const %q", schemaName, name, s, c)
			}
			if enum, ok := def["enum"].([]any); ok {
				found := false
				for _, e := range enum {
					if e == s {
						found = true
					}
				}
				if !found {
					t.Errorf("%s.%s = %q, not in enum %v", schemaName, name, s, enum)
				}
			}
			if p, ok := def["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(s) {
				t.Errorf("%s.%s = %q does not match %s", schemaName, name, s, p)
			}
			if max, ok := def["maxLength"].(float64); ok && float64(len(s)) > max {
				t.Errorf("%s.%s is longer than %v", schemaName, name, max)
			}
			if def["format"] == "date-time" {
				if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
					t.Errorf("%s.%s = %q is not a date-time: %v", schemaName, name, s, err)
				}
			}
		}
		if def["type"] == "string" && !isString {
			t.Errorf("%s.%s is not a string", schemaName, name)
		}
	}
}

// --- tests ---

func TestSubmitThenStatusFollowsTheContract(t *testing.T) {
	e := newEnv(t)

	res := e.submit(aliceToken, "key-00000001", validRequest)
	if res.status != http.StatusAccepted {
		t.Fatalf("submit status = %d\n%s", res.status, res.body)
	}
	if ct := res.header.Get("Content-Type"); ct != jsonContentType {
		t.Fatalf("Content-Type = %q", ct)
	}
	accepted := decode(t, res)
	checkAgainstSpec(t, "TaskAccepted", accepted)
	if accepted["status"] != "queued" {
		t.Fatalf("status = %v, want queued", accepted["status"])
	}
	id := accepted["task_id"].(string)

	status := e.get(aliceToken, id)
	if status.status != http.StatusOK {
		t.Fatalf("get status = %d\n%s", status.status, status.body)
	}
	body := decode(t, status)
	checkAgainstSpec(t, "TaskPublicStatus", body)
	if body["status"] != "queued" || body["task_id"] != id {
		t.Fatalf("unexpected status body: %s", status.body)
	}

	for _, secret := range []string{"worker", "lease", "attempt", "policy", "alice", "principal", "fingerprint", "digest"} {
		if strings.Contains(strings.ToLower(string(status.body)), secret) {
			t.Errorf("public status mentions %q: %s", secret, status.body)
		}
	}
}

func TestEveryResponseCarriesNoStoreAndNosniff(t *testing.T) {
	e := newEnv(t)
	for name, res := range map[string]response{
		"accepted":  e.submit(aliceToken, "key-00000001", validRequest),
		"401":       e.do(http.MethodGet, "/v1/tasks/x", "", nil, ""),
		"404":       e.do(http.MethodGet, "/nope", "", nil, ""),
		"not found": e.get(aliceToken, "tsk_00000000000000000000000000000001"),
	} {
		if res.header.Get("Cache-Control") != "no-store" || res.header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing security headers: %v", name, res.header)
		}
	}
}

func TestAuthentication(t *testing.T) {
	e := newEnv(t)

	t.Run("no credential", func(t *testing.T) {
		res := e.submit("", "key-00000001", validRequest)
		wantProblem(t, res, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
		if !strings.HasPrefix(res.header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("WWW-Authenticate = %q", res.header.Get("WWW-Authenticate"))
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		wantProblem(t, e.submit("not-a-valid-token-0123456789", "key-00000001", validRequest), http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
	})
	t.Run("too short token", func(t *testing.T) {
		wantProblem(t, e.submit("abc", "key-00000001", validRequest), http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
	})
	t.Run("token with a prefix of a valid one", func(t *testing.T) {
		wantProblem(t, e.submit(aliceToken[:len(aliceToken)-1], "key-00000001", validRequest), http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
	})
	t.Run("wrong scheme", func(t *testing.T) {
		res := e.do(http.MethodPost, "/v1/tasks", "", map[string]string{
			"Authorization": "Basic " + aliceToken, "Content-Type": "application/json", "Idempotency-Key": "key-00000001",
		}, validRequest)
		wantProblem(t, res, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
	})
	t.Run("token in the query string is ignored", func(t *testing.T) {
		res := e.do(http.MethodGet, "/v1/tasks/tsk_00000000000000000000000000000001?access_token="+aliceToken, "", nil, "")
		wantProblem(t, res, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
	})
	t.Run("admin credential is not a client", func(t *testing.T) {
		wantProblem(t, e.submit(adminToken, "key-00000001", validRequest), http.StatusForbidden, "CLIENT_ROLE_REQUIRED")
	})
	t.Run("a rejected request creates nothing", func(t *testing.T) {
		// Alice's quota is 3; if the failed attempts above had admitted work,
		// she could not submit three now.
		for i := 0; i < 3; i++ {
			if res := e.submit(aliceToken, fmt.Sprintf("key-after-%03d", i), validRequest); res.status != http.StatusAccepted {
				t.Fatalf("submit %d status = %d\n%s", i, res.status, res.body)
			}
		}
	})
}

func TestUnauthenticatedProbingLearnsNothing(t *testing.T) {
	e := newEnv(t)
	real := decode(t, e.submit(aliceToken, "key-00000001", validRequest))["task_id"].(string)

	a := e.get("", real)
	b := e.get("", "tsk_00000000000000000000000000000009")
	if a.status != b.status || !bytes.Equal(a.body, b.body) {
		t.Fatal("an unauthenticated caller can tell an existing task from an absent one")
	}
}

func TestSubmitRequiresAWellFormedRequest(t *testing.T) {
	e := newEnv(t)
	ok := map[string]string{"Content-Type": "application/json", "Idempotency-Key": "key-00000001"}

	t.Run("missing idempotency key", func(t *testing.T) {
		res := e.do(http.MethodPost, "/v1/tasks", aliceToken, map[string]string{"Content-Type": "application/json"}, validRequest)
		wantProblem(t, res, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED")
	})
	t.Run("duplicate idempotency key header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader(validRequest))
		req.Header.Set("Authorization", "Bearer "+aliceToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add("Idempotency-Key", "key-00000001")
		req.Header.Add("Idempotency-Key", "key-00000002")
		rec := httptest.NewRecorder()
		e.handler.ServeHTTP(rec, req)
		res := rec.Result()
		defer res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", res.StatusCode)
		}
	})
	t.Run("bad idempotency keys", func(t *testing.T) {
		for _, key := range []string{"short", strings.Repeat("k", 129), "has space!"} {
			res := e.do(http.MethodPost, "/v1/tasks", aliceToken, map[string]string{"Content-Type": "application/json", "Idempotency-Key": key}, validRequest)
			if res.status != http.StatusBadRequest {
				t.Errorf("key %q: status = %d, want 400", key, res.status)
			}
		}
	})
	t.Run("wrong content type", func(t *testing.T) {
		for _, ct := range []string{"", "text/plain", "application/json; charset=latin1", "application/json; boundary=x", "application/xml"} {
			headers := map[string]string{"Idempotency-Key": "key-00000001"}
			if ct != "" {
				headers["Content-Type"] = ct
			}
			res := e.do(http.MethodPost, "/v1/tasks", aliceToken, headers, validRequest)
			if res.status != http.StatusBadRequest {
				t.Errorf("content type %q: status = %d, want 400", ct, res.status)
			}
		}
	})
	t.Run("content type with utf-8 charset is accepted", func(t *testing.T) {
		res := e.do(http.MethodPost, "/v1/tasks", aliceToken, map[string]string{
			"Content-Type": "application/json; charset=UTF-8", "Idempotency-Key": "key-charset-01",
		}, validRequest)
		if res.status != http.StatusAccepted {
			t.Fatalf("status = %d\n%s", res.status, res.body)
		}
	})
	t.Run("oversized body", func(t *testing.T) {
		big := `{"schema":"eki.task-request/v1","pad":"` + strings.Repeat("x", 9*1024) + `"}`
		wantProblem(t, e.do(http.MethodPost, "/v1/tasks", aliceToken, ok, big), http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE")
	})
	t.Run("malformed and hostile json", func(t *testing.T) {
		for name, body := range map[string]string{
			"empty":         ``,
			"truncated":     `{"schema":`,
			"duplicate key": `{"schema":"eki.task-request/v1","schema":"eki.task-request/v1"}`,
			"array":         `[]`,
			"trailing":      validRequest + `{}`,
		} {
			res := e.do(http.MethodPost, "/v1/tasks", aliceToken, ok, body)
			if res.status != http.StatusBadRequest || res.header.Get("Content-Type") != problemContentType {
				t.Errorf("%s: status = %d, content type = %q\n%s", name, res.status, res.header.Get("Content-Type"), res.body)
				continue
			}
			p := decode(t, res)
			if code := p["code"]; code != "MALFORMED_JSON" && code != "INVALID_TASK_REQUEST" {
				t.Errorf("%s: code = %v", name, code)
			}
			checkAgainstSpec(t, "Problem", p)
		}
	})
}

func TestSubmitRejectsTheP0InvalidExamples(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"invalid-arbitrary-url-input.json", "invalid-client-selected-worker.json", "invalid-effect-mode.json"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "external", "examples", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		res := e.submit(aliceToken, "key-"+name[:12]+"x", string(raw))
		if res.status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400\n%s", name, res.status, res.body)
		}
	}
}

func TestPolicyRejectionsUseTheDeclaredStatuses(t *testing.T) {
	e := newEnv(t)
	withExecutor := strings.Replace(validRequest, "dev.example.transform", "evil.tool", 1)
	p := wantProblem(t, e.submit(aliceToken, "key-00000001", withExecutor), http.StatusUnprocessableEntity, "EXECUTOR_NOT_APPROVED")
	if p["policy_reason_category"] != "EXECUTOR" {
		t.Errorf("policy_reason_category = %v", p["policy_reason_category"])
	}
}

func TestIdempotentSubmission(t *testing.T) {
	e := newEnv(t)
	first := decode(t, e.submit(aliceToken, "key-00000001", validRequest))
	again := e.submit(aliceToken, "key-00000001", validRequest)
	if again.status != http.StatusAccepted || decode(t, again)["task_id"] != first["task_id"] {
		t.Fatalf("replay = %d %s, want the original task %v", again.status, again.body, first["task_id"])
	}
	different := strings.Replace(validRequest, `"normalize"`, `"other"`, 1)
	wantProblem(t, e.submit(aliceToken, "key-00000001", different), http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT")

	bob := decode(t, e.submit(bobToken, "key-00000001", validRequest))
	if bob["task_id"] == first["task_id"] {
		t.Fatal("two principals shared an idempotency key")
	}
}

func TestQuotaRejectionIsRetryableWithRetryAfter(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		if res := e.submit(aliceToken, fmt.Sprintf("key-quota-%03d", i), validRequest); res.status != http.StatusAccepted {
			t.Fatalf("submit %d status = %d", i, res.status)
		}
	}
	res := e.submit(aliceToken, "key-quota-999", validRequest)
	p := wantProblem(t, res, http.StatusTooManyRequests, "TASK_QUOTA_EXCEEDED")
	if p["retryable"] != true || res.header.Get("Retry-After") == "" {
		t.Errorf("a quota rejection must be retryable with Retry-After: %v %q", p["retryable"], res.header.Get("Retry-After"))
	}
}

func TestTasksAreVisibleOnlyToTheirOwner(t *testing.T) {
	e := newEnv(t)
	id := decode(t, e.submit(aliceToken, "key-00000001", validRequest))["task_id"].(string)

	foreign := e.get(bobToken, id)
	absent := e.get(bobToken, "tsk_00000000000000000000000000000009")
	wantProblem(t, foreign, http.StatusNotFound, "TASK_NOT_FOUND")
	if foreign.status != absent.status || !bytes.Equal(foreign.body, absent.body) {
		t.Fatalf("a foreign task is distinguishable from an absent one:\n%s\n%s", foreign.body, absent.body)
	}

	wantProblem(t, e.cancel(bobToken, id, "key-cancel-001"), http.StatusNotFound, "TASK_NOT_FOUND")
	if got := decode(t, e.get(aliceToken, id))["status"]; got != "queued" {
		t.Fatalf("a foreign cancel changed the task: status = %v", got)
	}
}

func TestCancel(t *testing.T) {
	e := newEnv(t)
	id := decode(t, e.submit(aliceToken, "key-00000001", validRequest))["task_id"].(string)

	t.Run("requires an idempotency key", func(t *testing.T) {
		wantProblem(t, e.cancel(aliceToken, id, ""), http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED")
	})
	t.Run("accepted and visible", func(t *testing.T) {
		res := e.cancel(aliceToken, id, "key-cancel-001")
		if res.status != http.StatusAccepted {
			t.Fatalf("status = %d\n%s", res.status, res.body)
		}
		body := decode(t, res)
		checkAgainstSpec(t, "CancelAccepted", body)

		status := decode(t, e.get(aliceToken, id))
		if status["status"] != "cancelled" || status["terminal_code"] != "CANCELLED" {
			t.Fatalf("status after cancel = %v", status)
		}
		checkAgainstSpec(t, "TaskPublicStatus", status)
	})
	t.Run("repeating is accepted", func(t *testing.T) {
		if res := e.cancel(aliceToken, id, "key-cancel-002"); res.status != http.StatusAccepted {
			t.Fatalf("status = %d\n%s", res.status, res.body)
		}
	})
	t.Run("unknown task", func(t *testing.T) {
		wantProblem(t, e.cancel(aliceToken, "tsk_00000000000000000000000000000009", "key-cancel-003"), http.StatusNotFound, "TASK_NOT_FOUND")
	})
}

func TestRoutingAndMethods(t *testing.T) {
	e := newEnv(t)

	t.Run("unknown path", func(t *testing.T) {
		wantProblem(t, e.do(http.MethodGet, "/v2/tasks", aliceToken, nil, ""), http.StatusNotFound, "NOT_FOUND")
	})
	t.Run("wrong method", func(t *testing.T) {
		res := e.do(http.MethodGet, "/v1/tasks", aliceToken, nil, "")
		wantProblem(t, res, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		if res.header.Get("Allow") != http.MethodPost {
			t.Errorf("Allow = %q", res.header.Get("Allow"))
		}
	})
	t.Run("delete is not a thing", func(t *testing.T) {
		wantProblem(t, e.do(http.MethodDelete, "/v1/tasks/tsk_00000000000000000000000000000001", aliceToken, nil, ""), http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	})
	t.Run("operations without an implementation say so", func(t *testing.T) {
		for _, tc := range []struct{ method, path string }{
			{http.MethodGet, "/v1/tasks/tsk_00000000000000000000000000000001/result"},
			{http.MethodPost, "/v1/artifacts/ingest-intents"},
			{http.MethodGet, "/v1/artifacts/art_1"},
			{http.MethodPut, "/v1/artifact-uploads/up_1"},
		} {
			wantProblem(t, e.do(tc.method, tc.path, aliceToken, nil, ""), http.StatusNotImplemented, "NOT_IMPLEMENTED")
		}
	})
	t.Run("path tricks do not reach a handler", func(t *testing.T) {
		for _, path := range []string{"/v1/tasks/", "/v1/tasks//cancel", "/v1/tasks/a/b/c", "/v1/tasks/../tasks"} {
			res := e.do(http.MethodGet, path, aliceToken, nil, "")
			if res.status == http.StatusOK || res.status == http.StatusAccepted {
				t.Errorf("%s: status = %d", path, res.status)
			}
		}
	})
}

func TestInternalFailuresAreGeneric(t *testing.T) {
	e := newEnv(t, func(c *Config, _ *admission.Policy) { c.Authenticator = panicAuth{} })
	res := e.get(aliceToken, "tsk_00000000000000000000000000000001")
	p := wantProblem(t, res, http.StatusInternalServerError, "INTERNAL_ERROR")
	if strings.Contains(string(res.body), "boom") {
		t.Fatalf("the panic value leaked to the Client: %s", res.body)
	}
	if p["retryable"] != true {
		t.Errorf("retryable = %v", p["retryable"])
	}
}

type panicAuth struct{}

func (panicAuth) Authenticate(*http.Request) (Identity, error) { panic("boom: secret internal state") }

func TestNewHandlerRequiresAnAuthenticator(t *testing.T) {
	svc, err := admission.New(admission.Config{Store: memory.New(), Policy: admission.Policy{
		ApprovedExecutors: []string{"a"}, MaxAttempts: 1, MaxOutstandingTasks: 1,
		PerTask: admission.ResourceCeiling{CPUMillis: 1, MemoryBytes: 1, WallTimeSeconds: 1, OutputBytesMax: 1},
	}})
	if err != nil {
		t.Fatalf("admission.New() error = %v", err)
	}
	if _, err := NewHandler(Config{Service: svc}); err == nil {
		t.Fatal("a handler without an authenticator was created: the API could be served anonymously")
	}
	if _, err := NewHandler(Config{Authenticator: panicAuth{}}); err == nil {
		t.Fatal("a handler without a service was created")
	}
}

func TestStaticTokensConfiguration(t *testing.T) {
	good := TokenEntry{Principal: "alice", Role: RoleClient, TokenSHA256: HashToken("a-long-enough-token-0123456")}
	for name, entries := range map[string][]TokenEntry{
		"empty":           nil,
		"no principal":    {{Role: RoleClient, TokenSHA256: good.TokenSHA256}},
		"unknown role":    {{Principal: "a", Role: "root", TokenSHA256: good.TokenSHA256}},
		"short hash":      {{Principal: "a", Role: RoleClient, TokenSHA256: "abcd"}},
		"non-hex hash":    {{Principal: "a", Role: RoleClient, TokenSHA256: strings.Repeat("z", 64)}},
		"duplicate token": {good, {Principal: "b", Role: RoleClient, TokenSHA256: good.TokenSHA256}},
	} {
		if _, err := NewStaticTokens(entries); err == nil {
			t.Errorf("%s: configuration was accepted", name)
		}
	}
	if _, err := NewStaticTokens([]TokenEntry{good}); err != nil {
		t.Fatalf("a valid configuration was rejected: %v", err)
	}
}

func TestServerTimeoutsAreSet(t *testing.T) {
	s := NewServer("127.0.0.1:0", http.NotFoundHandler())
	if s.ReadHeaderTimeout == 0 || s.ReadTimeout == 0 || s.WriteTimeout == 0 || s.IdleTimeout == 0 || s.MaxHeaderBytes == 0 {
		t.Fatalf("an unbounded server setting remains: %+v", s)
	}
}
